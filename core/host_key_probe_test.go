package core

import (
	"bytes"
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

	"golang.org/x/crypto/ssh"
)

type invalidHostSigner struct{ ssh.Signer }

func (s invalidHostSigner) Sign(random io.Reader, data []byte) (*ssh.Signature, error) {
	return s.Signer.Sign(random, append(append([]byte(nil), data...), 1))
}

func TestHostKeyProbeStopsBeforeAuthentication(t *testing.T) {
	ed, err := ssh.ParsePrivateKey(testIdentity(t).private)
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
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &ssh.Certificate{Key: ed.PublicKey(), CertType: ssh.HostCert,
		ValidPrincipals: []string{"127.0.0.1"}, ValidBefore: ssh.CertTimeInfinity}
	if err := certificate.SignCert(rand.Reader, ed); err != nil {
		t.Fatal(err)
	}
	certSigner, err := ssh.NewCertSigner(certificate, ed)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		signer  ssh.Signer
		invalid bool
		extra   ssh.Signer
	}{
		{"Ed25519", ed, false, nil}, {"ECDSA", ecdsaSigner, false, nil}, {"RSA", rsaSigner, false, nil},
		{"ordinary-key-alongside-certificate", ed, false, certSigner},
		{"invalid-handshake-signature", invalidHostSigner{ed}, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			finished := make(chan struct{})
			authentications := 0
			go func() {
				defer close(finished)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(2 * time.Second))
				config := &ssh.ServerConfig{NoClientAuth: true, AuthLogCallback: func(ssh.ConnMetadata, string, error) { authentications++ }}
				config.AddHostKey(tc.signer)
				if tc.extra != nil {
					config.AddHostKey(tc.extra)
				}
				connection, _, _, _ := ssh.NewServerConn(raw, config)
				if connection != nil {
					connection.Close()
				}
			}()
			address, text, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(text)
			key, err := ProbeHostKey(context.Background(), address, port, time.Second)
			if tc.invalid {
				if err == nil || key != nil {
					t.Fatal("invalid handshake signature produced a candidate key")
				}
			} else if err != nil || !bytes.Equal(key.Marshal(), tc.signer.PublicKey().Marshal()) {
				t.Fatalf("server key discovery: %v", err)
			}
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("probe did not release the server connection")
			}
			if authentications != 0 {
				t.Fatalf("probe attempted %d authentications before approval", authentications)
			}
		})
	}
}

func TestHostKeyProbeTimeoutAndCancellation(t *testing.T) {
	for _, abort := range []bool{false, true} {
		name := "timeout"
		if abort {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted, finished := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(finished)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(2 * time.Second))
				close(accepted)
				io.Copy(io.Discard, raw)
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if abort {
				go func() { <-accepted; cancel() }()
			}
			address, text, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(text)
			start := time.Now()
			key, err := ProbeHostKey(ctx, address, port, 100*time.Millisecond)
			want := context.DeadlineExceeded
			if abort {
				want = context.Canceled
			}
			if key != nil || !errors.Is(err, want) || time.Since(start) > time.Second {
				t.Fatalf("probe did not stop promptly: %v", err)
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("aborted probe left its socket open")
			}
		})
	}
}

func TestProbedKeyIsEnforcedOnNextConnection(t *testing.T) {
	identity := testIdentity(t)
	first, _ := ssh.ParsePrivateKey(testIdentity(t).private)
	replacement, _ := ssh.ParsePrivateKey(testIdentity(t).private)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan struct{})
	authentications := 0
	go func() {
		defer close(finished)
		for _, signer := range []ssh.Signer{first, replacement} {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			defer raw.Close()
			raw.SetDeadline(time.Now().Add(2 * time.Second))
			config := &ssh.ServerConfig{NoClientAuth: true, AuthLogCallback: func(ssh.ConnMetadata, string, error) { authentications++ }}
			config.AddHostKey(signer)
			connection, _, _, _ := ssh.NewServerConn(raw, config)
			if connection != nil {
				connection.Close()
			}
		}
	}()
	address, text, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(text)
	observed, err := ProbeHostKey(context.Background(), address, port, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	session, err := ConnectEmbedded(context.Background(), EmbeddedOptions{Address: address, Port: port, User: "user",
		Identity: identity, HostKey: observed, Root: ".", Discovery: "keys", Timeout: time.Second})
	if session != nil {
		session.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "host key does not match trusted key") {
		t.Fatalf("a key changed between discovery and connection was not rejected: %v", err)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("changed-key connection was not released")
	}
	if authentications != 0 {
		t.Fatalf("unapproved replacement key received %d authentications", authentications)
	}
}
