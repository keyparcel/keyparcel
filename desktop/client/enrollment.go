package main

/*
#include "ui.h"
*/
import "C"

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/cgo"
	"time"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
)

type enrollmentDetails struct{ Public, Fingerprint string }

func defaultIdentity() string {
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err = os.Stat(filepath.Join(home, ".ssh/id_ed25519")); err == nil {
			return "~/.ssh/id_ed25519"
		}
	}
	return ""
}

func readEnrollment(path string) (enrollmentDetails, error) {
	if path == "" {
		return enrollmentDetails{}, errors.New("Choose an identity file to show enrollment details")
	}
	resolved, err := core.ResolvePath(path)
	if err != nil {
		return enrollmentDetails{}, err
	}
	f, err := openInput(resolved)
	if err != nil {
		return enrollmentDetails{}, err
	}
	defer f.Close()
	// A mistaken large file or a special file must not stall the settings reader.
	const limit = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return enrollmentDetails{}, err
	}
	if len(data) > limit {
		return enrollmentDetails{}, errors.New("identity file exceeds 1 MiB")
	}
	id, err := core.ParseIdentity(data)
	if err != nil {
		return enrollmentDetails{}, err
	}
	details := enrollmentDetails{core.PublicText(id.Public), ssh.FingerprintSHA256(id.Public)}
	if sibling, err := openInput(resolved + ".pub"); err == nil {
		defer sibling.Close()
		text, err := io.ReadAll(io.LimitReader(sibling, limit+1))
		if err == nil && len(text) <= limit {
			public, comment, options, rest, err := ssh.ParseAuthorizedKey(text)
			if err == nil && len(options) == 0 && len(bytes.TrimSpace(rest)) == 0 && bytes.Equal(public.Marshal(), id.Public.Marshal()) && comment != "" {
				details.Public += " " + comment
			}
		}
	}
	return details, nil
}

func (m *model) clearEnrollment() {
	m.enrollmentVersion++
	m.enrollmentOpen = false
	m.enrollment = enrollmentDetails{}
	C.ui_enrollment_reset(m.ui)
}
func (m *model) showEnrollment(details enrollmentDetails, err error) {
	m.enrollment = details
	message := ""
	if err != nil {
		message = err.Error()
	}
	cstrings([]string{details.Public, details.Fingerprint, visible(message)}, func(v []*C.char) { C.ui_enrollment(m.ui, v[0], v[1], v[2]) })
}
func (m *model) openEnrollment() {
	m.enrollmentVersion++
	version, path := m.enrollmentVersion, m.get("identity")
	m.enrollmentOpen = true
	m.showEnrollment(enrollmentDetails{}, errors.New("Reading identity…"))
	select {
	case m.workers <- struct{}{}:
	default:
		m.showEnrollment(enrollmentDetails{}, errors.New("Identity reader is still finishing; try opening details again"))
		return
	}
	started := time.Now()
	m.options.diagnostics.start("Read enrollment")
	C.ui_ref(m.ui)
	go func() {
		details, err := readEnrollment(path)
		<-m.workers
		callback := cgo.NewHandle(func() {
			m.options.diagnostics.finish("Read enrollment", started, nil, err, m.closed || !m.enrollmentOpen || version != m.enrollmentVersion || path != m.get("identity"))
			if m.closed || !m.enrollmentOpen || version != m.enrollmentVersion || path != m.get("identity") {
				return
			}
			m.showEnrollment(details, err)
		})
		C.ui_post(m.ui, C.uintptr_t(callback))
	}()
}
func (m *model) settingsFields(p profile) {
	identity, root, discovery := p.Host.Identity, p.Host.Root, p.Host.Discovery
	if p.Target == "" {
		identity = defaultIdentity()
		root = ".local/share/keyparcel"
		discovery = ".ssh/authorized_keys"
	}
	if m.get("identity") != identity {
		m.clearEnrollment()
	}
	C.ui_settings_edit(m.ui, 1)
	defer C.ui_settings_edit(m.ui, 0)
	m.set("target", p.Target)
	m.set("identity", identity)
	m.set("root", root)
	m.set("discovery", discovery)
	mode := p.Host.InboxMode
	if mode == "" {
		mode = "auto"
	}
	m.set("inbox-mode", mode)
	m.settingsLoaded = true
}

// Saved profile errors must not hide the saved path that needs repairing.
type profileLoad struct {
	Profile profile
	Err     error
}

func (m *model) saveAndConnect() {
	target, identity, root, discovery := m.get("target"), m.get("identity"), m.get("root"), m.get("discovery")
	inboxMode := m.get("inbox-mode")
	m.run("Saving shared settings…", func(_ context.Context) (any, error) {
		if err := m.store.setup(target, identity, root, discovery, inboxMode); err != nil {
			return nil, err
		}
		return m.store.load(target)
	}, func(value any) {
		p := value.(profile)
		m.settingsLoaded = false
		m.applyProfile(p)
		m.run("Connecting through OpenSSH…", func(ctx context.Context) (any, error) {
			return m.remote(ctx, p.Target, &p.Host, func(_ *core.Session, p profile) (any, error) { return p, nil })
		}, func(value any) { m.applyProfile(value.(profile)); m.page("inbox") })
	})
}

// These native probes let the isolated widget test use actual pointer coordinates.
type informationState struct {
	x, y, width, height                     int
	mapped, focused, open, hovered, pressed bool
}

func (m *model) present() { C.ui_present(m.ui) }
func flushUI()            { C.ui_flush() }
func (m *model) informationState(name string) (s informationState) {
	cstrings([]string{name}, func(v []*C.char) {
		n := C.ui_info_state(m.ui, v[0])
		s = informationState{int(n.x), int(n.y), int(n.width), int(n.height), n.mapped != 0, n.focused != 0, n.open != 0, n.hovered != 0, n.pressed != 0}
	})
	return
}
