package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
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
	if closes.Load() != 0 {
		t.Fatal("application error retired the connection")
	}
	if err := c.Do(context.Background(), func(s *Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 1 {
		t.Fatal("application error caused a reconnect")
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
	c.Disconnect()
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

func TestConnectionConcurrentPollAndForegroundShareDial(t *testing.T) {
	var dials atomic.Int32
	dialing, releaseDial := make(chan struct{}), make(chan struct{})
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		dials.Add(1)
		close(dialing)
		<-releaseDial
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	pollEntered, foregroundEntered, release := make(chan *Session, 1), make(chan *Session, 1), make(chan struct{})
	done := make(chan error, 2)
	type key struct{}
	pollContext := context.WithValue(context.Background(), key{}, "poll")
	foregroundContext := context.WithValue(context.Background(), key{}, "foreground")
	go func() {
		done <- c.Poll(pollContext, func(s *Session) error { pollEntered <- s; <-release; return nil })
	}()
	<-dialing
	go func() {
		done <- c.Do(foregroundContext, func(s *Session) error { foregroundEntered <- s; <-release; return nil })
	}()
	close(releaseDial)
	p, f := <-pollEntered, <-foregroundEntered
	if p == f || p.ctx.Value(key{}) != "poll" || f.ctx.Value(key{}) != "foreground" {
		t.Fatal("operations did not receive independent immutable context views")
	}
	if dials.Load() != 1 {
		t.Fatal("concurrent initial operations created multiple transports")
	}
	if err := c.Poll(context.Background(), func(*Session) error { t.Fatal("second poll executed"); return nil }); !errors.Is(err, ErrPollBusy) {
		t.Fatal(err)
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestConnectionCanceledDialWaiterDoesNotRetireOwner(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		close(entered)
		<-release
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	done := make(chan error, 1)
	go func() { done <- c.Do(context.Background(), func(*Session) error { return nil }) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Poll(ctx, func(*Session) error { t.Fatal("canceled waiter ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal("canceled waiter retired healthy dial:", err)
	}
	if c.State() != "Connected" {
		t.Fatal(c.State())
	}
}

func TestConnectionActiveCancellationDisconnectsBothWithoutReplay(t *testing.T) {
	var dials, actions atomic.Int32
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		dials.Add(1)
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	entered := make(chan struct{}, 2)
	done := make(chan error, 2)
	action := func(s *Session) error { actions.Add(1); entered <- struct{}{}; <-s.ctx.Done(); return s.ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- c.Do(ctx, action) }()
	go func() { done <- c.Poll(context.Background(), action) }()
	<-entered
	<-entered
	cancel()
	for range 2 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if err := c.Do(context.Background(), func(s *Session) error { return s.ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 2 || actions.Load() != 2 {
		t.Fatal("wrong reconnect count or mutation replay", dials.Load(), actions.Load())
	}
}

func TestConnectionDisconnectedDialCannotResurrect(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var closes atomic.Int32
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		close(entered)
		<-release
		return &Session{ctx: ctx, closeTransport: func() error { closes.Add(1); return nil }}, nil
	})
	defer c.Close()
	done := make(chan error, 1)
	go func() {
		done <- c.Do(context.Background(), func(*Session) error { t.Error("stale dial ran operation"); return nil })
	}()
	<-entered
	c.Disconnect()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if c.State() != "Disconnected" || closes.Load() != 1 {
		t.Fatal("stale connection resurrected", c.State(), closes.Load())
	}
}

type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestConnectionCanceledWaitersLeaveHealthyTransport(t *testing.T) {
	for _, waitingForDial := range []bool{false, true} {
		t.Run(fmt.Sprintf("dial=%v", waitingForDial), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var dials atomic.Int32
			c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
				dials.Add(1)
				if waitingForDial {
					close(entered)
					<-release
				}
				return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
			})
			defer c.Close()
			done := make(chan error, 1)
			go func() {
				done <- c.Do(context.Background(), func(*Session) error {
					if !waitingForDial {
						close(entered)
						<-release
					}
					return nil
				})
			}()
			<-entered
			ctx, cancel := context.WithCancel(context.Background())
			observed := &waitingContext{Context: ctx, waiting: make(chan struct{})}
			waiter := make(chan error, 1)
			run := c.Do
			if waitingForDial {
				run = c.Poll
			}
			go func() {
				waiter <- run(observed, func(*Session) error { t.Error("canceled waiter executed"); return nil })
			}()
			<-observed.waiting
			cancel()
			if err := <-waiter; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal("waiter disconnected owner:", err)
			}
			if err := c.Do(context.Background(), func(*Session) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if dials.Load() != 1 || c.State() != "Connected" {
				t.Fatal("waiter retired healthy transport")
			}
		})
	}
}

func TestConnectionTransportLossPreservesFailureInsteadOfReportingUserCancel(t *testing.T) {
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	failure := errors.New("SSH stream lost during receive")
	err := c.Do(context.Background(), func(s *Session) error {
		c.mu.Lock()
		lease := c.lease
		c.mu.Unlock()
		lease.fail(io.ErrUnexpectedEOF)
		<-s.ctx.Done()
		return failure
	})
	if !errors.Is(err, failure) || c.State() != "Disconnected" {
		t.Fatal("transport failure was mislabeled as user cancellation", err, c.State())
	}
}

func TestConnectionOldCancellationCannotRetireReplacement(t *testing.T) {
	var dials atomic.Int32
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		dials.Add(1)
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	oldEntered, releaseOld := make(chan struct{}), make(chan struct{})
	oldDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { oldDone <- c.Poll(ctx, func(*Session) error { close(oldEntered); <-releaseOld; return nil }) }()
	<-oldEntered
	c.Disconnect()
	if err := c.Do(context.Background(), func(*Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	cancel() // The retired poll still owns a callback, after a new dial succeeded.
	close(releaseOld)
	if err := <-oldDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), func(*Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if c.State() != "Connected" || dials.Load() != 2 {
		t.Fatal("old callback retired replacement transport")
	}
}

func TestForegroundOwnsSetupAfterWaitingForFailedBackgroundDial(t *testing.T) {
	type policy struct{}
	started, release := make(chan struct{}), make(chan struct{})
	backgroundFailure := errors.New("background cannot prompt for host approval")
	var dials, actions atomic.Int32
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		dials.Add(1)
		if ctx.Value(policy{}) != "foreground" {
			close(started)
			<-release
			return nil, backgroundFailure
		}
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	pollDone, foregroundDone := make(chan error, 1), make(chan error, 1)
	go func() {
		pollDone <- c.Poll(context.Background(), func(*Session) error { t.Error("failed poll ran action"); return nil })
	}()
	<-started
	// Foreground Do evaluates Done once for admission, again while waiting for dial.
	ctx := &dialWaitingContext{Context: context.WithValue(context.Background(), policy{}, "foreground"), waiting: make(chan struct{})}
	go func() { foregroundDone <- c.Do(ctx, func(*Session) error { actions.Add(1); return nil }) }()
	<-ctx.waiting
	close(release)
	if err := <-pollDone; !errors.Is(err, backgroundFailure) {
		t.Fatal(err)
	}
	if err := <-foregroundDone; err != nil {
		t.Fatal("foreground inherited noninteractive setup failure:", err)
	}
	if dials.Load() != 2 || actions.Load() != 1 {
		t.Fatal("setup handoff retried action or created extra transports", dials.Load(), actions.Load())
	}
}

type dialWaitingContext struct {
	context.Context
	calls   atomic.Int32
	waiting chan struct{}
}

func (c *dialWaitingContext) Done() <-chan struct{} {
	if c.calls.Add(1) == 2 {
		close(c.waiting)
	}
	return c.Context.Done()
}

func TestForegroundDoesNotReplaceCanceledBackgroundSetup(t *testing.T) {
	started := make(chan struct{})
	var dials atomic.Int32
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		dials.Add(1)
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	defer c.Close()
	pollContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	go func() {
		done <- c.Poll(pollContext, func(*Session) error { t.Error("canceled setup ran poll"); return nil })
	}()
	<-started
	waiter := &dialWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
	go func() {
		done <- c.Do(waiter, func(*Session) error { t.Error("shared cancellation ran foreground action"); return nil })
	}()
	<-waiter.waiting
	cancel()
	for range 2 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if dials.Load() != 1 {
		t.Fatal("foreground replaced explicitly canceled setup")
	}
}
