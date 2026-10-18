package main

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
)

// Reuse the one native model/application in the existing lifecycle scenario.
func checkGTKProfilePreview(t *testing.T, m *model) {
	t.Helper()
	oldPath, oldOptions := m.store.path, m.options
	defer func() {
		m.settingsLoaded = false
		m.applyProfile(profile{Config: &core.Config{}})
		m.file, m.recipient = "", ""
		m.store.path, m.options = oldPath, oldOptions
	}()
	dir := t.TempDir()
	m.store.path = filepath.Join(dir, "config.json")
	makeHost := func() core.Host {
		path := identity(t, t.TempDir())
		key, err := core.LoadIdentity(path)
		if err != nil {
			t.Fatal(err)
		}
		return core.Host{Identity: path, Fingerprint: ssh.FingerprintSHA256(key.Public), Root: "storage", Discovery: "keys"}
	}
	first, second := makeHost(), makeHost()
	second.Root, second.Discovery, second.AccessMethod = "other-storage", "other-keys", "sftp"
	cfg := &core.Config{DefaultHost: "first", Hosts: map[string]core.Host{"first": first, "second": second, "same-key": second}, Peers: map[string]string{}}
	if err := cfg.Save(m.store.path); err != nil {
		t.Fatal(err)
	}
	m.settingsLoaded = false
	active := profile{Target: "first", Host: first, Config: cfg}
	m.applyProfile(active)
	if m.get("access-method") != "auto" {
		t.Fatal("default inbox policy was not automatic")
	}
	var dials atomic.Int32
	connection := core.NewConnection(m.ctx, func(context.Context) (*core.Session, error) { dials.Add(1); return nil, errors.New("unexpected dial") })
	m.session.connection, m.session.target, m.session.host = connection, active.Target, first
	m.session.current.Store(&ownedConnection{connection, active.Target, first})
	m.retryAt = time.Now().Add(time.Hour)
	m.items = []core.Item{{ID: "retained"}}
	m.devices = []core.Device{{Name: "retained"}}
	m.rejected = []core.Rejected{{ID: "rejected"}}
	m.file, m.recipient = "retained-file", "self"
	items, devices, rejected := m.items, m.devices, m.rejected
	peers, version, generation := m.peers, m.profileVersion, m.session.stamp()
	before, err := os.ReadFile(m.store.path)
	if err != nil {
		t.Fatal(err)
	}
	m.enrollmentOpen = true
	m.enrollment = enrollmentDetails{Public: "old", Fingerprint: "old"}
	m.event("host-selected", "second", "")
	if m.get("target") != "second" || m.get("identity") != second.Identity || m.get("root") != second.Root || m.get("discovery") != second.Discovery || m.get("access-method") != "sftp" {
		t.Fatal("saved selection did not populate every form field")
	}
	if !m.accessHintVisible() {
		t.Fatal("previewed method differs from active profile without apply hint")
	}
	m.set("access-method", "auto")
	if m.accessHintVisible() {
		t.Fatal("reverted method retained apply hint")
	}
	m.set("access-method", "sftp")
	if m.enrollmentOpen || m.enrollment != (enrollmentDetails{}) {
		t.Fatal("profile preview retained enrollment details")
	}
	after, err := os.ReadFile(m.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if m.target != active.Target || m.host != first || m.session.connection != connection || connection.State() == "Suspended" || m.session.stamp() != generation || m.profileVersion != version || m.active != nil || dials.Load() != 0 {
		t.Fatal("profile preview applied or reconnected the active profile")
	}
	if !bytes.Equal(before, after) || !reflect.DeepEqual(m.items, items) || !reflect.DeepEqual(m.devices, devices) || !reflect.DeepEqual(m.rejected, rejected) || !reflect.DeepEqual(m.peers, peers) || m.file != "retained-file" || m.recipient != "self" {
		t.Fatal("profile preview changed data, draft, trust or shared configuration")
	}
	baseline := m.pendingCallbacks()
	m.event("enrollment-toggle", "true", "")
	m.event("host-selected", "same-key", "") // Different profile, identical identity path.
	deadline := time.Now().Add(3 * time.Second)
	for m.pendingCallbacks() != baseline && time.Now().Before(deadline) {
		iterate()
		runtime.Gosched()
	}
	if m.pendingCallbacks() != baseline || m.enrollmentOpen || m.enrollment != (enrollmentDetails{}) {
		t.Fatal("late same-key enrollment preview survived profile selection")
	}
	// External CLI edits update the chooser map without replacing an unsaved form.
	updated := *cfg
	updated.Hosts = maps.Clone(cfg.Hosts)
	cfg = &updated
	active.Config = cfg
	cfg.Hosts["new-profile"] = first
	latest := second
	latest.Root = "latest-storage"
	cfg.Hosts["second"] = latest
	m.set("root", "unsaved-storage")
	m.set("access-method", "auto")
	if !m.publishSnapshot(liveSnapshot{Profile: active}, m.profileVersion) {
		t.Fatal("current profile-map update was rejected")
	}
	if m.hosts["new-profile"] != first || m.get("target") != "same-key" || m.get("root") != "unsaved-storage" || m.get("access-method") != "auto" {
		t.Fatal("profile-map update overwrote unsaved fields")
	}
	m.event("host-selected", "second", "")
	if m.get("root") != latest.Root || m.get("access-method") != "sftp" {
		t.Fatal("selection used an obsolete saved-profile map")
	}
	// Missing saved identities must still populate the form without validation/dial.
	missing := latest
	missing.Identity = filepath.Join(dir, "missing")
	cfg.Hosts["missing"] = missing
	m.event("host-selected", "missing", "")
	if m.get("identity") != missing.Identity || m.target != active.Target || m.active != nil {
		t.Fatal("invalid saved identity was hidden or applied")
	}
	m.event("host-selected", "second", "")
	if err := cfg.Save(m.store.path); err != nil {
		t.Fatal(err)
	}
	// A missing ssh config deterministically rejects setup before any network access.
	m.options.sshConfig = filepath.Join(dir, "missing-ssh-config")
	m.options.timeout = time.Second
	m.saveAndConnect()
	deadline = time.Now().Add(3 * time.Second)
	for (m.active != nil || m.target != "second") && time.Now().Before(deadline) {
		iterate()
		runtime.Gosched()
	}
	saved, err := core.LoadConfig(m.store.path)
	if err != nil || m.active != nil || m.target != "second" || m.host != latest || saved.DefaultHost != "second" || saved.Hosts["second"] != latest || m.get("target") != "second" {
		t.Fatal("Save & connect did not save/apply the previewed form:", err)
	}
	if m.accessHintVisible() {
		t.Fatal("applied method retained pending hint")
	}
	if connection.State() != "Suspended" {
		t.Fatal("explicit Save & connect did not retire the previous connection")
	}
	// Active-profile updates must not overwrite a separately edited form either.
	m.set("root", "new-unsaved-root")
	changed := latest
	changed.Root = "externally-updated-root"
	m.applyProfile(profile{Target: "second", Host: changed, Config: cfg})
	if m.get("root") != "new-unsaved-root" {
		t.Fatal("active-profile update overwrote unsaved form fields")
	}
}
