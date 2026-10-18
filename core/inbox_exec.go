package core

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

var errInboxCommandOutput = errors.New("inbox command output limit")

// Discard stderr without retaining server-controlled text. Stop the channel if
// it floods diagnostics while stdout is being parsed.
type inboxStderr struct {
	bytes  atomic.Int64
	cancel func()
}

func (w *inboxStderr) Write(p []byte) (int, error) {
	if w.bytes.Add(int64(len(p))) > 64*1024 {
		w.cancel()
		return 0, errInboxCommandOutput
	}
	return len(p), nil
}

func (m *opensshMaster) runInboxCommand(ctx context.Context, command string, consume func(io.Reader) error) error {
	child, cancel := context.WithCancel(m.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	cmd := exec.CommandContext(child, "ssh", append(m.channelArgs(), m.target, command)...)
	m.quiet(cmd)
	cmd.WaitDelay = 2 * time.Second
	stderr := &inboxStderr{cancel: cancel}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	readErr := consume(out)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	select {
	case <-m.done:
		return io.ErrClosedPipe
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// OpenSSH reserves 255 for client/transport errors, not a negotiated
	// unsupported command. Do not turn a lost mux into SFTP fallback.
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) && exit.ExitCode() == 255 {
		return io.ErrClosedPipe
	}
	if stderr.bytes.Load() > 64*1024 {
		return errInboxCommandOutput
	}
	if readErr != nil {
		return readErr
	}
	return waitErr
}

func runEmbeddedInbox(ctx context.Context, owner *ssh.Client, ownerDone <-chan struct{}, command string, consume func(io.Reader) error) error {
	type opened struct {
		session *ssh.Session
		err     error
	}
	ready := make(chan opened)
	// NewSession has no context API. Close a late accepted channel rather than
	// exposing it to a request that was canceled while opening.
	go func() {
		session, err := owner.NewSession()
		select {
		case ready <- opened{session, err}:
		case <-ctx.Done():
			if session != nil {
				session.Close()
			}
		}
	}()
	var session *ssh.Session
	select {
	case result := <-ready:
		if result.err != nil {
			return result.err
		}
		session = result.session
	case <-ctx.Done():
		return ctx.Err()
	}
	defer session.Close()
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(child, func() { session.Close() })
	defer stop()
	stderr := &inboxStderr{cancel: cancel}
	session.Stderr = stderr
	out, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	started := make(chan error, 1)
	go func() { started <- session.Start(command) }()
	select {
	case err = <-started:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()
	readErr := consume(out)
	if readErr != nil {
		session.Close()
	}
	var waitErr error
	select {
	case waitErr = <-wait:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ownerDone:
		return io.ErrClosedPipe
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if stderr.bytes.Load() > 64*1024 {
		return errInboxCommandOutput
	}
	if readErr != nil {
		return readErr
	}
	return waitErr
}
