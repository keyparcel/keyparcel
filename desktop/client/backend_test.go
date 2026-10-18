package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"keyparcel/core"
)

func identity(t *testing.T, dir string) string {
	t.Helper()
	b, err := core.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "identity")
	if err = os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestSharedSettingsAndApprovals(t *testing.T) {
	dir := t.TempDir()
	s := store{path: filepath.Join(dir, "config.json")}
	key := identity(t, dir)
	if err := s.setup("inbox", key, "storage", "keys"); err != nil {
		t.Fatal(err)
	}
	p, err := s.load("")
	if err != nil {
		t.Fatal(err)
	}
	// A CLI-style edit happens after the GUI loaded its previous snapshot.
	p.Config.Hosts["other"] = p.Host
	if err = p.Config.Save(s.path); err != nil {
		t.Fatal(err)
	}
	peerDir := t.TempDir()
	peer, err := core.LoadIdentity(identity(t, peerDir))
	if err != nil {
		t.Fatal(err)
	}
	pub := core.PublicText(peer.Public)
	if err = s.approve("bob", pub); err != nil {
		t.Fatal(err)
	}
	p, err = s.load("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Config.Hosts["other"]; !ok {
		t.Fatal("GUI edit lost externally added host")
	}
	if err = s.approve("another", pub); err == nil {
		t.Fatal("duplicate key approval succeeded")
	}
	if err = s.forget("bob", p.Public); err == nil {
		t.Fatal("stale approval could be forgotten")
	}
	if err = s.forget("bob", pub); err != nil {
		t.Fatal(err)
	}
}
func TestPreviewAndClipboard(t *testing.T) {
	for _, text := range []string{"hello 世界\n\t", "", strings.Repeat("🌍", previewLimit+1), "a\x00b", string([]byte{255}), strings.Repeat("x", previewLimit) + "\xff", strings.Repeat("x", previewLimit) + "\x00", strings.Repeat("x", viewLimit)} {
		f, err := os.CreateTemp(t.TempDir(), "parcel")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		f.WriteString(text)
		f.Seek(0, 0)
		v, err := readText(f, core.Manifest{ID: "id", Type: "file", Size: int64(len(text))}, profile{})
		if err != nil {
			t.Fatal(err)
		}
		if string(v.Data) != text {
			t.Fatal("viewer did not retain original bytes")
		}
		eligible := clipboardText(text) == nil
		if v.Text != eligible {
			t.Fatalf("eligibility mismatch for length %d", len(text))
		}
		if !eligible && (v.Preview != "" || v.Notice != unsupportedPreview) {
			t.Fatal("binary content exposed as text")
		}
		if len([]rune(v.Preview)) > previewLimit {
			t.Fatal("preview exceeded rune bound")
		}
		if eligible && strings.Contains(v.Notice, "truncated") != (len([]rune(text)) > previewLimit) {
			t.Fatal("missing truncation notice")
		}
		dest := filepath.Join(t.TempDir(), "saved.bin")
		if _, err = v.save(context.Background(), dest, false); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(dest)
		if err != nil || string(data) != text {
			t.Fatal("save altered retained bytes", err)
		}
	}
	f, err := os.CreateTemp(t.TempDir(), "oversize")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Truncate(viewLimit + 1)
	for _, size := range []int64{1, viewLimit + 1} {
		f.Seek(0, 0)
		if _, err = readText(f, core.Manifest{Size: size}, profile{}); err == nil {
			t.Fatal("oversized actual or signed size accepted")
		}
	}
	if clipboardText(strings.Repeat("x", clipboardLimit+1)) == nil {
		t.Fatal("clipboard cap changed")
	}
	if clipboardText(string([]byte{255})) == nil {
		t.Fatal("invalid UTF8 clipboard accepted")
	}
}
func TestFileValidationEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openInput(path); err == nil {
		t.Fatal("named pipe accepted")
	}
	if _, err := openInput(dir); err == nil {
		t.Fatal("directory accepted")
	}
	path = filepath.Join(dir, "empty")
	os.WriteFile(path, nil, 0600)
	f, err := openInput(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}
func TestGTKLifecycleAndImmediateCancellation(t *testing.T) {
	if os.Getenv("KEYPARCEL_DESKTOP_TEST_SESSION") == "" {
		t.Skip("use test.sh for isolated GTK lifecycle checks")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	m := newModel(options{config: filepath.Join(t.TempDir(), "config"), timeout: 30 * time.Millisecond})
	if m.ui == nil {
		t.Fatal("GTK initialization failed")
	}
	defer m.destroy()
	checkGTKInformationPointerAndKeyboard(t, m)
	testGTKStaleManualRefreshKeepsForegroundProgressAndCancel(t, m)
	// Local enrollment preview must not apply a profile or write configuration.
	path := identity(t, t.TempDir())
	m.set("identity", path)
	version, baselinePending := m.profileVersion, m.pendingCallbacks()
	await := func(description string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !condition() && time.Now().Before(deadline) {
			iterate()
			time.Sleep(time.Millisecond)
		}
		if !condition() {
			t.Fatal(description)
		}
	}
	m.event("enrollment-toggle", "true", "")
	await("valid enrollment did not load", func() bool { return m.enrollment.Public != "" })
	if m.profileVersion != version || m.active != nil || m.target != "" {
		t.Fatal("preview applied a profile or started connection work")
	}
	if _, err := os.Stat(m.store.path); !os.IsNotExist(err) {
		t.Fatal("preview wrote shared settings", err)
	}
	m.set("identity", path+".missing") // Native entry changed signal invalidates immediately.
	if m.enrollmentOpen || m.enrollment != (enrollmentDetails{}) || m.get("public-key") != "" {
		t.Fatal("editing identity retained enrollment")
	}
	m.event("enrollment-toggle", "true", "")
	await("invalid identity has no local error", func() bool { return m.get("enrollment-error") != "Reading identity…" })
	if !m.enrollmentOpen || m.get("enrollment-error") == "" || m.enrollment != (enrollmentDetails{}) {
		t.Fatal("invalid identity did not remain open with an error")
	}
	m.set("identity", path)
	if m.get("enrollment-error") != "" || m.enrollmentOpen {
		t.Fatal("path edit retained disclosure/error")
	}
	m.event("enrollment-toggle", "true", "")
	// Wait for the worker without processing the pending GTK result, then edit.
	readerDeadline := time.Now().Add(3 * time.Second)
	for len(m.workers) != 0 && time.Now().Before(readerDeadline) {
		time.Sleep(time.Millisecond)
	}
	m.set("identity", path+".edited")
	await("stale enrollment result remained pending", func() bool { return m.pendingCallbacks() == baselinePending })
	if m.enrollmentOpen || m.enrollment != (enrollmentDetails{}) {
		t.Fatal("stale preview survived path edit")
	}
	m.set("identity", path)
	m.event("enrollment-toggle", "true", "")
	m.event("enrollment-toggle", "false", "")
	await("closed disclosure reader did not finish", func() bool { return m.pendingCallbacks() == baselinePending })
	if m.enrollmentOpen || m.enrollment != (enrollmentDetails{}) {
		t.Fatal("closed disclosure accepted pending result")
	}
	m.event("choose-identity", "", "")
	if !m.pickerPending() {
		t.Fatal("identity picker did not open")
	}
	m.cancelPicker()
	if m.get("identity") != path {
		t.Fatal("canceling identity picker changed the path")
	}
	m.enrollmentOpen = true
	m.enrollment = enrollmentDetails{Public: "old", Fingerprint: "old"}
	cfg := &core.Config{Hosts: map[string]core.Host{}, Peers: map[string]string{}}
	host := core.Host{Identity: path}
	cfg.Hosts = map[string]core.Host{"first": host, "second": host}
	m.applyProfile(profile{Config: cfg, Target: "first", Host: host})
	discoveryProfile := profile{Config: cfg, Target: "first", Host: host}
	discoveryVersion := m.profileVersion
	for _, fingerprint := range []string{"initial discovery", "updated discovery"} {
		m.publishSnapshot(liveSnapshot{Profile: discoveryProfile, Devices: []core.Device{{Fingerprint: fingerprint}}, DevicesOK: true}, discoveryVersion)
		if len(m.devices) != 1 || m.devices[0].Fingerprint != fingerprint {
			t.Fatal("successful refresh did not update discovery snapshot")
		}
	}
	m.publishSnapshot(liveSnapshot{Profile: discoveryProfile}, discoveryVersion)
	if len(m.devices) != 1 || m.devices[0].Fingerprint != "updated discovery" {
		t.Fatal("failed refresh discarded discovery snapshot")
	}
	var diagnosticsLog bytes.Buffer
	m.options.diagnostics = newDiagnostics(&diagnosticsLog, false)
	m.set("target", "second")
	if m.target != "first" || m.active != nil {
		t.Fatal("typing a saved target applied its profile")
	}
	if diagnosticsLog.Len() != 0 {
		t.Fatal("typing a target logged a profile switch:", diagnosticsLog.String())
	}
	m.enrollmentOpen = true
	m.enrollment = enrollmentDetails{Public: "old", Fingerprint: "old"}
	m.applyProfile(profile{Config: cfg, Target: "second", Host: host})
	if !strings.Contains(diagnosticsLog.String(), "msg=profile_switch") || strings.Contains(diagnosticsLog.String(), path) {
		t.Fatal("applied profile switch missing or exposed values:", diagnosticsLog.String())
	}
	if m.devices != nil || m.publishSnapshot(liveSnapshot{Profile: discoveryProfile, Devices: []core.Device{{Fingerprint: "stale discovery"}}, DevicesOK: true}, discoveryVersion) {
		t.Fatal("profile change retained or stale refresh repopulated discovery")
	}
	if m.enrollmentOpen || m.enrollment != (enrollmentDetails{}) {
		t.Fatal("different host with the same identity retained details")
	}
	m.applyProfile(profile{Config: cfg})
	broken := core.Host{Identity: path + ".missing", Fingerprint: "saved-fingerprint", Root: "root", Discovery: "keys"}
	brokenConfig := &core.Config{Hosts: map[string]core.Host{"broken": broken}, Peers: map[string]string{}, DefaultHost: "broken"}
	if err := brokenConfig.Save(m.store.path); err != nil {
		t.Fatal(err)
	}
	m.load("broken")
	await("saved invalid profile did not load", func() bool { return m.active == nil && m.target == "broken" })
	if m.get("identity") != broken.Identity || m.enrollmentOpen {
		t.Fatal("saved invalid identity path was hidden or auto-previewed")
	}
	m.applyProfile(profile{Config: cfg})
	await("invalid-profile worker did not retire", func() bool { return m.pendingCallbacks() == baselinePending })
	m.recipient = "self"
	feedback := &sendFeedback{}
	m.feedback = feedback
	m.event("send-text", "", "") // Exercise the handler despite the disabled button.
	if m.sending != nil || m.active != nil {
		t.Fatal("empty text action started a send")
	}
	if m.feedback != feedback {
		t.Fatal("empty text action cleared previous feedback")
	}
	m.feedback = nil
	m.recipient = ""
	for i := 0; i < 10; i++ {
		m.event("choose-file", "", "")
		if !m.pickerPending() {
			t.Fatal("picker did not open")
		}
		m.cancelPicker()
		if m.pickerPending() {
			t.Fatal("immediate cancellation left picker pending")
		}
	}
	m.refreshRequested = true
	m.message("Clipboard copying failed")
	if m.refreshRequested {
		t.Fatal("a pending refresh still owns the newer action error")
	}
	release := make(chan struct{})
	m.refreshRequested = true
	m.confirm("Copied")
	m.run("Stalled", func(context.Context) (any, error) { <-release; return nil, nil }, func(any) { t.Error("timed-out callback applied") })
	if m.confirmation.Text != "" {
		t.Fatal("new operation retained an old confirmation")
	}
	if m.refreshRequested {
		t.Fatal("new operation retained ownership of a preempted refresh")
	}
	deadline := time.Now().Add(time.Second)
	for m.active != nil && time.Now().Before(deadline) {
		iterate()
		time.Sleep(time.Millisecond)
	}
	if m.active != nil {
		t.Fatal("timeout did not restore controls")
	}
	if !strings.Contains(diagnosticsLog.String(), "category=timeout") || !strings.Contains(diagnosticsLog.String(), "level=WARN") {
		t.Fatal("real operation timeout was not diagnosed:", diagnosticsLog.String())
	}
	close(release)
	m.event("choose-file", "", "") // Shutdown must also cancel a newly opened picker.
}

func TestVisibleDoesNotLoseWhitespace(t *testing.T) {
	if !bytes.Equal([]byte(visible(" \n\t世界")), []byte(" \n\t世界")) {
		t.Fatal("whitespace changed")
	}
}

func TestAskpassRejectsCredentials(t *testing.T) {
	for _, prompt := range []string{"Password:", "Enter passphrase for key '/tmp/key':", "Please type 'yes' or 'no':", "WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!"} {
		if hostApprovalPrompt(prompt) {
			t.Fatalf("credential/changed-key prompt accepted: %q", prompt)
		}
	}
	if !hostApprovalPrompt("The authenticity of host 'host (127.0.0.1)' can't be established.\nED25519 key fingerprint is: SHA256:abc.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? ") {
		t.Fatal("OpenSSH host-key prompt rejected")
	}
}
