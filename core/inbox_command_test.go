package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"keyparcel/internal/sftptest"
)

func localInboxCommand(env []string) inboxCommand {
	return func(ctx context.Context, command string, consume func(io.Reader) error) error {
		child, cancel := context.WithCancel(ctx)
		defer cancel()
		cmd := exec.CommandContext(child, "/bin/sh", "-c", command)
		cmd.Env = append(os.Environ(), env...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second
		cmd.Stderr = &inboxStderr{cancel: cancel}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err = cmd.Start(); err != nil {
			return err
		}
		readErr := consume(out)
		if readErr != nil {
			cancel()
		}
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if readErr != nil {
			return readErr
		}
		return waitErr
	}
}

func TestInboxBatchToolsAndLocalVerification(t *testing.T) {
	for _, tools := range []string{"gnu", "busybox"} {
		t.Run(tools, func(t *testing.T) {
			env := []string{}
			if tools == "busybox" {
				busy, err := exec.LookPath("busybox")
				if err != nil {
					t.Skip("BusyBox unavailable")
				}
				bin := t.TempDir()
				for _, name := range []string{"sh", "stat", "head", "find"} {
					if err = os.Symlink(busy, filepath.Join(bin, name)); err != nil {
						t.Fatal(err)
					}
				}
				env = append(env, "PATH="+bin+":"+os.Getenv("PATH"))
			}
			sender, recipient := testIdentity(t), testIdentity(t)
			root := filepath.Join(t.TempDir(), "quote' newline\nroot")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s := &Session{ctx: ctx, Identity: recipient, Root: root, closeTransport: func() error { return nil }}
			var probes atomic.Int32
			run := localInboxCommand(env)
			s.inbox = &inboxAcceleration{run: func(ctx context.Context, command string, consume func(io.Reader) error) error {
				if command == inboxShell(inboxProbe) {
					probes.Add(1)
				}
				return run(ctx, command, consume)
			}}
			if err := os.MkdirAll(s.InboxDir(), 0700); err != nil {
				t.Fatal(err)
			}
			manifest, ciphertext := receivePackage(t, sender, recipient, bytes.Repeat([]byte{0, 255, 10}, 500000))
			name := filepath.Join(s.InboxDir(), manifest.ID+".age")
			if err := os.WriteFile(name, ciphertext, 0600); err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(1700000123, 0).UTC()
			if err := os.Chtimes(name, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			symlink := filepath.Join(s.InboxDir(), strings.Repeat("1", 32)+".age")
			if err := os.Symlink(name, symlink); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(s.InboxDir(), strings.Repeat("2", 32)+".age"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(filepath.Join(s.InboxDir(), strings.Repeat("3", 32)+".age"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.InboxDir(), strings.Repeat("4", 32)+".age"), []byte{0, 255, 10}, 0600); err != nil {
				t.Fatal(err)
			}
			cfg := &Config{Peers: map[string]string{"sender": PublicText(sender.Public)}}
			items, rejected, ids, err := s.ListAccelerated(cfg)
			if err != nil || len(items) != 1 || len(rejected) != 4 || len(ids) != 5 {
				t.Fatalf("bounded real tools: %d items %d rejects %d ids: %v", len(items), len(rejected), len(ids), err)
			}
			if !reflect.DeepEqual(items[0].Manifest, manifest) || items[0].From != "sender" || !items[0].UploadedAt.Equal(stamp) {
				t.Fatalf("verified metadata/timestamp changed: %+v", items[0])
			}
			// The capability cache never caches trust decisions.
			items, rejected, _, err = s.ListAccelerated(&Config{Peers: map[string]string{}})
			if err != nil || len(items) != 0 || len(rejected) != 5 {
				t.Fatalf("current approvals not checked: %+v %+v %v", items, rejected, err)
			}
			if probes.Load() != 1 {
				t.Fatalf("probe ran %d times on one owner", probes.Load())
			}
			// A valid signed package under a different filename is still rejected.
			if err = os.Rename(name, filepath.Join(s.InboxDir(), strings.Repeat("5", 32)+".age")); err != nil {
				t.Fatal(err)
			}
			items, rejected, _, err = s.ListAccelerated(cfg)
			if err != nil || len(items) != 0 || len(rejected) != 5 {
				t.Fatalf("filename ID bypass: %+v %+v %v", items, rejected, err)
			}
		})
	}
}

func TestInboxFramesFailClosed(t *testing.T) {
	id := strings.Repeat("0", 32)
	validPrefix := "KP-INBOX-1\nX " + id + " nonregular\n"
	for name, wire := range map[string]string{
		"version": "banner\nEND\n", "header bound": strings.Repeat("x", 1000) + "\n",
		"missing end": validPrefix, "duplicate": validPrefix + "X " + id + " nonregular\nEND\n",
		"path ID":       "KP-INBOX-1\nX ../unsafe nonregular\nEND\n",
		"oversize":      "KP-INBOX-1\nR " + id + " 0 1048577\n",
		"negative size": "KP-INBOX-1\nR " + id + " 0 -1\n",
		"timestamp":     "KP-INBOX-1\nR " + id + " 9999999999999999999999 0\n\nEND\n",
		"truncated":     "KP-INBOX-1\nR " + id + " 0 20\nshort",
		"trailing":      validPrefix + "END\nextra",
	} {
		t.Run(name, func(t *testing.T) {
			s := &Session{ctx: context.Background(), Identity: testIdentity(t)}
			items, rejected, ids, err := s.decodeInbox(&Config{}, strings.NewReader(wire))
			if err == nil || items != nil || rejected != nil || ids != nil {
				t.Fatalf("malformed stream exposed snapshot: %+v %+v %+v %v", items, rejected, ids, err)
			}
		})
	}
	// Exercise aggregate limits with streamed readers, without a giant input buffer.
	for _, byteLimit := range []bool{false, true} {
		t.Run(fmt.Sprint("aggregate bytes=", byteLimit), func(t *testing.T) {
			readers := []io.Reader{strings.NewReader("KP-INBOX-1\n")}
			n := inboxRecordLimit + 1
			data := ""
			if byteLimit {
				n = 65
				data = strings.Repeat("x", inboxPrefixLimit)
			}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("%032x", i)
				if byteLimit {
					readers = append(readers, strings.NewReader(fmt.Sprintf("R %s 0 %d\n", id, len(data))), strings.NewReader(data), strings.NewReader("\n"))
				} else {
					readers = append(readers, strings.NewReader("X "+id+" nonregular\n"))
				}
			}
			s := &Session{ctx: context.Background(), Identity: testIdentity(t)}
			items, rejected, ids, err := s.decodeInbox(&Config{}, io.MultiReader(readers...))
			if !errors.Is(err, errInboxBudget) || items != nil || rejected != nil || ids != nil {
				t.Fatalf("aggregate limit returned partial snapshot: %v", err)
			}
		})
	}
}

func TestInboxFallbackAndTransportFailures(t *testing.T) {
	for _, kind := range []string{"unsupported", "budget", "malformed", "lost", "control EOF", "sftp"} {
		t.Run(kind, func(t *testing.T) {
			server := sftptest.New(t)
			identity := testIdentity(t)
			s := &Session{ctx: context.Background(), Identity: identity, Client: server.Client, Root: "/inbox", closeTransport: func() error { return nil }}
			cfg := &Config{}
			m, ciphertext := receivePackage(t, identity, identity, []byte("retained"))
			putInboxPackage(t, s, m.ID, ciphertext)
			calls := 0
			a := &inboxAcceleration{}
			if kind == "sftp" {
				a.mode = "sftp"
			}
			a.run = func(ctx context.Context, command string, consume func(io.Reader) error) error {
				calls++
				if kind == "control EOF" {
					return io.EOF
				}
				if kind == "lost" {
					return io.ErrClosedPipe
				}
				if kind == "unsupported" {
					return ErrInboxAccelerationUnsupported
				}
				if command == inboxShell(inboxProbe) {
					return consume(bytes.NewReader([]byte("KP-CAP-1\n\x00\xff\n")))
				}
				if kind == "budget" {
					return errInboxBudget
				}
				return consume(strings.NewReader("KP-INBOX-1\nX " + m.ID + " nonregular\n"))
			}
			s.inbox = a
			items, rejected, ids, err := s.List(cfg)
			if kind == "malformed" || kind == "lost" || kind == "control EOF" {
				if err == nil || items != nil || rejected != nil || ids != nil {
					t.Fatalf("failure used SFTP/partial data: %+v %v", items, err)
				}
			} else {
				if err != nil || len(items) != 1 || len(ids) != 1 || len(rejected) != 0 {
					t.Fatalf("SFTP fallback lost inbox: %+v %v", items, err)
				}
				before := calls
				if _, _, _, err = s.List(cfg); err != nil {
					t.Fatal(err)
				}
				if calls != before {
					t.Fatal("unsupported/limited/sftp-only owner repeated command")
				}
			}
		})
	}
}

func TestInboxProbeSharedByConcurrentRequestViews(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var probes, lists atomic.Int32
	both := make(chan struct{})
	release := make(chan struct{})
	a := &inboxAcceleration{run: func(ctx context.Context, command string, consume func(io.Reader) error) error {
		if command == inboxShell(inboxProbe) {
			probes.Add(1)
			return consume(bytes.NewReader([]byte("KP-CAP-1\n\x00\xff\n")))
		}
		if lists.Add(1) == 2 {
			close(both)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return consume(strings.NewReader("KP-INBOX-1\nEND\n"))
	}}
	identity := testIdentity(t)
	connection := NewConnection(ctx, func(ctx context.Context) (*Session, error) {
		return &Session{ctx: ctx, Identity: identity, inbox: a, closeTransport: func() error { return nil }}, nil
	})
	defer connection.Close()
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, run := range []func(context.Context, func(*Session) error) error{connection.Do, connection.Poll} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- run(ctx, func(s *Session) error { _, _, _, err := s.List(&Config{}); return err })
		}()
	}
	select {
	case <-both:
	case <-ctx.Done():
		t.Fatal("probe cache serialized complete inbox operations")
	}
	close(release)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if probes.Load() != 1 {
		t.Fatalf("operation views probed %d times", probes.Load())
	}
}

func TestInboxTimestampOutsideSFTPV3FallsBack(t *testing.T) {
	s := &Session{}
	for _, stamp := range []string{"-1", "4294967296"} {
		items, rejected, ids, err := s.decodeInbox(&Config{}, strings.NewReader("KP-INBOX-1\nR "+strings.Repeat("a", 32)+" "+stamp+" 0\n\nEND\n"))
		if !errors.Is(err, ErrInboxAccelerationUnsupported) || items != nil || rejected != nil || ids != nil {
			t.Fatal("timestamp changed SFTP semantics:", stamp, err)
		}
	}
}
