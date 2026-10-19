package core

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

// ErrPollBusy means an automatic refresh was skipped; polls never queue.
var ErrPollBusy = errors.New("automatic refresh already running")

type connectionStatus struct {
	name      string
	transport context.Context
}

type connectionLease struct {
	background bool
	ctx        context.Context
	cancel     context.CancelFunc
	fail       context.CancelCauseFunc
	ready      chan struct{}
	channels   [2]*sftpChannel
	session    *Session
	err        error
}

// Connection admits one foreground operation and one automatic poll on its
// separate foreground and background SFTP channels on one SSH owner. Cancellation of admitted I/O disconnects
// both operations; mutations are never replayed.
type Connection struct {
	ctx        context.Context
	cancel     context.CancelFunc
	dial       func(context.Context) (*Session, error)
	foreground chan struct{}
	poll       chan struct{}
	mu         sync.Mutex
	lease      *connectionLease
	status     atomic.Pointer[connectionStatus]
}

// State observes transport loss without probing the server.
func (c *Connection) State() string {
	if c.ctx.Err() != nil {
		return "Suspended"
	}
	s := c.status.Load()
	if s == nil {
		return "Not connected"
	}
	if s.transport.Err() != nil {
		return "Disconnected"
	}
	return s.name
}

func NewConnection(parent context.Context, dial func(context.Context) (*Session, error)) *Connection {
	ctx, cancel := context.WithCancel(parent)
	return &Connection{ctx: ctx, cancel: cancel, dial: dial, foreground: make(chan struct{}, 1), poll: make(chan struct{}, 1)}
}

// Close permanently shuts down the owner, including an in-progress dial.
func (c *Connection) Close() { c.cancel(); c.Disconnect() }

// Disconnect retires the shared transport. Later requests may reconnect.
func (c *Connection) Disconnect() {
	c.mu.Lock()
	lease := c.lease
	c.lease = nil
	c.mu.Unlock()
	if lease != nil {
		lease.cancel()
	}
}

func (c *Connection) Do(ctx context.Context, action func(*Session) error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return c.ctx.Err()
	case c.foreground <- struct{}{}:
	}
	defer func() { <-c.foreground }()
	return c.run(ctx, action, false)
}

// Poll never accumulates waiting refreshes. Foreground admission is independent.
func (c *Connection) Poll(ctx context.Context, action func(*Session) error) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.poll <- struct{}{}:
		defer func() { <-c.poll }()
		return c.run(ctx, action, true)
	default:
		return ErrPollBusy
	}
}

func (c *Connection) acquire(ctx context.Context, background, handoff bool) (*connectionLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if err := c.ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	lease := c.lease
	owner := lease == nil || lease.ctx.Err() != nil
	if owner {
		// Preserve the initiating request's dial policy values, not its lifetime.
		transport, fail := context.WithCancelCause(context.WithoutCancel(ctx))
		cancel := func() { fail(context.Canceled) }
		lease = &connectionLease{background: background, ctx: transport, cancel: cancel, fail: fail, ready: make(chan struct{})}
		c.lease = lease
		c.status.Store(&connectionStatus{"Connecting", transport})
		stop := context.AfterFunc(c.ctx, cancel)
		context.AfterFunc(transport, func() { stop() })
	}
	c.mu.Unlock()
	if !owner {
		// A canceled waiter must not retire the dial owned by another request.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-lease.ctx.Done():
			if errors.Is(context.Cause(lease.ctx), context.Canceled) {
				return nil, context.Canceled
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-c.ctx.Done():
				return nil, c.ctx.Err()
			case <-lease.ready:
			}
		case <-lease.ready:
		}
		if lease.err != nil {
			// A foreground caller waiting on failed background setup gets its
			// own dial policy once. No action has run, and healthy polls stay.
			if handoff && !background && lease.background && !errors.Is(context.Cause(lease.ctx), context.Canceled) {
				return c.acquire(ctx, false, false)
			}
			return nil, lease.err
		}
		return lease, nil
	}
	stop := context.AfterFunc(ctx, lease.cancel)
	session, err := c.dial(lease.ctx)
	stop()
	c.mu.Lock()
	if c.lease != lease || lease.ctx.Err() != nil || ctx.Err() != nil || c.ctx.Err() != nil {
		if err == nil {
			err = context.Canceled
		}
	}
	lease.session, lease.err = session, err
	if err == nil {
		c.status.Store(&connectionStatus{"Connected", lease.ctx})
		context.AfterFunc(lease.ctx, func() { session.Close() })
		wait := session.waitTransport
		if wait == nil && session.Client != nil {
			wait = session.Client.Wait
		}
		slot := 0
		if background {
			slot = 1
		}
		lease.channels[slot] = session.channel
		if wait != nil {
			go func() {
				err := wait()
				if err == nil {
					err = io.ErrClosedPipe
				}
				lease.fail(err)
			}()
		}
	} else {
		lease.fail(err)
	}
	close(lease.ready)
	c.mu.Unlock()
	if err != nil {
		if session != nil {
			session.Close()
		}
		return nil, err
	}
	return lease, nil
}

func (c *Connection) run(ctx context.Context, action func(*Session) error, background bool) error {
	lease, err := c.acquire(ctx, background, !background)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := context.Cause(lease.ctx); err != nil {
		return err
	}
	// Capture this generation: a late cancellation cannot close its replacement.
	retired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { lease.cancel(); close(retired) })
	operation, cancel := context.WithCancel(ctx)
	stopTransport := context.AfterFunc(lease.ctx, cancel)
	// A fresh view avoids mutable shared Session.ctx (and copying sync.Once).
	s := lease.session
	view := &Session{Client: s.Client, Identity: s.Identity, Root: s.Root, DiscoveryPath: s.DiscoveryPath, ctx: operation, closeTransport: s.Close}
	if s.openSFTP != nil {
		slot := 0
		if background {
			slot = 1
		}
		channel := lease.channels[slot]
		if channel != nil {
			select {
			case <-channel.done:
				channel.close()
				s.forgetChannel(channel)
				channel = nil
			default:
			}
		}
		if channel == nil {
			channel, err = s.openChannel()
			if err == nil {
				lease.channels[slot] = channel
			}
		}
		if err == nil {
			view.Client = channel.client
		}
	}
	if err == nil && operation.Err() == nil {
		err = action(view)
	}
	if err == nil && operation.Err() != nil {
		err = operation.Err()
	}
	if !stop() {
		<-retired
	}
	stopTransport()
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	if cause := context.Cause(lease.ctx); cause != nil {
		if err == nil || errors.Is(cause, context.Canceled) {
			return cause
		}
	}
	// Application and channel errors leave the owner usable. Its Wait observes
	// transport loss; no operation, including an uncertain send, is retried here.
	return err
}
