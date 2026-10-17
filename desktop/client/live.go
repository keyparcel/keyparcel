package main

import (
	"context"
	"keyparcel/core"
	"sync"
	"sync/atomic"
)

type ownedConnection struct {
	connection *core.Connection
	target     string
	host       core.Host
}

// Owns the reusable transport; its lease covers both polls and user operations.
type sessionOwner struct {
	mu          sync.Mutex
	ctx         context.Context
	connection  *core.Connection
	target      string
	host        core.Host
	interactive bool
	current     atomic.Pointer[ownedConnection]
}

func (o *sessionOwner) request(ctx context.Context, p profile, sshConfig, helper string, interactive bool, action func(*core.Session, profile) (any, error)) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.connection == nil || o.target != p.Target || o.host != p.Host {
		if o.connection != nil {
			o.connection.Close()
		}
		o.target, o.host = p.Target, p.Host
		o.connection = core.NewConnection(o.ctx, func(ctx context.Context) (*core.Session, error) {
			if o.interactive {
				return core.ConnectWithAskpass(ctx, p.Target, p.Host, sshConfig, helper)
			}
			// Background reconnects must never open repeated approval/credential dialogs.
			return core.ConnectForCompletion(ctx, p.Target, p.Host, sshConfig)
		})
	}
	o.current.Store(&ownedConnection{o.connection, p.Target, p.Host})
	o.interactive = interactive
	var value any
	err := o.connection.Do(ctx, func(s *core.Session) error { var err error; value, err = action(s, p); return err })
	return value, err
}

func (o *sessionOwner) state(target string, host core.Host) string {
	c := o.current.Load()
	if c == nil || c.target != target || c.host != host {
		return "Not connected"
	}
	return c.connection.State()
}
