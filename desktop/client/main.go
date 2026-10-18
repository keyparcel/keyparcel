package main

/*
#cgo pkg-config: gtk4
#include "ui.h"
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/cgo"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"keyparcel/assets/branding"
	"keyparcel/core"
	"keyparcel/internal/buildinfo"
	"keyparcel/internal/display"
)

type options struct {
	config, sshConfig, host, helper string
	timeout                         time.Duration
	diagnostics                     *diagnostics
}
type inbox struct {
	Items    []core.Item
	Rejected []core.Rejected
}
type pendingSave struct {
	ID, Path string
	Profile  profile
	View     *received
}
type model struct {
	ui                                *C.UI
	options                           options
	store                             store
	ctx                               context.Context
	cancel                            context.CancelFunc
	active                            context.CancelFunc
	workers                           chan struct{}
	sequence                          uint64
	closed                            bool
	loggedConnection                  string
	session                           sessionOwner
	profileVersion                    uint64
	polling, hasSnapshot              bool
	pollCancel                        context.CancelFunc
	manualCancel                      context.CancelFunc
	retryAt                           time.Time
	retryDelay                        time.Duration
	refreshRequested                  bool
	confirmation                      confirmation
	rejected                          []core.Rejected
	target, public, fingerprint, file string
	items                             []core.Item
	devices                           []core.Device
	peers                             map[string]string
	hosts                             map[string]core.Host
	navigation                        navigation
	host                              core.Host
	view                              *received
	save                              *pendingSave
	forget                            *core.Device
	deleteInbox                       *pendingDelete
	addingRecipient                   *profile
	recipient                         string
	extraRecipients                   []string
	settingsLoaded                    bool
	enrollmentVersion                 uint64
	enrollmentOpen                    bool
	enrollment                        enrollmentDetails
	feedback                          *sendFeedback
	sending                           *sendAttempt
}

func cstrings(values []string, call func([]*C.char)) {
	native := make([]*C.char, len(values))
	for i, value := range values {
		native[i] = C.CString(value)
	}
	defer func() {
		for _, value := range native {
			C.free(unsafe.Pointer(value))
		}
	}()
	call(native)
}
func (m *model) get(name string) string {
	var value string
	cstrings([]string{name}, func(v []*C.char) { value = C.GoString(C.ui_get(m.ui, v[0])) })
	return value
}
func (m *model) set(name, text string) {
	cstrings([]string{name, visible(text)}, func(v []*C.char) { C.ui_set(m.ui, v[0], v[1]) })
}
func (m *model) message(text string) {
	if text != "" {
		m.refreshRequested = false
	}
	cstrings([]string{visible(text)}, func(v []*C.char) { C.ui_message(m.ui, v[0]) })
}
func (m *model) confirm(text string) {
	m.confirmation = confirmation{text, time.Now().Add(3 * time.Second)}
	cstrings([]string{text}, func(v []*C.char) { C.ui_confirmation(m.ui, v[0]) })
}
func (m *model) progress(text string) {
	cstrings([]string{text}, func(v []*C.char) { C.ui_progress(m.ui, v[0]) })
}
func (m *model) page(name string) {
	if m.navigation.current == "settings" && name != "settings" {
		m.clearEnrollment()
	}
	if m.navigation.current != name {
		m.refreshRequested = false
	}
	m.navigation.open(name)
	cstrings([]string{name}, func(v []*C.char) { C.ui_page(m.ui, v[0]) })
}
func (m *model) clear(name string) {
	cstrings([]string{name}, func(v []*C.char) { C.ui_clear(m.ui, v[0]) })
}
func (m *model) copy(text string) bool {
	started := time.Now()
	m.options.diagnostics.start("Copy")
	var copyError error
	defer func() { m.options.diagnostics.finish("Copy", started, nil, copyError, false) }()
	m.confirm("")
	if err := clipboardText(text); err != nil {
		copyError = err
		m.message(err.Error())
		return false
	}
	accepted := false
	cstrings([]string{text}, func(v []*C.char) { accepted = C.ui_copy(m.ui, v[0]) != 0 })
	if !accepted {
		copyError = errors.New("clipboard rejected")
		m.message("Clipboard could not accept this text")
		return false
	}
	m.confirm("Copied")
	return true
}
func visible(text string) string {
	text = strings.ToValidUTF8(strings.ReplaceAll(text, "\x00", "␀"), "�")
	runes := []rune(text)
	if len(runes) > 65536 {
		return string(runes[:65536]) + "…"
	}
	return text
}

//export goRelease
func goRelease(handle C.uintptr_t) { cgo.Handle(handle).Delete() }

//export goDispatch
func goDispatch(handle C.uintptr_t) { cgo.Handle(handle).Value().(func())() }

//export goEvent
func goEvent(handle C.uintptr_t, action, arg, value *C.char) {
	m := cgo.Handle(handle).Value().(*model)
	m.event(C.GoString(action), C.GoString(arg), C.GoString(value))
}

func (m *model) run(notice string, work func(context.Context) (any, error), done func(any), failed ...func(error)) bool {
	if m.active != nil {
		return false
	}
	select {
	case m.workers <- struct{}{}:
	default:
		m.message("File workers are still finishing; try again shortly.")
		return false
	}
	started := time.Now()
	m.options.diagnostics.start(notice)
	m.refreshRequested = false
	C.ui_refreshing(m.ui, 0)
	m.profileVersion++
	m.sequence++
	sequence := m.sequence
	ctx, cancel := context.WithTimeout(m.ctx, m.options.timeout)
	m.active = cancel
	C.ui_busy(m.ui, 1)
	m.confirm("")
	m.message("")
	m.progress(notice)
	C.ui_ref(m.ui)
	type result struct {
		value any
		err   error
	}
	results := make(chan result, 1)
	go func() { defer func() { <-m.workers }(); value, err := work(ctx); results <- result{value, err} }()
	go func() {
		var outcome result
		select {
		case outcome = <-results:
		case <-ctx.Done():
			outcome.err = ctx.Err()
		}
		cancel()
		callback := cgo.NewHandle(func() {
			m.options.diagnostics.finish(notice, started, outcome.value, outcome.err, m.closed || m.sequence != sequence)
			if m.closed || m.sequence != sequence {
				return
			}
			m.active = nil
			m.retryAt = time.Time{}
			C.ui_busy(m.ui, 0)
			if outcome.err != nil {
				var changed *recipientChanged
				if errors.As(outcome.err, &changed) {
					m.applyProfile(changed.profile)
				}
				message := outcome.err.Error()
				if errors.Is(outcome.err, context.DeadlineExceeded) {
					message = "Operation timed out; remote transfers retained. Try again."
				}
				if errors.Is(outcome.err, context.Canceled) {
					message = "Operation cancelled; remote transfers retained."
				}
				m.message(message)
				if len(failed) > 0 {
					failed[0](outcome.err)
				}
				return
			}
			done(outcome.value)
			m.poll()
		})
		C.ui_post(m.ui, C.uintptr_t(callback))
	}()
	return true
}

func (m *model) load(target string) {
	m.run("Loading settings…", func(context.Context) (any, error) {
		p, err := m.store.load(target)
		if p.Config == nil {
			return nil, err
		}
		return profileLoad{p, err}, nil
	}, func(value any) {
		result := value.(profileLoad)
		p := result.Profile
		m.settingsLoaded = false
		m.applyProfile(p)
		if result.Err != nil {
			m.message(result.Err.Error())
		}
		if p.Target == "" {
			m.page("settings")
		}
	})
}
func (m *model) applyProfile(p profile) {
	profileChanged := m.target != p.Target || m.host != p.Host
	if profileChanged {
		if m.host.Identity != "" {
			m.options.diagnostics.profileSwitch()
		} else {
			m.options.diagnostics.resetBackground()
		}
		m.clearEnrollment()
	}
	trustChanged := m.target != p.Target || m.host != p.Host || !reflect.DeepEqual(m.peers, p.Config.Peers)
	if trustChanged {
		m.options.diagnostics.resetBackground()
		m.session.invalidate(p.Target, p.Host)
		m.profileVersion++
		m.items, m.devices, m.rejected = nil, nil, nil
		m.view, m.save = nil, nil
		m.deleteInbox = nil
		m.addingRecipient = nil
		if m.target != p.Target || m.host != p.Host {
			m.clearSendFeedback()
		}
		C.ui_clear_results(m.ui)
		if m.navigation.current == "text" || m.navigation.current == "saved" {
			m.page("inbox")
		}
		m.clear("items")
		m.clear("device-list")
		m.hasSnapshot = false
		m.retryAt = time.Time{}
		m.connection(m.session.state(p.Target, p.Host))
	}
	recipient := retainedRecipient(m.recipient, m.target, m.host, m.peers, p)
	for _, name := range m.extraRecipients {
		if retainedRecipient(name, m.target, m.host, m.peers, p) == "" {
			recipient = ""
			break
		}
	}
	if recipient == "" {
		m.extraRecipients = nil
	}
	if m.sending != nil && !m.sending.matches(p) {
		m.clearSendAttempt()
		recipient = ""
		m.extraRecipients = nil
		m.message("Server or recipient approval changed; choose recipients again. Previous deliveries are not undone.")
	}
	m.peers = p.Config.Peers
	m.hosts = p.Config.Hosts
	m.host = p.Host
	m.target = p.Target
	m.public = p.Public
	m.fingerprint = p.Fingerprint
	targetText := m.get("target")
	C.ui_settings_edit(m.ui, 1)
	m.clear("hosts")
	for _, name := range sortedKeys(p.Config.Hosts) {
		cstrings([]string{name}, func(v []*C.char) { C.ui_host(m.ui, v[0]) })
	}
	m.set("target", targetText)
	C.ui_settings_edit(m.ui, 0)
	if !m.settingsLoaded {
		m.settingsFields(p)
	}
	m.set("active-host", "Host: "+p.Target)
	m.recipient = recipient
	m.set("ssh-config", m.options.sshConfig)
	m.drawSend()
}

func (m *model) remote(ctx context.Context, target string, expected *core.Host, action func(*core.Session, profile) (any, error)) (any, error) {
	generation := m.session.stamp()
	p, err := m.store.load(target)
	if err != nil {
		return nil, err
	}
	if p.Target == "" {
		return nil, errors.New("configure a host in Settings first")
	}
	if expected != nil && p.Host != *expected {
		return nil, &recipientChanged{p}
	}
	return m.session.request(ctx, p, m.options.sshConfig, m.options.helper, true, false, generation, action)
}
func (m *model) refreshInbox() { m.requestRefresh() }
func (m *model) drawInbox(rejected []core.Rejected) {
	cstrings([]string{"items"}, func(v []*C.char) { C.ui_rows_begin(m.ui, v[0]) })
	defer C.ui_rows_end(m.ui)
	if len(m.items) == 0 && len(rejected) == 0 {
		cstrings([]string{"", "Inbox is empty", ""}, func(v []*C.char) { C.ui_item(m.ui, v[0], v[1], v[2], 0) })
	}
	for _, item := range m.items {
		name := "Text"
		canView := boolean(item.Size <= viewLimit)
		if item.Filename != nil {
			name = "File: " + *item.Filename
		}
		title := visible(item.From + " · " + name)
		detail := display.Size(item.Size) + " · Uploaded " + item.UploadedAt.Local().Format("2006-01-02 15:04:05 -07:00")
		cstrings([]string{item.ID, title, detail}, func(v []*C.char) { C.ui_item(m.ui, v[0], v[1], v[2], canView) })
	}
	for _, bad := range rejected {
		cstrings([]string{"", "Not verified: " + bad.ID, visible(bad.Reason)}, func(v []*C.char) { C.ui_item(m.ui, v[0], v[1], v[2], 0) })
	}
}
func (m *model) refreshDevices() {
	m.requestRefresh()
}
func (m *model) drawDevices() {
	cstrings([]string{"device-list"}, func(v []*C.char) { C.ui_rows_begin(m.ui, v[0]) })
	defer C.ui_rows_end(m.ui)
	for _, d := range m.devices {
		trust := "Unapproved"
		if d.Trusted {
			trust = "Approved as " + d.Name
		}
		if d.Self {
			trust = "This device"
		}
		if !d.Supported {
			trust = "Unsupported key"
		}
		if !d.Discovered {
			trust += " — not discovered"
		}
		cstrings([]string{d.Fingerprint, visible(trust + "\n" + d.Fingerprint + "\n" + d.Comment)}, func(v []*C.char) {
			C.ui_device(m.ui, v[0], v[1], boolean(d.Supported), boolean(d.Trusted), boolean(d.Self))
		})
	}
}
func boolean(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
func (m *model) device(fp string) (core.Device, bool) {
	for _, d := range m.devices {
		if d.Fingerprint == fp {
			return d, true
		}
	}
	return core.Device{}, false
}

func (m *model) event(action, arg, value string) {
	if action == "close" {
		m.closed = true
		m.view = nil
		m.save = nil
		m.cancel()
		m.clearSendAttempt()
		if m.active != nil {
			m.active()
		}
		return
	}
	if action == "cancel" {
		if m.active != nil {
			m.active()
		} else if m.manualCancel != nil {
			m.profileVersion++
			m.manualCancel()
			m.refreshRequested = false
			C.ui_refreshing(m.ui, 0)
			m.message("Operation cancelled; remote transfers retained.")
		} else if m.pollCancel != nil {
			m.profileVersion++
			m.pollCancel()
			m.refreshRequested = false
			C.ui_refreshing(m.ui, 0)
			m.message("Operation cancelled; remote transfers retained.")
		}
		return
	}
	if m.closed {
		return
	}
	switch action {
	case "dismiss-error":
		m.message("")
	case "picker-cancel":
		// Cancelling a native picker leaves the existing draft or viewer intact.
	case "poll":
		m.connection(m.session.state(m.target, m.host))
		if m.confirmation.Text != "" && m.confirmation.current(time.Now()) == "" {
			m.confirmation = confirmation{}
			cstrings([]string{""}, func(v []*C.char) { C.ui_confirmation(m.ui, v[0]) })
		}
		if m.feedback != nil && m.feedback.expire(time.Now()) {
			m.drawFeedback()
		}
		m.poll()
	case "feedback-expanded":
		if m.feedback != nil {
			m.feedback.expand(arg == "true", time.Now())
		}
	case "notice", "error":
		m.message(arg)
	case "back":
		if m.active != nil {
			return
		}
		if m.navigation.current == "saved" && m.save != nil && m.save.View != nil {
			m.page("text")
			m.save = nil
			return
		}
		if parent := m.navigation.parent(); parent != "" {
			m.event("page", parent, "")
		}
	case "result-back":
		m.event("back", "", "")
	case "page":
		C.ui_clear_results(m.ui)
		m.view = nil
		m.save = nil
		m.page(arg)

	case "host-selected":
		m.clearEnrollment()
		if host, ok := m.hosts[arg]; ok {
			m.settingsFields(profile{Target: arg, Host: host})
		}
	case "identity-changed":
		m.clearEnrollment()
	case "choose-identity":
		C.ui_identity_picker(m.ui)
	case "identity-path":
		m.clearEnrollment()
		m.set("identity", arg)
	case "enrollment-toggle":
		if arg == "true" {
			m.openEnrollment()
		} else {
			m.clearEnrollment()
		}
	case "save-connect":
		m.saveAndConnect()
	case "inbox":
		m.refreshInbox()
	case "devices":
		m.refreshDevices()
	case "copy-key":
		if m.enrollmentOpen && m.enrollment.Public != "" {
			m.copy(m.enrollment.Public)
		}
	case "copy-identity":
		if m.enrollmentOpen && m.enrollment.Fingerprint != "" {
			m.copy(m.enrollment.Fingerprint)
		}
	case "copy-peer", "copy-id":
		m.copy(arg)
	case "details":
		cstrings([]string{arg}, func(v []*C.char) { C.ui_details(m.ui, v[0]) })
	case "approve-request":
		if d, ok := m.device(arg); ok && d.Supported && !d.Self {
			p, err := m.store.load(m.target)
			if err != nil {
				m.message(err.Error())
				return
			}
			name := core.SuggestedPeerName(d.Comment, p.Config.Peers)
			cstrings([]string{arg, "Verify this device independently:\n" + arg + "\n" + visible(d.Comment), name}, func(v []*C.char) { C.ui_approve(m.ui, v[0], v[1], v[2]) })
		}
	case "approve":
		d, ok := m.device(arg)
		if !ok {
			m.message("Refresh Devices before approval")
			return
		}
		m.run("Saving peer approval…", func(context.Context) (any, error) { return nil, m.store.approve(value, d.PublicKey) }, func(any) { m.refreshDevices(); m.confirm("Device approved") })
	case "forget-request":
		d, ok := m.device(arg)
		if ok {
			m.forget = &d
			cstrings([]string{"forget", "Forget approval for " + d.Name + "?\n" + d.Fingerprint}, func(v []*C.char) { C.ui_confirm(m.ui, v[0], v[1]) })
		}
	case "forget":
		if m.forget == nil {
			return
		}
		d := *m.forget
		m.forget = nil
		m.run("Forgetting peer approval…", func(context.Context) (any, error) { return nil, m.store.forget(d.Name, d.PublicKey) }, func(any) { m.refreshDevices(); m.confirm("Device unapproved") })
	case "choose-file":
		cstrings([]string{""}, func(v []*C.char) { C.ui_picker(m.ui, 0, v[0]) })
	case "send-path":
		m.run("Checking selected file…", func(context.Context) (any, error) {
			f, err := openInput(arg)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			return f.Stat()
		}, func(v any) {
			m.file = arg
			info := v.(os.FileInfo)
			m.set("selected-file", filepath.Base(arg)+" · "+display.Size(info.Size()))
			C.ui_send_ready(m.ui, 1)
			m.drawSend()
		})
	case "discard-draft":
		m.recipient = ""
		m.clearSendAttempt()
		m.extraRecipients = nil
		m.clearSendFeedback()
		C.ui_clear_draft(m.ui, 0)
		m.file = ""
		m.set("selected-file", "No file selected")
		C.ui_send_ready(m.ui, 0)
		m.drawSend()
		m.confirm("Draft discarded")
	case "add-recipient-request":
		m.chooseRecipient()
	case "recipient-cancel":
		m.addingRecipient = nil
	case "add-recipient":
		m.addRecipient(value)
	case "remove-recipient":
		m.removeRecipient(arg)
	case "retry-send":
		m.retrySend()
	case "edit-send":
		m.clearSendAttempt()
		m.extraRecipients = nil
		m.recipient = ""
		m.clearSendFeedback()
		m.drawSend()
	case "send-text", "send-file":
		m.send(action == "send-file")
	case "delete-request":
		m.requestDelete(arg)
	case "delete-confirm":
		m.confirmDelete(arg)
	case "delete-cancel":
		m.deleteInbox = nil
	case "receive-text":
		m.receiveText(arg)
	case "receive-file":
		m.beginSave(arg, nil)
	case "save-view":
		if m.view != nil {
			m.beginSave(m.view.ID, m.view)
		}
	case "save-path":
		m.savePath(arg, false)
	case "replace":
		if m.save != nil {
			m.savePath(m.save.Path, true)
		}
	case "copy-message":
		if m.view != nil && m.view.Text {
			if m.copy(string(m.view.Data)) {
				m.confirm("Complete text copied")
			}
		}
	case "copy-selection":
		if m.view == nil {
			return
		}
		if !m.view.Text {
			return
		}
		text := C.ui_selection(m.ui)
		if text == nil {
			return
		}
		selection := C.GoString(text)
		C.g_free(C.gpointer(text))
		m.copy(selection)
	case "remove":
		m.remove()
	}
}

func (m *model) receiveText(id string) {
	var item *core.Item
	for i := range m.items {
		if m.items[i].ID == id {
			item = &m.items[i]
			break
		}
	}
	if item == nil {
		m.message("Refresh Inbox before viewing this transfer")
		return
	}
	if item.Size > viewLimit {
		m.message(tooLargeToView)
		return
	}
	captured := profile{Target: m.target, Host: m.host, Config: &core.Config{Peers: m.peers}}
	target := m.target
	m.run("Verifying parcel…", func(ctx context.Context) (any, error) {
		return m.remote(ctx, target, &captured.Host, func(s *core.Session, p profile) (any, error) {
			if !(pendingDelete{Profile: captured}).matches(p) {
				return nil, &recipientChanged{p}
			}
			var view *received
			_, err := s.Receive(p.Config, id, func(manifest core.Manifest, f *os.File) error {
				var err error
				view, err = readText(f, manifest, p)
				return err
			}, true)
			return view, err
		})
	}, func(v any) {
		view := v.(*received)
		if !(pendingDelete{Profile: view.Profile}).matches(m.currentProfile()) {
			m.message("The server or approvals changed; view the parcel again")
			return
		}
		m.view = view
		m.save = nil
		cstrings([]string{view.Preview, view.Notice}, func(v []*C.char) { C.ui_view(m.ui, v[0], v[1], boolean(view.Text)) })
		m.page("text")
	})
}
func (m *model) currentProfile() profile {
	return profile{Target: m.target, Host: m.host, Config: &core.Config{Peers: m.peers}}
}
func (m *model) beginSave(id string, view *received) {
	if m.active != nil {
		return
	}
	captured := m.currentProfile()
	name := id + ".txt"
	if view != nil {
		if view.ID != id || m.view != view || !(pendingDelete{Profile: view.Profile}).matches(captured) {
			m.message("The server or approvals changed; view the parcel again")
			return
		}
		if view.Manifest.Filename != nil {
			name = *view.Manifest.Filename
		}
	} else {
		found := false
		for _, item := range m.items {
			if item.ID == id {
				found = true
				if item.Filename != nil {
					name = *item.Filename
				}
				break
			}
		}
		if !found {
			m.message("Refresh Inbox before saving this transfer")
			return
		}
	}
	m.save = &pendingSave{ID: id, Profile: captured, View: view}
	cstrings([]string{name}, func(v []*C.char) { C.ui_picker(m.ui, 1, v[0]) })
}
func (m *model) savePath(path string, overwrite bool) {
	if m.save == nil || m.active != nil {
		return
	}
	pending := *m.save
	pending.Path = path
	m.save = &pending
	m.run("Verifying and saving file…", func(ctx context.Context) (any, error) {
		p, err := m.store.load(pending.Profile.Target)
		if err != nil {
			return nil, err
		}
		if !(pendingDelete{Profile: pending.Profile}).matches(p) {
			return nil, &recipientChanged{p}
		}
		if !overwrite {
			if info, err := os.Lstat(path); err == nil {
				if !info.Mode().IsRegular() {
					return nil, errors.New("destination is not a regular file")
				}
				return "confirm", nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		if pending.View != nil {
			_, err := pending.View.save(ctx, path, overwrite)
			return "saved", err
		}
		return m.remote(ctx, pending.Profile.Target, &pending.Profile.Host, func(s *core.Session, p profile) (any, error) {
			if !(pendingDelete{Profile: pending.Profile}).matches(p) {
				return nil, &recipientChanged{p}
			}
			_, err := s.Receive(p.Config, pending.ID, func(manifest core.Manifest, f *os.File) error {
				_, err := core.SaveContentContext(ctx, path, manifest, f, overwrite)
				return err
			}, true)
			return "saved", err
		})
	}, func(v any) {
		if v.(string) == "confirm" {
			cstrings([]string{"replace", "Replace the existing regular file?\n" + path}, func(v []*C.char) { C.ui_confirm(m.ui, v[0], v[1]) })
			return
		}
		if m.save != &pending || !(pendingDelete{Profile: pending.Profile}).matches(m.currentProfile()) {
			return
		}
		C.ui_saved_origin(m.ui, boolean(pending.View != nil))
		m.set("saved-path", path)
		m.page("saved")
	})
}
func (m *model) requestDelete(id string) {
	if m.active != nil || m.deleteInbox != nil || C.ui_picker_pending(m.ui) != 0 {
		return
	}
	if !containsItem(m.items, id) {
		m.message("Refresh Inbox before deleting this transfer")
		return
	}
	p := profile{Target: m.target, Host: m.host, Config: &core.Config{Peers: m.peers}}
	m.deleteInbox = &pendingDelete{ID: id, Profile: p}
	cstrings([]string{id, "Delete this device's remote inbox copy without downloading it?\nOther recipients' copies and saved local files remain untouched."}, func(v []*C.char) { C.ui_confirm_delete(m.ui, v[0], v[1]) })
}

func (m *model) confirmDelete(id string) {
	if m.active != nil {
		return
	}
	request := m.deleteInbox
	m.deleteInbox = nil
	current := profile{Target: m.target, Host: m.host, Config: &core.Config{Peers: m.peers}}
	if request == nil || request.ID != id || !request.matches(current) || !containsItem(m.items, id) {
		m.message("The inbox or server changed; select the transfer again")
		return
	}
	m.run("Deleting remote inbox copy…", func(ctx context.Context) (any, error) {
		return m.remote(ctx, request.Profile.Target, &request.Profile.Host, func(s *core.Session, p profile) (any, error) {
			if !request.matches(p) {
				return nil, errors.New("Approvals changed; select the transfer again")
			}
			_, failures, err := s.Delete([]string{request.ID}, false)
			if len(failures) > 0 {
				return nil, fmt.Errorf("could not delete transfer: %s", failures[0].Reason)
			}
			return nil, err
		})
	}, func(any) {
		current := profile{Target: m.target, Host: m.host, Config: &core.Config{Peers: m.peers}}
		if !request.matches(current) {
			return
		}
		kept := make([]core.Item, 0, len(m.items))
		for _, item := range m.items {
			if item.ID != request.ID {
				kept = append(kept, item)
			}
		}
		m.items = kept
		m.drawInbox(m.rejected)
		m.confirm("Parcel deleted")
	})
}

func (m *model) remove() {
	var id string
	var p profile
	if m.navigation.current == "saved" && m.save != nil {
		id = m.save.ID
		p = m.save.Profile
	} else if m.view != nil {
		id = m.view.ID
		p = m.view.Profile
	} else if m.save != nil {
		id = m.save.ID
		p = m.save.Profile
	} else {
		return
	}
	m.run("Removing remote transfer…", func(ctx context.Context) (any, error) {
		return m.remote(ctx, p.Target, &p.Host, func(s *core.Session, current profile) (any, error) {
			if !(pendingDelete{Profile: p}).matches(current) {
				return nil, &recipientChanged{current}
			}
			_, failures, err := s.Delete([]string{id}, false)
			if len(failures) > 0 {
				return nil, fmt.Errorf("could not remove transfer: %s", failures[0].Reason)
			}
			return nil, err
		})
	}, func(any) {
		if !(pendingDelete{Profile: p}).matches(m.currentProfile()) {
			return
		}
		kept := m.items[:0]
		for _, item := range m.items {
			if item.ID != id {
				kept = append(kept, item)
			}
		}
		m.items = kept
		C.ui_clear_results(m.ui)
		m.view = nil
		m.save = nil
		m.drawInbox(m.rejected)
		m.page("inbox")
		m.confirm("Parcel deleted")
	})
}

func newModel(o options) *model {
	m := &model{options: o, store: store{path: o.config, diagnostics: o.diagnostics}, target: o.host, workers: make(chan struct{}, 2)}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.session.ctx = m.ctx
	m.session.diagnostics = o.diagnostics
	cstrings([]string{branding.Icon}, func(v []*C.char) {
		m.ui = C.ui_new(C.uintptr_t(cgo.NewHandle(m)), v[0])
	})
	if m.ui != nil {
		m.set("target", o.host)
		m.set("identity", defaultIdentity())
		m.set("root", ".local/share/keyparcel")
		m.set("discovery", ".ssh/authorized_keys")
		m.page("settings")
		m.watch()
	}
	return m
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("keyparcel-gtk", buildinfo.String())
		return
	}
	runtime.LockOSThread()
	if os.Getenv("KEYPARCEL_HOST_APPROVAL") == "1" {
		if len(os.Args) != 2 || !hostApprovalPrompt(os.Args[1]) {
			fmt.Fprintln(os.Stderr, "only OpenSSH host-key approval is supported")
			os.Exit(1)
		}
		accepted := false
		cstrings([]string{os.Args[1]}, func(v []*C.char) { accepted = C.ui_askpass(v[0]) != 0 })
		if accepted {
			fmt.Println("yes")
		} else {
			fmt.Println("no")
		}
		return
	}
	d := newDiagnostics(os.Stderr, false)
	d.startup()
	defaultConfig, err := core.ConfigPath()
	if err != nil {
		d.log.Error("startup_failed", "category", diagnosticCategory(err))
		os.Exit(1)
	}
	f := flag.NewFlagSet("keyparcel-gtk", flag.ExitOnError)
	o := options{}
	var verbose bool
	f.BoolVar(&verbose, "verbose", false, "safe operation and connection diagnostics on stderr")
	f.StringVar(&o.config, "config", defaultConfig, "shared CLI configuration")
	f.StringVar(&o.sshConfig, "ssh-config", "", "OpenSSH configuration override")
	f.StringVar(&o.host, "host", "", "configured SSH target")
	f.DurationVar(&o.timeout, "request-timeout", 2*time.Minute, "maximum operation lifetime")
	f.Parse(os.Args[1:])
	if f.NArg() != 0 || o.timeout <= 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments or invalid timeout")
		os.Exit(1)
	}
	o.diagnostics = newDiagnostics(os.Stderr, verbose)
	d = o.diagnostics
	o.helper, err = os.Executable()
	if err != nil {
		d.log.Error("startup_failed", "category", diagnosticCategory(err))
		os.Exit(1)
	}
	temporary, err := os.MkdirTemp("", "keyparcel-gtk-")
	if err != nil {
		d.log.Error("startup_failed", "category", diagnosticCategory(err))
		os.Exit(1)
	}
	defer os.RemoveAll(temporary)
	os.Setenv("TMPDIR", temporary)
	m := newModel(o)
	if m.ui == nil {
		d.log.Error("startup_failed", "category", "native")
		os.Exit(1)
	}
	m.page("inbox")
	m.load(o.host)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	C.ui_ref(m.ui)
	go func() {
		interrupted := false
		select {
		case <-signals:
			interrupted = true
			m.cancel()
		case <-m.ctx.Done():
		}
		callback := cgo.NewHandle(func() {
			if interrupted && !m.closed {
				C.ui_shutdown(m.ui)
			}
		})
		C.ui_post(m.ui, C.uintptr_t(callback))
	}()
	C.ui_run(m.ui)
	m.closed = true
	m.cancel()
	deadline := time.Now().Add(3 * time.Second)
	for C.ui_pending(m.ui) > 0 && time.Now().Before(deadline) {
		C.ui_iterate()
		time.Sleep(time.Millisecond)
	}
	C.ui_shutdown(m.ui)
	C.ui_unref(m.ui)
}

func hostApprovalPrompt(prompt string) bool {
	// OpenSSH's host confirmation has no SSH_ASKPASS_PROMPT hint.
	return strings.HasPrefix(prompt, "The authenticity of host ") &&
		strings.Contains(prompt, "key fingerprint is") &&
		strings.Contains(prompt, "Are you sure you want to continue connecting ")
}

func iterate()                         { C.ui_iterate() }
func (m *model) pendingCallbacks() int { return int(C.ui_pending(m.ui)) }
func (m *model) pickerPending() bool   { return C.ui_picker_pending(m.ui) != 0 }
func (m *model) cancelPicker()         { C.ui_cancel_picker(m.ui) }
func (m *model) destroy() {
	m.event("close", "", "")
	C.ui_shutdown(m.ui)
	deadline := time.Now().Add(3 * time.Second)
	for C.ui_pending(m.ui) > 0 && time.Now().Before(deadline) {
		iterate()
		time.Sleep(time.Millisecond)
	}
	C.ui_unref(m.ui)
}
