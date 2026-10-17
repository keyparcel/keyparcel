package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionReuseAndFailureRetirement(t *testing.T) {
	var dials, closes atomic.Int32
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		dials.Add(1)
		return &Session{ctx: ctx, closeTransport: func() error { closes.Add(1); return nil }}, nil
	})
	defer c.Close()
	for range 3 {
		ctx, cancel := context.WithCancel(context.Background())
		err := c.Do(ctx, func(s *Session) error { return nil })
		cancel() // A finished request's deadline must not close the reusable session.
		if err != nil {
			t.Fatal(err)
		}
	}
	if dials.Load() != 1 {
		t.Fatal("successful requests did not reuse one connection")
	}
	sentinel := errors.New("delivery failed")
	if err := c.Do(context.Background(), func(s *Session) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if closes.Load() != 1 {
		t.Fatal("failed request did not retire the connection")
	}
	if err := c.Do(context.Background(), func(s *Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 2 {
		t.Fatal("next request did not reconnect after failure")
	}
}

func TestConnectionCancellationDoesNotLeakIntoNextLease(t *testing.T) {
	var dials atomic.Int32
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		dials.Add(1)
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Do(ctx, func(s *Session) error { <-s.ctx.Done(); return s.ctx.Err() }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request deadline: %v", err)
	}
	if err := c.Do(context.Background(), func(s *Session) error { return s.ctx.Err() }); err != nil {
		t.Fatalf("next request inherited cancellation: %v", err)
	}
	if dials.Load() != 2 {
		t.Fatal("canceled connection was reused")
	}
}

func TestConnectionSerializesRequestsAndCloseInterruptsSetup(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	result := make(chan error, 1)
	go func() { result <- c.Do(context.Background(), func(s *Session) error { return nil }) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Do(ctx, func(s *Session) error { t.Error("canceled queued request executed"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c.Close()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt setup")
	}
	close(release)
}

func TestConnectionSerializesLiveActions(t *testing.T) {
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 2)
	go func() {
		done <- c.Do(context.Background(), func(s *Session) error { close(entered); <-release; return nil })
	}()
	<-entered
	second := make(chan struct{})
	go func() { done <- c.Do(context.Background(), func(s *Session) error { close(second); return nil }) }()
	select {
	case <-second:
		t.Fatal("requests overlapped on the same session")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestConnectionStateTracksTransportNotRequestResults(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var transport context.Context
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		transport = ctx
		close(entered)
		<-release
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	if c.State() != "Not connected" {
		t.Fatal(c.State())
	}
	done := make(chan error, 1)
	go func() { done <- c.Do(context.Background(), func(*Session) error { return nil }) }()
	<-entered
	if c.State() != "Connecting" {
		t.Fatal("dial is in progress:", c.State())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if c.State() != "Connected" {
		t.Fatal("authenticated session:", c.State())
	}
	// No polling request is needed to observe a retired transport.
	c.retire()
	<-transport.Done()
	if c.State() != "Disconnected" {
		t.Fatal("lost transport:", c.State())
	}
	c.Close()
	if c.State() != "Suspended" {
		t.Fatal("intentional close:", c.State())
	}
}

func TestConnectionStateReportsFailedAuthentication(t *testing.T) {
	c := NewConnection(context.Background(), func(context.Context) (*Session, error) { return nil, errors.New("authentication failed") })
	defer c.Close()
	if err := c.Do(context.Background(), func(*Session) error { t.Fatal("failed dial ran action"); return nil }); err == nil {
		t.Fatal("missing error")
	}
	if c.State() != "Disconnected" {
		t.Fatal("failed dial:", c.State())
	}
}
