package bridge

import (
	"context"
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

type stalledListing struct {
	started chan struct{}
	closed  chan struct{}
}

func (s *stalledListing) Filelist(*sftp.Request) (sftp.ListerAt, error) {
	close(s.started)
	<-s.closed
	return nil, io.EOF
}

func TestRequestDeadlineClosesStalledSFTPAfterSetup(t *testing.T) {
	t.Run("legacy lifetime", func(t *testing.T) { testRequestDeadline(t, false) })
	t.Run("persistent request", func(t *testing.T) { testRequestDeadline(t, true) })
}

func testRequestDeadline(t *testing.T, persistent bool) {
	private, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	public, err := PublicKey(private)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	listing := &stalledListing{started: make(chan struct{}), closed: make(chan struct{})}
	finished := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(5 * time.Second))
		config := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
		config.AddHostKey(signer)
		server, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			finished <- err
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		go func() { server.Wait(); close(listing.closed) }()
		for incoming := range channels {
			channel, requests, err := incoming.Accept()
			if err != nil {
				finished <- err
				return
			}
			go func() {
				for request := range requests {
					if request.Type != "subsystem" {
						request.Reply(false, nil)
						continue
					}
					request.Reply(true, nil)
					fs := sftp.NewRequestServer(channel, sftp.Handlers{FileList: listing})
					err := fs.Serve()
					fs.Close()
					finished <- err
					return
				}
			}()
		}
		finished <- errors.New("no SFTP subsystem requested")
	}()
	address, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	var client *Client
	if persistent {
		client, err = NewPersistentClient(private, address, port, "user", public, "/inbox", "/keys", 1000)
	} else {
		client, err = ConnectWithTimeout(private, address, port, "user", public, "/inbox", "/keys", 1000)
	}
	if err != nil {
		t.Fatal("SSH/SFTP setup should succeed before the request stalls:", err)
	}
	defer client.Close()
	if persistent {
		if err := client.CheckConnection(); err != nil {
			t.Fatal("explicit connection check should authenticate before polling:", err)
		}
	}
	result := make(chan error, 1)
	go func() { _, err := client.Inbox(); result <- err }()
	select {
	case <-listing.started:
	case <-time.After(2 * time.Second):
		t.Fatal("connected client never reached the stalled listing request")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) || err.Error() != "Request timed out. Try again." {
			t.Fatalf("stalled request did not report a retryable timeout: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request deadline did not unblock SFTP")
	}
	select {
	case <-listing.closed:
	case <-time.After(time.Second):
		t.Fatal("request deadline did not close SSH")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stalled server did not release its request")
	}
	for range 2 {
		if err := client.Close(); err != nil {
			t.Fatal("timed-out client cleanup failed:", err)
		}
	}
}

func TestRequestTimeoutRejectsInvalidDurations(t *testing.T) {
	timeouts := []int{0, -1}
	if strconv.IntSize == 64 {
		timeouts = append(timeouts, int(^uint(0)>>1))
	}
	for _, timeout := range timeouts {
		if _, err := ConnectWithTimeout(nil, "unused", 22, "user", "", ".", "keys", timeout); err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("invalid request timeout %d accepted: %v", timeout, err)
		}
	}
}

func TestSetupDeadlineUsesRetryableMessage(t *testing.T) {
	err := requestError(context.Background(), context.DeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) || err.Error() != "Request timed out. Try again." {
		t.Fatalf("setup deadline lost its retry message or cause: %v", err)
	}
	if err := requestError(context.Background(), context.Canceled); err != context.Canceled {
		t.Fatalf("intentional cancellation was reported as a timeout: %v", err)
	}
}

func TestPersistentClientCloseInterruptsHandshake(t *testing.T) {
	private, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	public, err := PublicKey(private)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(5 * time.Second))
		close(accepted)
		io.Copy(io.Discard, raw) // Deliberately never reply to the SSH handshake.
	}()
	address, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	client, err := NewPersistentClient(private, address, port, "user", public, "/inbox", "/keys", 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result := make(chan error, 1)
	go func() { _, err := client.Inbox(); result <- err }()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("persistent client did not start setup")
	}
	client.Close()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("close during setup: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close left handshake running")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("Close left the TCP connection open")
	}
}
