package core

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
)

func TestSessionCloseDuringChannelSetup(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var owners, channels atomic.Int32
	s := &Session{
		closeTransport: func() error { owners.Add(1); return nil },
		openSFTP: func() (*sftpChannel, error) {
			close(entered)
			<-release
			return &sftpChannel{close: func() error { channels.Add(1); return nil }}, nil
		},
	}
	done := make(chan error, 1)
	go func() { _, err := s.openChannel(); done <- err }()
	<-entered
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, net.ErrClosed) {
		t.Fatal("late channel was published after owner close:", err)
	}
	s.Close()
	if owners.Load() != 1 || channels.Load() != 1 || len(s.channels) != 0 {
		t.Fatal("late channel or owner leaked", owners.Load(), channels.Load(), len(s.channels))
	}
}
