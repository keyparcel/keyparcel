package core

import "context"

// Connection owns one reusable SSH/SFTP session. Do serializes its callers;
// cancellation retires the transport, so a stalled request cannot poison reuse.
// User operations are never retried: their result may already exist remotely.
type Connection struct {
	ctx       context.Context
	cancel    context.CancelFunc
	dial      func(context.Context) (*Session, error)
	gate      chan struct{}
	session   *Session
	transport context.Context
	retire    context.CancelFunc
	stopClose func() bool
}

func NewConnection(parent context.Context, dial func(context.Context) (*Session, error)) *Connection {
	ctx, cancel := context.WithCancel(parent)
	c := &Connection{ctx: ctx, cancel: cancel, dial: dial, gate: make(chan struct{}, 1)}
	c.gate <- struct{}{}
	return c
}

// Close also interrupts setup or an operation in progress. It does not wait for
// a caller's local filesystem work to finish.
func (c *Connection) Close() { c.cancel() }

func (c *Connection) drop() {
	if c.retire != nil {
		c.retire()
	}
	if c.stopClose != nil {
		c.stopClose()
	}
	if c.session != nil {
		c.session.Close()
	}
	c.session = nil
}

func (c *Connection) Do(ctx context.Context, action func(*Session) error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return c.ctx.Err()
	case <-c.gate:
	}
	defer func() { c.gate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if c.session == nil || c.transport.Err() != nil {
		c.drop()
		c.transport, c.retire = context.WithCancel(c.ctx)
	}
	// This cancellation belongs to this lease only. Wait for a racing callback
	// before another request can acquire the session.
	retired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { c.retire(); close(retired) })
	var err error
	if c.session == nil {
		c.session, err = c.dial(c.transport)
		if err == nil {
			session := c.session
			c.stopClose = context.AfterFunc(c.transport, func() { session.Close() })
			if session.Client != nil {
				retire := c.retire
				go func() { session.Client.Wait(); retire() }()
			}
		}
	}
	if err == nil {
		c.session.ctx = ctx
		err = action(c.session)
	}
	if !stop() {
		<-retired
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if c.ctx.Err() != nil {
		err = c.ctx.Err()
	}
	if err != nil {
		c.drop()
	}
	return err
}
