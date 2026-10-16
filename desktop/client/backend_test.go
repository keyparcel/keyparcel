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
	cases := []struct {
		name, text                string
		truncated, nul, copyError bool
	}{
		{"unicode", "hello 世界\n\t", false, false, false},
		{"empty", "", false, false, false},
		{"truncated", strings.Repeat("🌍", previewLimit+1), true, false, false},
		{"nul", "a\x00b", false, true, true},
		{"limit", strings.Repeat("x", clipboardLimit), true, false, false},
		{"oversize", strings.Repeat("x", clipboardLimit+1), true, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "text")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			f.WriteString(c.text)
			f.Seek(0, 0)
			v, err := readText(f, core.Manifest{ID: "id", Size: int64(len(c.text))}, profile{})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(v.Notice, "truncated") != c.truncated || v.PreviewNUL != c.nul || (v.CopyError != "") != c.copyError {
				t.Fatalf("unexpected preview policy: %+v", v)
			}
			if !c.copyError && v.Clipboard != c.text {
				t.Fatal("whole-message copy substituted preview")
			}
			if len([]rune(v.Preview)) > previewLimit {
				t.Fatal("preview exceeded rune bound")
			}
		})
	}
	if err := clipboardText(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
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
	release := make(chan struct{})
	m.run("Stalled", func(context.Context) (any, error) { <-release; return nil, nil }, func(any) { t.Error("timed-out callback applied") })
	deadline := time.Now().Add(time.Second)
	for m.active != nil && time.Now().Before(deadline) {
		iterate()
		time.Sleep(time.Millisecond)
	}
	if m.active != nil {
		t.Fatal("timeout did not restore controls")
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
