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

func (m *model) syncStatus(state, detail string) {
	cstrings([]string{state, visible(detail)}, func(v []*C.char) { C.ui_sync(m.ui, v[0], v[1]) })
}

func (m *model) requestRefresh(message string) {
	m.refreshMessage = message
	m.retryAt = time.Time{}
	m.message("Checking inbox and devices…")
	C.ui_refreshing(m.ui, 1)
	m.poll()
}

func (m *model) poll() {
	if m.active != nil || m.polling || m.target == "" || time.Now().Before(m.retryAt) {
		return
	}
	m.polling = true
	version, target := m.profileVersion, m.target
	ctx, cancel := context.WithTimeout(m.ctx, m.options.timeout)
	m.pollCancel = cancel
	if !m.hasSnapshot {
		m.syncStatus("Loading", "Refreshing inbox and devices")
	}
	C.ui_ref(m.ui)
	go func() {
		p, err := m.store.load(target)
		snapshot := liveSnapshot{Profile: p}
		if err == nil {
			_, err = m.session.request(ctx, p, m.options.sshConfig, m.options.helper, false, func(s *core.Session, p profile) (any, error) {
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
		cancel()
		handle := cgo.NewHandle(func() {
			m.polling = false
			m.pollCancel = nil
			if m.closed {
				return
			}
			if version != m.profileVersion || m.active != nil {
				// An intervening user operation owns the current state; discard old results.
				return
			}
			if p := snapshot.Profile; p.Config != nil && (m.target != p.Target || m.host != p.Host || !reflect.DeepEqual(m.peers, p.Config.Peers)) {
				m.applyProfile(snapshot.Profile)
			}
			message := m.refreshMessage
			m.refreshMessage = ""
			C.ui_refreshing(m.ui, 0)

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
			if err != nil {
				m.syncStatus("Stale", err.Error())
				if m.retryDelay == 0 {
					m.retryDelay = time.Second
				}
				m.retryAt = time.Now().Add(m.retryDelay)
				m.retryDelay = min(m.retryDelay*2, 30*time.Second)
				if message != "" {
					m.message("Refresh failed; last successful data retained: " + err.Error())
				}
				return
			}
			m.hasSnapshot = true
			m.retryDelay = time.Second
			m.retryAt = time.Time{}
			m.syncStatus("Current", "Inbox and Devices refresh automatically")
			if message != "" {
				m.message(message)
			}
		})
		C.ui_post(m.ui, C.uintptr_t(handle))
	}()
}
