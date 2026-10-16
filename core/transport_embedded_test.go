package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestEmbeddedSessionLifecycle(t *testing.T) {
	id, hostID := testIdentity(t), testIdentity(t)
	edSigner, err := ssh.ParsePrivateKey(hostID.private)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(ecdsaKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		hostKey      ssh.PublicKey
		rsaAlgorithm string
		cancel       bool
	}{
		{name: "Ed25519-close", hostKey: edSigner.PublicKey()},
		{name: "Ed25519-cancel", hostKey: edSigner.PublicKey(), cancel: true},
		{name: "RSA-SHA256-close", hostKey: rsaSigner.PublicKey(), rsaAlgorithm: ssh.KeyAlgoRSASHA256},
		{name: "RSA-SHA512-close", hostKey: rsaSigner.PublicKey(), rsaAlgorithm: ssh.KeyAlgoRSASHA512},
		{name: "ECDSA-close", hostKey: ecdsaSigner.PublicKey()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverRSA := rsaSigner
			if tc.rsaAlgorithm != "" {
				limited, err := ssh.NewSignerWithAlgorithms(rsaSigner.(ssh.AlgorithmSigner), []string{tc.rsaAlgorithm})
				if err != nil {
					t.Fatal("restrict server RSA algorithm:", err)
				}
				serverRSA = limited
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted, finished := make(chan struct{}), make(chan struct{})
			root := t.TempDir()
			go func() {
				defer close(finished)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				config := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
					if metadata.User() != "selected-user" || string(key.Marshal()) != string(id.Public.Marshal()) {
						return nil, errors.New("wrong identity")
					}
					return nil, nil
				}}
				config.AddHostKey(edSigner)
				config.AddHostKey(ecdsaSigner)
				config.AddHostKey(serverRSA)
				server, channels, requests, err := ssh.NewServerConn(raw, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for newChannel := range channels {
					channel, requests, err := newChannel.Accept()
					if err != nil {
						return
					}
					for request := range requests {
						if request.Type != "subsystem" {
							request.Reply(false, nil)
							continue
						}
						request.Reply(true, nil)
						fs, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(root))
						if err != nil {
							channel.Close()
							return
						}
						close(accepted)
						fs.Serve()
						fs.Close()
						return
					}
				}
			}()
			address, portText, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(portText)
			id.Path = ""
			t.Setenv("PATH", t.TempDir())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session, err := ConnectEmbedded(ctx, EmbeddedOptions{Address: address, Port: port, User: "selected-user", Identity: id, HostKey: tc.hostKey, Root: "inbox-root", Discovery: "keys", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			<-accepted
			if session.Root != root+"/inbox-root" || session.DiscoveryPath != root+"/keys" {
				t.Fatal("remote paths:", session.Root, session.DiscoveryPath)
			}
			if _, err := session.Client.Getwd(); err != nil {
				t.Fatal(err)
			}
			if tc.cancel {
				cancel()
			} else {
				if err := session.Close(); err != nil {
					t.Fatal("healthy session Close failed:", err)
				}
			}
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("session shutdown did not release SSH/SFTP connection")
			}
			if _, err := session.Client.Getwd(); err == nil {
				t.Fatal("closed session remains usable")
			}
			if err := session.Close(); err != nil {
				t.Fatal("session cleanup failed:", err)
			}
			if err := session.Close(); err != nil {
				t.Fatal("repeated Close failed:", err)
			}

		})
	}
}

func TestEmbeddedSetupTimeoutAndCancellation(t *testing.T) {
	id := testIdentity(t)
	for _, cancelSetup := range []bool{false, true} {
		name := "timeout"
		if cancelSetup {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted, finished := make(chan struct{}), make(chan error, 1)
			go func() {
				raw, err := listener.Accept()
				if err != nil {
					finished <- err
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(2 * time.Second))
				close(accepted)
				_, err = io.Copy(io.Discard, raw)
				finished <- err
			}()
			address, text, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(text)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelSetup {
				go func() { <-accepted; cancel() }()
			}
			start := time.Now()
			_, err = ConnectEmbedded(ctx, EmbeddedOptions{Address: address, Port: port, User: "user", Identity: id, HostKey: id.Public, Root: ".", Discovery: "keys", Timeout: 100 * time.Millisecond})
			want := context.DeadlineExceeded
			if cancelSetup {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("expected %v, got %v", want, err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("connection setup did not stop promptly")
			}
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal("failed setup did not close socket:", err)
				}
			case <-time.After(time.Second):
				t.Fatal("failed setup leaked socket")
			}
		})
	}
	for _, change := range []func(*EmbeddedOptions){
		func(o *EmbeddedOptions) { o.HostKey = nil },
		func(o *EmbeddedOptions) { o.Port = 0 },
		func(o *EmbeddedOptions) { o.User = "" },
	} {
		o := EmbeddedOptions{Address: "unused", Port: 22, User: "user", Identity: id, HostKey: id.Public, Root: ".", Discovery: "keys"}
		change(&o)
		if _, err := ConnectEmbedded(context.Background(), o); err == nil || !strings.Contains(err.Error(), "settings") {
			t.Fatal("invalid settings:", err)
		}
	}
}

func TestEmbeddedSFTPSetupTimeout(t *testing.T) {
	id := testIdentity(t)
	signer, err := ssh.ParsePrivateKey(id.private)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(2 * time.Second))
		config := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
		config.AddHostKey(signer)
		server, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			finished <- err
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for newChannel := range channels {
			channel, requests, err := newChannel.Accept()
			if err != nil {
				finished <- err
				return
			}
			defer channel.Close()
			for request := range requests {
				request.Reply(request.Type == "subsystem", nil)
				if request.Type == "subsystem" {
					_, err := io.Copy(io.Discard, channel)
					finished <- err
					return
				}
			}
		}
		finished <- errors.New("no SFTP request received")
	}()
	address, text, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(text)
	_, err = ConnectEmbedded(context.Background(), EmbeddedOptions{Address: address, Port: port, User: "user", Identity: id, HostKey: id.Public, Root: ".", Discovery: "keys", Timeout: 200 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("SFTP setup timeout:", err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal("failed SFTP setup did not release connection:", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SFTP setup leaked connection")
	}
}
