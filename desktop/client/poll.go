package main

/*
#include "ui.h"
*/
import "C"

import (
	"context"
	"keyparcel/core"
	"reflect"
	"runtime/cgo"
	"time"
)

type liveSnapshot struct {
	Profile            profile
	Items              []core.Item
	Rejected           []core.Rejected
	Devices            []core.Device
	InboxOK, DevicesOK bool
}

func (m *model) watch() { C.ui_watch(m.ui) }

func (m *model) connection(state string) {
	if state != m.loggedConnection {
		m.options.diagnostics.connection(state)
		m.loggedConnection = state
	}
	cstrings([]string{state}, func(v []*C.char) { C.ui_connection(m.ui, v[0]) })
}

func (m *model) requestRefresh() {
	if m.target == "" {
		m.message("Configure a server in Settings first")
		return
	}
	if m.active != nil || m.manualCancel != nil {
		return
	}
	m.profileVersion++
	version, target := m.profileVersion, m.target
	started := time.Now()
	m.options.diagnostics.start("Refresh")
	generation := m.session.stamp()
	ctx, cancel := context.WithTimeout(m.ctx, m.options.timeout)
	m.manualCancel = cancel
	m.refreshRequested = true
	m.message("")
	C.ui_refreshing(m.ui, 1)
	C.ui_ref(m.ui)
	go func() {
		snapshot, err := m.fetchSnapshot(ctx, target, false, generation)
		cancel()
		handle := cgo.NewHandle(func() {
			m.options.diagnostics.finish("Refresh", started, nil, err, m.closed || version != m.profileVersion || m.active != nil)
			m.finishManualRefresh(snapshot, version, err)
		})
		C.ui_post(m.ui, C.uintptr_t(handle))
	}()
}

func (m *model) finishManualRefresh(snapshot liveSnapshot, version uint64, err error) {
	m.manualCancel = nil
	// Only this refresh may clear its presentation; newer work owns progress/Cancel.
	if m.closed || version != m.profileVersion || m.active != nil {
		return
	}
	manual := m.refreshRequested
	m.refreshRequested = false
	C.ui_refreshing(m.ui, 0)
	if !m.publishSnapshot(snapshot, version) {
		return
	}
	if err == nil {
		m.options.diagnostics.background(nil)
	}
	if err != nil && manual {
		m.message("Refresh failed; last successful data retained: " + err.Error())
	}
}

func (m *model) operationProgress() string { return C.GoString(C.ui_operation_progress(m.ui)) }
func (m *model) activateCancel() bool      { return C.ui_activate_cancel(m.ui) != 0 }

func (m *model) poll() {
	if m.active != nil || m.manualCancel != nil || m.polling || m.target == "" || time.Now().Before(m.retryAt) {
		return
	}
	m.polling = true
	version, target := m.profileVersion, m.target
	generation := m.session.stamp()
	ctx, cancel := context.WithTimeout(m.ctx, m.options.timeout)
	m.pollCancel = cancel
	C.ui_ref(m.ui)
	go func() {
		snapshot, err := m.fetchSnapshot(ctx, target, true, generation)
		cancel()
		handle := cgo.NewHandle(func() {
			m.polling = false
			m.pollCancel = nil
			if !m.publishSnapshot(snapshot, version) {
				return
			}
			m.options.diagnostics.background(err)
			if err != nil {
				if m.retryDelay == 0 {
					m.retryDelay = time.Second
				}
				m.retryAt = time.Now().Add(m.retryDelay)
				m.retryDelay = min(m.retryDelay*2, 30*time.Second)
				return
			}
			m.hasSnapshot = true
			m.retryDelay = time.Second
			m.retryAt = time.Time{}
		})
		C.ui_post(m.ui, C.uintptr_t(handle))
	}()
}

func (m *model) fetchSnapshot(ctx context.Context, target string, background bool, generation uint64) (liveSnapshot, error) {
	p, err := m.store.load(target)
	snapshot := liveSnapshot{Profile: p}
	if err == nil {
		_, err = m.session.request(ctx, p, m.options.sshConfig, m.options.helper, false, background, generation, func(s *core.Session, p profile) (any, error) {
			var err error
			snapshot.Items, snapshot.Rejected, _, err = s.List(p.Config)
			if err == nil {
				snapshot.InboxOK = true
				snapshot.Devices, err = s.Discover(p.Config)
				snapshot.DevicesOK = err == nil
			}
			return nil, err
		})
	}
	return snapshot, err
}

func (m *model) publishSnapshot(snapshot liveSnapshot, version uint64) bool {
	// A later user operation owns the current rows and trust-dependent results.
	if m.closed || version != m.profileVersion || m.active != nil {
		return false
	}
	if p := snapshot.Profile; p.Config != nil && (m.target != p.Target || m.host != p.Host || !reflect.DeepEqual(m.peers, p.Config.Peers) || !reflect.DeepEqual(m.hosts, p.Config.Hosts)) {
		m.applyProfile(p)
	}
	if snapshot.InboxOK && (!reflect.DeepEqual(m.items, snapshot.Items) || !reflect.DeepEqual(m.rejected, snapshot.Rejected) || !m.hasSnapshot) {
		m.items, m.rejected = snapshot.Items, snapshot.Rejected
		m.drawInbox(m.rejected)
	}
	if snapshot.DevicesOK && (!reflect.DeepEqual(m.devices, snapshot.Devices) || !m.hasSnapshot) {
		m.devices = snapshot.Devices
		m.drawDevices()
	}
	if snapshot.InboxOK {
		m.hasSnapshot = true
	}
	return true
}
