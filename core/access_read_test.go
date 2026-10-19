package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"
	"testing"
	"time"

	"keyparcel/internal/sftptest"
)

func TestCommandReadPolicyAndSharedProbe(t *testing.T) {
	for _, mode := range []string{"auto", "ssh", "sftp"} {
		t.Run(mode, func(t *testing.T) {
			server := sftptest.New(t)
			id := testIdentity(t)
			s := &Session{ctx: context.Background(), Client: server.Client, Identity: id, Root: "/root", DiscoveryPath: "/keys", closeTransport: func() error { return nil }}
			f, err := s.Client.Create("/keys")
			if err != nil {
				t.Fatal(err)
			}
			f.Write([]byte(PublicText(id.Public) + " self\n"))
			f.Close()
			m, wire := receivePackage(t, id, id, []byte("retained"))
			putInboxPackage(t, s, m.ID, wire)
			calls := 0
			s.access = &remoteAccess{mode: mode, run: func(context.Context, string, func(io.Reader) error) error {
				calls++
				return ErrSSHCommandsUnsupported
			}}
			_, de := s.Discover(&Config{})
			_, _, _, le := s.List(&Config{})
			_, re := s.Receive(&Config{}, m.ID, func(_ Manifest, f *os.File) error {
				b, e := io.ReadAll(f)
				if e == nil && !bytes.Equal(b, []byte("retained")) {
					return errors.New("fallback bytes differ")
				}
				return e
			}, true)
			for _, e := range []error{de, le, re} {
				if mode == "ssh" {
					if e == nil || !strings.Contains(e.Error(), "--access-method sftp") {
						t.Fatal("required command silently fell back:", e)
					}
				} else if e != nil {
					t.Fatal("ordinary fallback failed:", e)
				}
			}
			want := 1
			if mode == "sftp" {
				want = 0
			}
			if calls != want {
				t.Fatalf("probe calls %d want%d", calls, want)
			}
			if s.access.mode != mode {
				t.Fatal("capability result rewrote configured method")
			}
		})
	}
}

func TestCommandReceiveWaitsForCompleteSuccessfulExit(t *testing.T) {
	for _, failure := range []string{"exit", "extra bytes", "short stream", "negative length", "truncated header"} {
		t.Run(failure, func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)
			server := sftptest.New(t)
			id := testIdentity(t)
			s := &Session{ctx: context.Background(), Client: server.Client, Identity: id, Root: "/root", closeTransport: func() error { return nil }}
			m, wire := receivePackage(t, id, id, []byte("verified but not yet deliverable"))
			putInboxPackage(t, s, m.ID, wire)
			s.access = &remoteAccess{mode: "ssh", supported: true, run: func(_ context.Context, _ string, consume func(io.Reader) error) error {
				header := fmt.Sprintf("KP-PARCEL-1 %d\n", len(wire))
				body := wire
				switch failure {
				case "extra bytes":
					body = append(append([]byte{}, wire...), 0)
				case "short stream":
					header = fmt.Sprintf("KP-PARCEL-1 %d\n", len(wire)+1)
				case "negative length":
					header = "KP-PARCEL-1 -1\n"
				case "truncated header":
					header = "KP-PARCEL-1"
				}
				err := consume(io.MultiReader(strings.NewReader(header), bytes.NewReader(body)))
				if err == nil && failure == "exit" {
					err = errors.New("remote exit failure")
				}
				return err
			}}
			s.access.once.Do(func() {})
			_, err := s.Receive(&Config{}, m.ID, func(Manifest, *os.File) error { t.Fatal("command delivered before stream+exit validation"); return nil }, false)
			if err == nil {
				t.Fatal("invalid command accepted")
			}
			if _, err = s.Client.Lstat(path.Join(s.InboxDir(), m.ID+".age")); err != nil {
				t.Fatal("failed command removed parcel:", err)
			}
			entries, err := os.ReadDir(temp)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed command leaked staging:", entries, err)
			}
		})
	}
}

func TestCommandParcelScriptRejectsUnsafeFilesBeforeReading(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "FIFO", "missing", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "unreadable" && os.Geteuid() == 0 {
				t.Skip("root bypasses file permissions")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			server := sftptest.New(t)
			id := testIdentity(t)
			s := &Session{ctx: ctx, Client: server.Client, Identity: id, Root: t.TempDir(), closeTransport: func() error { return nil }}
			m, wire := receivePackage(t, id, id, []byte("retained"))
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(os.MkdirAll(s.InboxDir(), 0700))
			if kind != "missing" {
				putInboxPackage(t, s, m.ID, wire)
			}
			name := path.Join(s.InboxDir(), m.ID+".age")
			switch kind {
			case "symlink":
				must(os.WriteFile(name+".original", wire, 0600))
				must(os.Symlink(name+".original", name))
			case "directory":
				must(os.Mkdir(name, 0700))
			case "FIFO":
				if err := syscall.Mkfifo(name, 0600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				must(os.WriteFile(name, wire, 0000))
			}
			s.access = &remoteAccess{mode: "ssh", run: localRemoteCommand(nil)}
			_, err := s.Receive(&Config{}, m.ID, func(Manifest, *os.File) error { t.Fatal("unsafe file reached delivery"); return nil }, true)
			if err == nil {
				t.Fatal("unsafe file accepted")
			}
			if kind == "symlink" || kind == "directory" || kind == "FIFO" {
				if !strings.Contains(err.Error(), "not a regular file") {
					t.Fatal(err)
				}
			}
			if kind == "missing" && !errors.Is(err, os.ErrNotExist) {
				t.Fatal("missing-file classification lost:", err)
			}
			if kind == "unreadable" && !errors.Is(err, errRemoteReadPreflight) {
				t.Fatal("unreadable-file preflight lost:", err)
			}
			if ctx.Err() != nil {
				t.Fatal("unsafe file blocked until timeout:", kind)
			}
		})
	}
}

func TestInboxLimitLeavesOtherCommandReadsAvailable(t *testing.T) {
	for _, mode := range []string{"auto", "ssh"} {
		t.Run(mode, func(t *testing.T) {
			server := sftptest.New(t)
			id := testIdentity(t)
			s := &Session{ctx: context.Background(), Client: server.Client, Identity: id, Root: "/root", DiscoveryPath: "/keys", closeTransport: func() error { return nil }}
			m, wire := receivePackage(t, id, id, []byte("still available"))
			putInboxPackage(t, s, m.ID, wire)
			calls := 0
			s.access = &remoteAccess{mode: mode, supported: true, run: func(_ context.Context, command string, consume func(io.Reader) error) error {
				calls++
				if command == remoteShell(accessRead, s.DiscoveryPath, "discovery") {
					return consume(strings.NewReader("KP-DISCOVERY-1\n" + PublicText(id.Public) + "\n"))
				}
				return consume(io.MultiReader(strings.NewReader(fmt.Sprintf("KP-PARCEL-1 %d\n", len(wire))), bytes.NewReader(wire)))
			}}
			s.access.once.Do(func() {})
			s.access.inboxLimited.Store(true)
			items, _, _, err := s.List(&Config{})
			if mode == "ssh" {
				if !errors.Is(err, errInboxBudget) {
					t.Fatal("explicit SSH hid aggregate fallback", err)
				}
			} else if err != nil || len(items) != 1 {
				t.Fatal("Auto failed ordinary large inbox listing", items, err)
			}
			if calls != 0 {
				t.Fatal("limited inbox repeated stream")
			}
			if _, err = s.Discover(&Config{}); err != nil {
				t.Fatal("inbox budget disabled command discovery", err)
			}
			if _, err = s.Receive(&Config{}, m.ID, func(Manifest, *os.File) error { return nil }, true); err != nil {
				t.Fatal("inbox budget disabled command receive", err)
			}
			if calls != 2 {
				t.Fatal("other reads silently used SFTP", calls)
			}
		})
	}
}
