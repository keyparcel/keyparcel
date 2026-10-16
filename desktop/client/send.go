package main

/*
#include "ui.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"keyparcel/core"
)

type sendAttempt struct {
	Profile                profile
	Names                  []string
	Keys                   map[string]string
	File                   bool
	Text, Source, Filename string
	Stage                  *os.File
	Deliveries             []core.Delivery
}

func sendKey(p profile, name string) string {
	text := p.Public
	if name != "self" {
		text = p.Config.Peers[name]
	}
	key, err := core.ParsePublic(text)
	if err != nil {
		return ""
	}
	return core.PublicText(key)
}
func (a *sendAttempt) matches(p profile) bool {
	if a.Profile.Target != p.Target || a.Profile.Host != p.Host {
		return false
	}
	for name, key := range a.Keys {
		if key == "" || sendKey(p, name) != key {
			return false
		}
	}
	return true
}
func (a *sendAttempt) failed() []string {
	var names []string
	for _, d := range a.Deliveries {
		if d.Error != "" && !d.Unconfirmed {
			names = append(names, d.Recipient)
		}
	}
	return names
}
func (a *sendAttempt) merge(next []core.Delivery) {
	if len(a.Deliveries) == 0 {
		a.Deliveries = next
		return
	}
	for i, old := range a.Deliveries {
		for _, d := range next {
			if d.Recipient == old.Recipient {
				a.Deliveries[i] = d
			}
		}
	}
}
func (a *sendAttempt) summary() string {
	var lines []string
	for _, d := range a.Deliveries {
		state := "delivered"
		if d.Unconfirmed {
			state = "unconfirmed — check the inbox before sending again"
		} else if d.Error != "" {
			state = "failed — " + d.Error
		}
		lines = append(lines, recipientLabel(d.Recipient)+": "+state)
	}
	return strings.Join(lines, "\n")
}
func recipientLabel(name string) string {
	if name == "self" {
		return "This device"
	}
	return name
}
func (m *model) sendProfile() profile {
	return profile{Target: m.target, Host: m.host, Public: m.public, Config: &core.Config{Peers: m.peers}}
}
func (m *model) selectedRecipients() []string {
	var names []string
	seen := map[string]bool{}
	for _, name := range append([]string{m.get("recipient")}, m.extraRecipients...) {
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names
}
func (m *model) candidates() []string {
	p := m.sendProfile()
	selected := map[string]bool{}
	for _, name := range m.selectedRecipients() {
		selected[sendKey(p, name)] = true
	}
	var names []string
	for _, name := range append([]string{"self"}, sortedKeys(m.peers)...) {
		key := sendKey(p, name)
		if key != "" && !selected[key] {
			names = append(names, name)
			selected[key] = true
		}
	}
	return names
}
func (m *model) chooseRecipient() {
	if m.active != nil || m.sending != nil {
		return
	}
	names := m.candidates()
	if len(names) == 0 {
		m.message("All approved devices are already selected")
		return
	}
	p := m.sendProfile()
	m.addingRecipient = &p
	cstrings(names, func(v []*C.char) { C.ui_add_recipient(m.ui, &v[0], C.int(len(v))) })
}
func (m *model) addRecipient(name string) {
	previous := m.addingRecipient
	m.addingRecipient = nil
	current := m.sendProfile()
	if previous == nil || previous.Target != current.Target || previous.Host != current.Host || sendKey(*previous, name) != sendKey(current, name) {
		m.message("Host or approval changed; add the recipient again")
		return
	}
	if m.active != nil || m.sending != nil {
		return
	}
	for _, candidate := range m.candidates() {
		if candidate == name {
			if m.get("recipient") == "" {
				cstrings([]string{name}, func(v []*C.char) { C.ui_select_recipient(m.ui, v[0]) })
			} else {
				m.extraRecipients = append(m.extraRecipients, name)
			}
			m.drawSend()
			return
		}
	}
}
func (m *model) removeRecipient(name string) {
	if m.active != nil || m.sending != nil {
		return
	}
	if m.get("recipient") == name {
		next := ""
		if len(m.extraRecipients) > 0 {
			next = m.extraRecipients[0]
			m.extraRecipients = m.extraRecipients[1:]
		}
		cstrings([]string{next}, func(v []*C.char) { C.ui_select_recipient(m.ui, v[0]) })
	} else {
		var kept []string
		for _, n := range m.extraRecipients {
			if n != name {
				kept = append(kept, n)
			}
		}
		m.extraRecipients = kept
	}
	m.drawSend()
}
func (m *model) drawSend() {
	names := m.selectedRecipients()
	m.clear("recipient-tokens")
	if len(m.extraRecipients) > 0 {
		for _, name := range names {
			cstrings([]string{name, recipientLabel(name)}, func(v []*C.char) { C.ui_recipient_token(m.ui, v[0], v[1]) })
		}
	}
	retry := false
	if m.sending != nil {
		retry = len(m.sending.failed()) > 0 && m.sending.matches(m.sendProfile())
	}
	C.ui_send_state(m.ui, C.int(len(names)), boolean(m.sending != nil), boolean(retry), boolean(m.file != ""))
}
func (m *model) clearSendAttempt() {
	if m.sending != nil && m.sending.Stage != nil {
		m.sending.Stage.Close()
		os.Remove(m.sending.Stage.Name())
	}
	m.sending = nil
}
func (m *model) send(file bool) {
	if m.active != nil || m.sending != nil {
		return
	}
	names := m.selectedRecipients()
	if len(names) == 0 {
		m.message("Choose a recipient before sending")
		return
	}
	a := &sendAttempt{Profile: m.sendProfile(), Names: names, Keys: map[string]string{}, File: file, Source: m.file, Filename: filepath.Base(m.file)}
	for _, name := range names {
		a.Keys[name] = sendKey(a.Profile, name)
	}
	if file {
		f, err := os.CreateTemp("", "keyparcel-send-*")
		if err != nil {
			m.message(err.Error())
			return
		}
		a.Stage = f
	} else {
		text := C.ui_draft(m.ui)
		a.Text = C.GoString(text)
		C.g_free(C.gpointer(text))
	}
	m.sending = a
	m.performSend(a, names, true)
}
func (m *model) retrySend() {
	a := m.sending
	if a == nil || m.active != nil {
		return
	}
	if !a.matches(m.sendProfile()) {
		m.message("Server or approvals changed; edit the draft and choose recipients again")
		return
	}
	names := a.failed()
	if len(names) == 0 {
		return
	}
	m.performSend(a, names, false)
}

type sendResult struct {
	Deliveries []core.Delivery
	Err        error
}

func (m *model) performSend(a *sendAttempt, names []string, prepare bool) {
	started := m.run("Sending…", func(ctx context.Context) (any, error) {
		result := sendResult{}
		p, err := m.store.load(a.Profile.Target)
		if err != nil {
			result.Err = err
			return result, nil
		}
		if !a.matches(p) {
			return nil, &recipientChanged{p}
		}
		var input io.Reader = strings.NewReader(a.Text)
		if a.File {
			stop := context.AfterFunc(ctx, func() { a.Stage.Close() })
			defer stop()
			if prepare {
				source, e := openInput(a.Source)
				if e != nil {
					result.Err = e
					return result, nil
				}
				defer source.Close()
				stopSource := context.AfterFunc(ctx, func() { source.Close() })
				defer stopSource()
				if _, e = io.Copy(a.Stage, source); e != nil {
					result.Err = e
					return result, nil
				}
			}
			if _, e := a.Stage.Seek(0, io.SeekStart); e != nil {
				result.Err = e
				return result, nil
			}
			input = a.Stage
		}
		value, e := m.remote(ctx, a.Profile.Target, &a.Profile.Host, func(s *core.Session, p profile) (any, error) {
			if !a.matches(p) {
				return nil, &recipientChanged{p}
			}
			var filename *string
			if a.File {
				filename = &a.Filename
			}
			kind := "text"
			if a.File {
				kind = "file"
			}
			deliveries, e := s.Send(p.Config, names, kind, filename, input)
			return sendResult{deliveries, e}, e
		})
		if value != nil {
			r := value.(sendResult)
			if e != nil {
				r.Err = e
			}
			return r, nil
		}
		result.Err = e
		return result, nil
	}, func(value any) {
		if m.sending != a {
			return
		}
		result := value.(sendResult)
		var changed *recipientChanged
		if errors.As(result.Err, &changed) {
			m.applyProfile(changed.profile)
			m.message(result.Err.Error())
			return
		}
		if len(result.Deliveries) == 0 {
			m.message(fmt.Sprint(result.Err))
			if len(a.Deliveries) == 0 {
				m.clearSendAttempt()
			}
			m.drawSend()
			return
		}
		if !a.matches(m.sendProfile()) {
			m.clearSendAttempt()
			m.drawSend()
			m.message("Send finished for the previous host; check its inboxes before retrying")
			return
		}
		a.merge(result.Deliveries)
		m.set("send-report", a.summary())
		complete := true
		for _, d := range a.Deliveries {
			if d.Error != "" || d.Unconfirmed {
				complete = false
			}
		}
		if !complete {
			m.drawSend()
			m.message("Send incomplete; the draft is retained")
			return
		}
		C.ui_clear_draft(m.ui, boolean(a.File))
		if a.File {
			m.file = ""
			m.set("selected-file", "No file selected")
		}
		m.extraRecipients = nil
		message := "Sent " + a.Deliveries[0].ID
		if len(a.Names) > 1 {
			message = fmt.Sprintf("Sent to %d devices\n%s", len(a.Names), a.summary())
		}
		m.clearSendAttempt()
		m.drawSend()
		m.page("inbox")
		m.message(message)
	}, func(err error) {
		if m.sending != a {
			return
		}
		if errors.As(err, new(*recipientChanged)) {
			m.clearSendAttempt()
			m.drawSend()
			return
		}
		// The bounded worker may still be returning its final acknowledgement.
		var uncertain []core.Delivery
		for _, name := range names {
			uncertain = append(uncertain, core.Delivery{Recipient: name, Error: err.Error(), Unconfirmed: true})
		}
		a.merge(uncertain)
		m.set("send-report", a.summary())
		m.drawSend()
	})
	if !started {
		if prepare {
			m.clearSendAttempt()
		}
		m.drawSend()
	}
}
