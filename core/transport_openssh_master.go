package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
)

// Each Session owns its socket and authenticated master; user-configured masters
// cannot establish the selected identity's trust or outlive this owner.
type opensshMaster struct {
	ctx    context.Context
	cancel context.CancelFunc
	target string
	args   []string
	socket string
	dir    string
	quiet  func(*exec.Cmd)
	cmd    *exec.Cmd
	stderr bytes.Buffer
	done   chan struct{}
	err    error
	once   sync.Once
}

func openSSHMasterArgs(args []string, socket string) []string {
	return append([]string{"-o", "ControlMaster=yes", "-S", strings.ReplaceAll(socket, "%", "%%"), "-o", "ControlPersist=no", "-o", "ForkAfterAuthentication=no", "-o", "ClearAllForwardings=yes", "-o", "RequestTTY=no", "-o", "RemoteCommand=none"}, args...)
}

func startOpenSSHMaster(ctx context.Context, target string, args []string, quiet func(*exec.Cmd), tty *os.File) (*opensshMaster, error) {
	dir, err := os.MkdirTemp("", "keyparcel-ssh-")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &opensshMaster{ctx: ctx, cancel: cancel, target: target, args: args, socket: filepath.Join(dir, "mux"), dir: dir, quiet: quiet, done: make(chan struct{})}
	// Command-line first values override both sshArgs and the user's config.
	m.cmd = exec.CommandContext(ctx, "ssh", append(openSSHMasterArgs(args, m.socket), "-N", target)...)
	quiet(m.cmd)
	m.cmd.WaitDelay = 2 * time.Second
	m.cmd.Stderr = &m.stderr
	if tty != nil {
		m.cmd.Stderr = tty
	}
	if err = m.cmd.Start(); err != nil {
		cancel()
		os.RemoveAll(dir)
		return nil, err
	}
	go func() {
		m.err = m.cmd.Wait()
		os.RemoveAll(m.dir)
		close(m.done)
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			m.close()
			return nil, fmt.Errorf("SSH master connection: %v: %s", m.err, strings.TrimSpace(m.stderr.String()))
		case <-ctx.Done():
			m.close()
			return nil, ctx.Err()
		case <-ticker.C:
			if _, err := os.Lstat(m.socket); err != nil {
				continue
			}
			check := exec.CommandContext(ctx, "ssh", append(m.channelArgs(), "-O", "check", target)...)
			quiet(check)
			check.WaitDelay = 2 * time.Second
			if check.Run() == nil {
				return m, nil
			}
		}
	}
}

func (m *opensshMaster) channelArgs() []string {
	// OpenSSH otherwise falls back to a fresh network connection when mux reuse
	// fails. A failing proxy forbids that, including inherited ProxyJump routes.
	return append([]string{"-o", "ControlMaster=no", "-S", strings.ReplaceAll(m.socket, "%", "%%"), "-o", "ControlPersist=no", "-o", "ProxyCommand=false", "-o", "ProxyJump=none", "-o", "ForkAfterAuthentication=no", "-o", "ClearAllForwardings=yes", "-o", "RequestTTY=no", "-o", "RemoteCommand=none"}, m.args...)
}

func (m *opensshMaster) close() {
	m.once.Do(func() {
		m.cancel()
		<-m.done
		os.RemoveAll(m.dir)
	})
}

func (m *opensshMaster) openSFTP() (*sftp.Client, func() error, error) {
	cmd := exec.CommandContext(m.ctx, "ssh", append(m.channelArgs(), "-s", m.target, "sftp")...)
	m.quiet(cmd)
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		return nil, nil, err
	}
	client, err := sftp.NewClientPipe(out, in, sftp.UseConcurrentReads(false), sftp.UseConcurrentWrites(false))
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, nil, fmt.Errorf("SSH/SFTP connection: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	closeTransport := func() error {
		closeErr := client.Close()
		err := cmd.Wait()
		if closeErr != nil {
			return closeErr
		}
		if err != nil {
			return fmt.Errorf("SSH: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	return client, closeTransport, nil
}
