package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type heldListing struct {
	sftp.FileLister
	gate atomic.Pointer[listingGate]
}

type listingGate struct {
	entered chan struct{}
	release chan struct{}
	unblock sync.Once
}

func (l *heldListing) hold() *listingGate {
	gate := &listingGate{entered: make(chan struct{}, 2), release: make(chan struct{})}
	l.gate.Store(gate)
	return gate
}
func (g *listingGate) close() { g.unblock.Do(func() { close(g.release) }) }

func (l *heldListing) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if gate := l.gate.Load(); r.Method == "List" && gate != nil {
		gate.entered <- struct{}{}
		<-gate.release
	}
	return l.FileLister.Filelist(r)
}

// Exercise actual SFTP packets over both supported SSH backends, not just two
// callbacks on a fake Session. The foreground transfer must finish before the held poll is released.
func TestConnectionConcurrentTransport(t *testing.T) {
	for _, backend := range []string{"embedded", "openssh"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "openssh" {
				if _, err := exec.LookPath("ssh"); err != nil {
					t.Skip("OpenSSH client unavailable")
				}
				// ControlPath must also work when the user's temp path has spaces.
				dir, err := os.MkdirTemp("", "kp space-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(dir) })
				t.Setenv("TMPDIR", dir)
			}
			identity, host := testIdentity(t), testIdentity(t)
			signer, err := ssh.ParsePrivateKey(host.private)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			var tcp, authenticated, subsystems atomic.Int32
			owners := make(chan *ssh.ServerConn, 20)
			var rejectChannel atomic.Bool
			execHeld := make(chan struct{})
			handlers := sftp.InMemHandler()
			held := &heldListing{FileLister: handlers.FileList}
			gate := held.hold()
			handlers.FileList = held
			unblock := gate.close
			defer unblock()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			go func() {
				for {
					raw, err := listener.Accept()
					if err != nil {
						return
					}
					tcp.Add(1)
					go func() {
						defer raw.Close()
						stop := context.AfterFunc(ctx, func() { raw.Close() })
						defer stop()
						config := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
							if meta.User() != "selected-user" || !bytes.Equal(key.Marshal(), identity.Public.Marshal()) {
								return nil, errors.New("wrong identity")
							}
							return nil, nil
						}}
						config.AddHostKey(signer)
						server, channels, requests, err := ssh.NewServerConn(raw, config)
						if err != nil {
							return
						}
						authenticated.Add(1)
						owners <- server
						defer server.Close()
						go ssh.DiscardRequests(requests)
						for incoming := range channels {
							if incoming.ChannelType() != "session" {
								incoming.Reject(ssh.UnknownChannelType, "session required")
								continue
							}
							go func(incoming ssh.NewChannel) {
								channel, requests, err := incoming.Accept()
								if err != nil {
									return
								}
								defer channel.Close()
								for request := range requests {
									if request.Type == "exec" {
										var command struct{ Command string }
										if ssh.Unmarshal(request.Payload, &command) != nil {
											request.Reply(false, nil)
											return
										}
										request.Reply(true, nil)
										code := uint32(0)
										switch command.Command {
										case "test:binary":
											channel.Write([]byte{0, 255, 10})
										case "test:hold":
											close(execHeld)
											io.Copy(io.Discard, channel)
										case "test:exit":
											code = 7
										case "test:stderr":
											channel.Stderr().Write(bytes.Repeat([]byte("x"), 128*1024))
										case "test:malformed":
											channel.Write([]byte("bad frame\n"))
										default:
											code = 127
										}
										channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
										return
									}
									if request.Type != "subsystem" {
										request.Reply(false, nil)
										continue
									}
									if rejectChannel.Swap(false) {
										request.Reply(false, nil)
										return
									}
									subsystems.Add(1)
									request.Reply(true, nil)
									fs := sftp.NewRequestServer(channel, handlers)
									fs.Serve()
									fs.Close()
									return
								}
							}(incoming)
						}
					}()
				}
			}()
			address, portText, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(portText)
			root := t.TempDir()
			identity.Path = filepath.Join(root, "identity")
			if err := os.WriteFile(identity.Path, identity.private, 0600); err != nil {
				t.Fatal(err)
			}
			known := filepath.Join(root, "known_hosts")
			if err := os.WriteFile(known, []byte(fmt.Sprintf("[%s]:%d %s", address, port, PublicText(host.Public))), 0600); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "ssh_config")
			if err := os.WriteFile(config, []byte(fmt.Sprintf("Host fixture\n HostName %s\n Port %d\n User selected-user\n UserKnownHostsFile %q\n GlobalKnownHostsFile /dev/null\n IdentityAgent none\n", address, port, known)), 0600); err != nil {
				t.Fatal(err)
			}
			c := NewConnection(ctx, func(ctx context.Context) (*Session, error) {
				if backend == "openssh" {
					return ConnectForCompletion(ctx, "fixture", Host{Identity: identity.Path, Fingerprint: ssh.FingerprintSHA256(identity.Public), Root: "/", Discovery: "/keys"}, config)
				}
				return ConnectEmbedded(ctx, EmbeddedOptions{Address: address, Port: port, User: "selected-user", HostKey: host.Public, Identity: identity, Root: "/", Discovery: "/keys", Timeout: time.Second})
			})
			defer c.Close()
			polled := make(chan error, 1)
			go func() { polled <- c.Poll(ctx, func(s *Session) error { _, err := s.Client.ReadDir("/"); return err }) }()
			select {
			case <-gate.entered:
			case err := <-polled:
				t.Fatal("poll did not enter server:", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			payload := []byte("foreground bytes while poll is held")
			transferred := make(chan error, 1)
			go func() {
				transferred <- c.Do(ctx, func(s *Session) error {
					file, err := s.Client.Create("/parcel")
					if err != nil {
						return err
					}
					if _, err = file.Write(payload); err != nil {
						file.Close()
						return err
					}
					if err = file.Close(); err != nil {
						return err
					}
					read, err := s.Client.Open("/parcel")
					if err != nil {
						return err
					}
					defer read.Close()
					data := make([]byte, len(payload))
					if _, err = read.ReadAt(data, 0); err != nil {
						return err
					}
					if !bytes.Equal(data, payload) {
						return errors.New("foreground bytes changed")
					}
					return nil
				})
			}()
			select {
			case err := <-transferred:
				if err != nil {
					t.Fatal("foreground transfer while poll held:", err)
				}
			case <-ctx.Done():
				t.Fatal("foreground blocked by background SFTP channel", ctx.Err())
			}
			if tcp.Load() != 1 || authenticated.Load() != 1 || subsystems.Load() != 2 {
				t.Fatalf("expected one TCP/auth and two SFTP subsystems, got %d/%d/%d", tcp.Load(), authenticated.Load(), subsystems.Load())
			}
			t.Logf("held poll foreground transfer: TCP=%d auth=%d SFTP=%d", tcp.Load(), authenticated.Load(), subsystems.Load())
			unblock()
			if err := <-polled; err != nil {
				t.Fatal("poll failed after concurrent transfer:", err)
			}
			// Exec channels share this owner and must never retire its SFTP roles
			// on a remote exit, bounded stderr, or malformed read-only response.
			for _, command := range []string{"test:binary", "test:exit", "test:stderr", "test:malformed"} {
				err := c.Do(ctx, func(s *Session) error {
					return s.access.run(ctx, command, func(r io.Reader) error {
						if command == "test:malformed" {
							_, _, _, err := s.decodeInbox(&Config{}, r)
							return err
						}
						data, err := io.ReadAll(io.LimitReader(r, 16))
						if command == "test:binary" && !bytes.Equal(data, []byte{0, 255, 10}) {
							return errors.New("exec changed binary bytes")
						}
						return err
					})
				})
				if command == "test:binary" && err != nil {
					t.Fatal("binary exec:", err)
				}
				if command != "test:binary" && err == nil {
					t.Fatal("exec failure was hidden:", command)
				}
				if command == "test:stderr" && !errors.Is(err, errCommandOutput) {
					t.Fatal("stderr bound:", err)
				}
				if err := c.Do(ctx, func(s *Session) error { _, err := s.Client.Stat("/parcel"); return err }); err != nil {
					t.Fatal("exec failure retired SFTP owner:", command, err)
				}
			}
			if tcp.Load() != 1 || authenticated.Load() != 1 {
				t.Fatal("exec opened another authenticated transport")
			}
			// A normal remote application error must preserve the same authenticated socket.
			if err := c.Do(ctx, func(s *Session) error { _, err := s.Client.Stat("/absent"); return err }); !os.IsNotExist(err) {
				t.Fatal("missing expected file error:", err)
			}
			if err := c.Do(ctx, func(s *Session) error { _, err := s.Client.Stat("/parcel"); return err }); err != nil {
				t.Fatal(err)
			}
			if tcp.Load() != 1 || authenticated.Load() != 1 {
				t.Fatal("file error retired transport")
			}
			// Closing only the poll's SFTP channel must preserve the foreground channel
			// and authentication. Its next request gets a fresh channel, not a replay.
			var failedChannel *sftp.Client
			if err := c.Poll(ctx, func(s *Session) error { failedChannel = s.Client; return s.Client.Close() }); err != nil {
				t.Fatal(err)
			}
			if err := c.Do(ctx, func(s *Session) error { _, err := s.Client.Stat("/parcel"); return err }); err != nil {
				t.Fatal("poll channel failure killed foreground:", err)
			}
			if c.State() != "Connected" {
				t.Fatal("channel failure reported owner disconnected")
			}
			c.mu.Lock()
			failed := c.lease.channels[1]
			c.mu.Unlock()
			select {
			case <-failed.done:
			case <-ctx.Done():
				t.Fatal("closed channel not observed")
			}
			rejectChannel.Store(true)
			if err := c.Poll(ctx, func(s *Session) error { t.Error("failed channel setup ran action"); return nil }); err == nil {
				t.Fatal("rejected subsystem setup succeeded")
			}
			if c.State() != "Connected" {
				t.Fatal("subsystem rejection retired owner")
			}
			if err := c.Do(ctx, func(s *Session) error { _, err := s.Client.Stat("/parcel"); return err }); err != nil {
				t.Fatal("subsystem rejection killed foreground:", err)
			}
			if err := c.Poll(ctx, func(s *Session) error {
				if s.Client == failedChannel {
					return errors.New("dead poll channel reused")
				}
				_, err := s.Client.Stat("/parcel")
				return err
			}); err != nil {
				t.Fatal("replacement poll channel:", err)
			}
			if tcp.Load() != 1 || authenticated.Load() != 1 || subsystems.Load() != 3 {
				t.Fatalf("channel repair changed ownership: %d/%d/%d", tcp.Load(), authenticated.Load(), subsystems.Load())
			}

			// An admitted cancellation still retires both channels. No foreground
			// mutation is replayed, and a later request may establish a new owner.
			gate = held.hold()
			defer gate.close()
			go func() { polled <- c.Poll(ctx, func(s *Session) error { _, err := s.Client.ReadDir("/"); return err }) }()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			admitted, cancelAdmitted := context.WithCancel(ctx)
			var actions atomic.Int32
			err = c.Do(admitted, func(s *Session) error {
				actions.Add(1)
				if _, err := s.Client.Stat("/parcel"); err != nil {
					return err
				}
				go func() {
					select {
					case <-execHeld:
						cancelAdmitted()
					case <-ctx.Done():
					}
				}()
				return s.access.run(s.ctx, "test:hold", func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatal("admitted cancellation:", err)
			}
			select {
			case err := <-polled:
				if err == nil {
					t.Fatal("poll survived global cancellation")
				}
			case <-ctx.Done():
				t.Fatal("cancel did not release background I/O")
			}
			gate.close()
			if actions.Load() != 1 || c.State() != "Disconnected" {
				t.Fatal("cancel replayed action or retained owner", actions.Load(), c.State())
			}
			if err := c.Do(ctx, func(s *Session) error { _, err := s.Client.Stat("/parcel"); return err }); err != nil {
				t.Fatal("reconnect after explicit cancel:", err)
			}
			if tcp.Load() != 2 || authenticated.Load() != 2 {
				t.Fatal("later request did not reconnect exactly once")
			}
			<-owners // Retired first owner.
			owner := <-owners
			gate = held.hold()
			defer gate.close()
			go func() { polled <- c.Poll(ctx, func(s *Session) error { _, err := s.Client.ReadDir("/"); return err }) }()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			go func() {
				transferred <- c.Do(ctx, func(s *Session) error { _, err := s.Client.ReadDir("/"); return err })
			}()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("foreground did not reach separate server handler")
			}
			owner.Close()
			for _, done := range []chan error{polled, transferred} {
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("owner death left operation successful")
					}
				case <-ctx.Done():
					t.Fatal("owner death left blocked operation")
				}
			}
			gate.close()
			c.mu.Lock()
			lease := c.lease
			c.mu.Unlock()
			select {
			case <-lease.ctx.Done():
			case <-ctx.Done():
				t.Fatal("owner death not observed")
			}
			if c.State() != "Disconnected" {
				t.Fatal("owner death reported connected")
			}

			if backend == "openssh" {
				c.Close()
				checkOpenSSHMasterLifecycle(t, ctx, identity, config, &tcp, &authenticated, &subsystems)
			}
		})
	}
}
