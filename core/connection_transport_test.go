package core

import (
	"bytes"
	"context"
	"encoding/binary"
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
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (l *heldListing) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if r.Method == "List" {
		l.once.Do(func() { close(l.entered) })
		<-l.release
	}
	return l.FileLister.Filelist(r)
}

// The fixture serializes command replies. Observe the foreground OPEN on the
// wire while the poll is held, then release it; one SFTP stream cannot promise
// progress past a server that blocks its own packet processing.
type observedSFTP struct {
	ssh.Channel
	pending *bytes.Reader
	opened  chan struct{}
	once    sync.Once
}

func (c *observedSFTP) Read(p []byte) (int, error) {
	if c.pending == nil || c.pending.Len() == 0 {
		var header [4]byte
		if _, err := io.ReadFull(c.Channel, header[:]); err != nil {
			return 0, err
		}
		size := int(binary.BigEndian.Uint32(header[:]))
		if size > 1<<20 {
			return 0, errors.New("unexpected fixture packet size")
		}
		packet := make([]byte, 4+size)
		copy(packet, header[:])
		if _, err := io.ReadFull(c.Channel, packet[4:]); err != nil {
			return 0, err
		}
		if size > 0 && packet[4] == 3 {
			c.once.Do(func() { close(c.opened) })
		}
		c.pending = bytes.NewReader(packet)
	}
	return c.pending.Read(p)
}

// Exercise actual SFTP packets over both supported SSH backends, not just two
// callbacks on a fake Session. The server observes both requests before releasing the held poll.
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
			foregroundQueued := make(chan struct{})
			handlers := sftp.InMemHandler()
			held := &heldListing{FileLister: handlers.FileList, entered: make(chan struct{}), release: make(chan struct{})}
			handlers.FileList = held
			var release sync.Once
			unblock := func() { release.Do(func() { close(held.release) }) }
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
						defer server.Close()
						go ssh.DiscardRequests(requests)
						for incoming := range channels {
							if incoming.ChannelType() != "session" {
								incoming.Reject(ssh.UnknownChannelType, "session required")
								continue
							}
							channel, requests, err := incoming.Accept()
							if err != nil {
								return
							}
							for request := range requests {
								if request.Type != "subsystem" {
									request.Reply(false, nil)
									continue
								}
								subsystems.Add(1)
								request.Reply(true, nil)
								fs := sftp.NewRequestServer(&observedSFTP{Channel: channel, opened: foregroundQueued}, handlers)
								fs.Serve()
								fs.Close()
								return
							}
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
			case <-held.entered:
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
			case <-foregroundQueued:
			case err := <-transferred:
				t.Fatal("foreground request did not reach wire:", err)
			case <-ctx.Done():
				t.Fatal("foreground queued behind poll instead of sharing client", ctx.Err())
			}
			unblock()
			if err := <-transferred; err != nil {
				t.Fatal("concurrent foreground bytes:", err)
			}
			if tcp.Load() != 1 || authenticated.Load() != 1 || subsystems.Load() != 1 {
				t.Fatalf("expected one TCP/auth/SFTP subsystem, got %d/%d/%d", tcp.Load(), authenticated.Load(), subsystems.Load())
			}
			unblock()
			if err := <-polled; err != nil {
				t.Fatal("poll failed after concurrent transfer:", err)
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
			if backend == "openssh" {
				c.Close()
				checkOpenSSHMasterLifecycle(t, ctx, identity, config, &tcp, &authenticated, &subsystems)
			}
		})
	}
}
