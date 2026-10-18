package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"keyparcel/core"
)

func TestUnknownArgumentsFailBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{{"send", "--unexpected"}, {"receive", "12345678"}, {"inbox", "--watch", "--json"}, {"setup", "--host", "-bad"}, {"inbox", "--inbox-mode", "ssh"}, {"setup", "--inbox-mode", "ssh"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			args = append(args, "--config", filepath.Join(t.TempDir(), "config"))
			var out, diag bytes.Buffer
			if err := Run(context.Background(), args, strings.NewReader(""), &out, &diag); err == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}
func TestCorruptConfigPreserved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte("corrupted"), 0600)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"untrust", "alice", "--config", p}, strings.NewReader(""), &out, &out); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "corrupted" {
		t.Fatal("configuration was overwritten")
	}
}

func TestLocalInputAndOutputErrors(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ssh-called")
	stub := `#!/bin/sh
touch "$SSH_MARKER"
exit 99
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("SSH_MARKER", marker)
	config := filepath.Join(dir, "config")
	cfg := &core.Config{Hosts: map[string]core.Host{"offline": {Identity: "missing-key", Fingerprint: "fp", Root: "root", Discovery: "keys"}}, Peers: map[string]string{}, DefaultHost: "offline"}
	if err := cfg.Save(config); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"receive", "12345678", "--save-to", ""}, "nonempty path"},
		{[]string{"receive", "12345678", "--stdout", "--save-to", ""}, "nonempty path"},
		{[]string{"receive", "12345678", "--copy", "--save-to", ""}, "nonempty path"},
		{[]string{"receive", "12345678", "--stdout", "--save-to", dir}, "exactly one"},
		{[]string{"receive", "12345678", "--copy", "--stdout"}, "exactly one"},
		{[]string{"receive", "12345678", "--stdout", "--overwrite"}, "--overwrite requires --save-to"},
		{[]string{"delete", "--all", "12345678"}, "delete requires"},
		{[]string{"delete"}, "delete requires"},
		{[]string{"send", "--to", "self", "--file", filepath.Join(dir, "absent")}, "input file:"},
		{[]string{"send", "--to", "self", "--file", dir}, "not a regular file"},
	}
	if os.Geteuid() != 0 {
		unreadable := filepath.Join(dir, "unreadable")
		if err := os.WriteFile(unreadable, []byte("secret"), 0000); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct {
			args []string
			want string
		}{[]string{"send", "--to", "self", "--file", unreadable}, "permission denied"})
	}
	for _, tc := range cases {
		var out, diag bytes.Buffer
		args := append(tc.args, "--config", config)
		err := Run(context.Background(), args, strings.NewReader(""), &out, &diag)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: %v", tc.args, err)
		}
		if out.Len() != 0 {
			t.Fatalf("invalid command produced content: %q", out.String())
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("invalid command launched SSH")
	}
}

func TestClipboardContentAndLimit(t *testing.T) {
	dir := t.TempDir()
	copied := filepath.Join(dir, "copied")
	for _, tool := range []string{"wl-copy", "osascript"} {
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(`#!/bin/sh
cat > "$COPIED_PATH"
`), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("WAYLAND_DISPLAY", "test")
	t.Setenv("COPIED_PATH", copied)
	for _, tc := range []struct {
		name    string
		content []byte
		want    string
	}{
		{"unicode", []byte("  世界 \ufffd\n\n"), ""},
		{"empty", nil, ""},
		{"RTF-looking text", []byte(`{\rtf1\ansi hello}`), ""},
		{"EPS-looking text", []byte("%!PS-Adobe-3.0 EPSF-3.0\nhello\n"), ""},
		{"limit", bytes.Repeat([]byte("a"), maxClipboardBytes), ""},
		{"oversized", bytes.Repeat([]byte("a"), maxClipboardBytes+1), "16 MiB"},
		{"invalid UTF-8", []byte{0xff}, "valid UTF-8"},
		{"NUL", []byte("hello\x00world"), "without NUL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(copied, []byte("previous"), 0600); err != nil {
				t.Fatal(err)
			}
			input, err := os.CreateTemp(dir, "input")
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			if _, err = input.Write(tc.content); err != nil {
				t.Fatal(err)
			}
			if _, err = input.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			err = clipboard(context.Background(), input)
			got, readErr := os.ReadFile(copied)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, tc.content) {
					t.Fatal("clipboard bytes changed")
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "--save-to or --stdout") {
					t.Fatalf("unexpected rejection: %v", err)
				}
				if string(got) != "previous" {
					t.Fatal("rejection changed clipboard")
				}
			}
		})
	}
}

// Edge case: a FIFO without a writer must be rejected rather than opened.
func TestSendRejectsNamedPipe(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- Run(context.Background(), []string{"send", "--to", "self", "--file", fifo, "--config", filepath.Join(dir, "config")}, strings.NewReader(""), io.Discard, io.Discard)
	}()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("expected local rejection, got %v", err)
		}
	case <-time.After(time.Second):
		// Release a blocked reader so a regression cannot strand the test goroutine.
		writer, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		select {
		case <-result:
		case <-time.After(time.Second):
		}
		t.Fatal("send blocked opening a named pipe")
	}
}

func TestVersionWithoutConfiguration(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, arg := range []string{"version", "--version"} {
		var out bytes.Buffer
		if err := Run(context.Background(), []string{arg}, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); !strings.HasPrefix(got, "keyparcel development (") {
			t.Fatalf("version should be available without configuration: %q", got)
		}
	}
}

func TestInboxModeOverrideDoesNotRewriteProfile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	cfg := &core.Config{Hosts: map[string]core.Host{"server": {Identity: "unused", Fingerprint: "unused", Root: "root", Discovery: "keys", InboxMode: "sftp"}}, Peers: map[string]string{}, DefaultHost: "server"}
	if err := cfg.Save(p); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "auto", "sftp"} {
		want := mode
		if want == "" {
			want = "sftp"
		}
		called := false
		connect := func(_ context.Context, _ string, h core.Host, _ string, _ bool) (*core.Session, error) {
			called = true
			if h.InboxMode != want {
				t.Fatalf("listing policy = %q, want %q", h.InboxMode, want)
			}
			return nil, io.EOF
		}
		args := []string{"inbox", "--config", p}
		if mode != "" {
			args = append(args, "--inbox-mode", mode)
		}
		if err := RunWithConnector(context.Background(), args, strings.NewReader(""), io.Discard, io.Discard, connect); err != io.EOF || !called {
			t.Fatal("policy did not reach connector:", err)
		}
	}
	after, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("one-shot inbox override rewrote saved settings:", err)
	}
}
