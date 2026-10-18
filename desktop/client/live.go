package main

import (
	"context"
	"errors"
	"keyparcel/core"
	"maps"
	"sync"
	"sync/atomic"
)

type ownedConnection struct {
	connection *core.Connection
	target     string
	host       core.Host
}

// Owns transport selection; Connection owns admission and shared cancellation.
type sessionOwner struct {
	mu          sync.Mutex
	ctx         context.Context
	connection  *core.Connection
	target      string
	host        core.Host
	current     atomic.Pointer[ownedConnection]
	generation  uint64
	diagnostics *diagnostics
}

type interactiveDial struct{}

func (o *sessionOwner) stamp() uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.generation
}

func (o *sessionOwner) invalidate(target string, host core.Host) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.generation++
	if o.connection != nil && (o.target != target || o.host != host) {
		o.connection.Close()
		o.connection = nil
		o.current.Store(nil)
	}
	o.target, o.host = target, host
}

func (o *sessionOwner) request(ctx context.Context, p profile, sshConfig, helper string, interactive, background bool, generation uint64, action func(*core.Session, profile) (any, error)) (any, error) {
	o.mu.Lock()
	if generation != o.generation {
		o.mu.Unlock()
		return nil, errors.New("server or approvals changed; try the operation again")
	}
	if o.target != p.Target || o.host != p.Host {
		o.generation++
	}
	if o.connection == nil || o.target != p.Target || o.host != p.Host {
		if o.connection != nil {
			o.connection.Close()
		}
		o.target, o.host = p.Target, p.Host
		report := &dialDiagnostics{diagnostics: o.diagnostics}
		o.connection = core.NewConnection(o.ctx, func(ctx context.Context) (*core.Session, error) {
			return report.dial(ctx, func() (*core.Session, error) {
				if ctx.Value(interactiveDial{}) == true {
					return core.ConnectWithAskpass(ctx, p.Target, p.Host, sshConfig, helper)
				}
				// Background reconnects must never open repeated approval/credential dialogs.
				return core.ConnectForCompletion(ctx, p.Target, p.Host, sshConfig)
			})
		})
	}
	o.current.Store(&ownedConnection{o.connection, p.Target, p.Host})
	connection := o.connection
	o.mu.Unlock()
	// Profile maps belong to this operation, never to a mutable GUI selection.
	config := *p.Config
	config.Peers, config.Hosts = maps.Clone(p.Config.Peers), maps.Clone(p.Config.Hosts)
	p.Config = &config
	ctx = context.WithValue(ctx, interactiveDial{}, interactive)
	run := connection.Do
	if background {
		run = connection.Poll
	}
	var value any
	err := run(ctx, func(s *core.Session) error { var err error; value, err = action(s, p); return err })
	return value, err
}

func (o *sessionOwner) state(target string, host core.Host) string {
	c := o.current.Load()
	if c == nil || c.target != target || c.host != host {
		return "Not connected"
	}
	return c.connection.State()
}
