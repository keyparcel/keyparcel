package core

import (
	"context"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// Reuse the authenticated real SSH/SFTP fixture from the connection test.
func checkOpenSSHMasterLifecycle(t *testing.T, ctx context.Context, identity *Identity, config string, tcp, authenticated, subsystems *atomic.Int32) {
	t.Helper()
	args := sshArgs(Host{Identity: identity.Path}, config, false)
	quiet := func(cmd *exec.Cmd) { cmd.Env = append(os.Environ(), "SSH_ASKPASS_REQUIRE=never") }
	m, err := startOpenSSHMaster(ctx, "fixture", args, quiet, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	info, err := os.Stat(m.dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("master directory must be private: %v %v", info, err)
	}
	info, err = os.Lstat(m.socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("master socket missing: %v %v", info, err)
	}
	beforeTCP, beforeAuth, beforeSFTP := tcp.Load(), authenticated.Load(), subsystems.Load()
	// Keep the owner alive, but make mux reuse impossible. Even with a reachable
	// authenticated target, opening the channel must not establish a new TCP.
	if err = os.Remove(m.socket); err != nil {
		t.Fatal(err)
	}
	if client, closeTransport, err := m.openSFTP(); err == nil {
		closeTransport()
		client.Close()
		t.Fatal("missing private socket allowed a new connection")
	}
	if tcp.Load() != beforeTCP || authenticated.Load() != beforeAuth || subsystems.Load() != beforeSFTP {
		t.Fatalf("missing socket fell back: TCP/auth/SFTP %d/%d/%d -> %d/%d/%d", beforeTCP, beforeAuth, beforeSFTP, tcp.Load(), authenticated.Load(), subsystems.Load())
	}
	m.close()
	if _, err = os.Lstat(m.dir); !os.IsNotExist(err) {
		t.Fatalf("owner shutdown retained private directory: %v", err)
	}
	select {
	case <-m.done:
	default:
		t.Fatal("owner shutdown left SSH master running")
	}
	// Cancellation also removes the socket without relying on Session.Close.
	canceled, cancel := context.WithCancel(ctx)
	m, err = startOpenSSHMaster(canceled, "fixture", args, quiet, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer m.close()
	cancel()
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reap SSH master")
	}
	if _, err = os.Lstat(m.dir); !os.IsNotExist(err) {
		t.Fatalf("cancellation retained private directory: %v", err)
	}
	// If the owner exits unexpectedly, the existing channel fails, with no replay.
	m, err = startOpenSSHMaster(ctx, "fixture", args, quiet, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	client, closeTransport, err := m.openSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer closeTransport()
	beforeTCP = tcp.Load()
	if err = m.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-m.done
	if _, err = client.Stat("/"); err == nil {
		t.Fatal("dead owner left a usable channel")
	}
	if tcp.Load() != beforeTCP {
		t.Fatal("dead owner replayed channel on a new TCP")
	}
}

func TestOpenSSHMasterStartFailureCleansSocketDirectory(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("OpenSSH client unavailable")
	}
	root, err := os.MkdirTemp("", "kp-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	t.Setenv("TMPDIR", root)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Invalid local configuration fails before any remote connection is possible.
	if m, err := startOpenSSHMaster(ctx, "unused", []string{"-o", "InvalidKeyparcelOption=yes"}, func(*exec.Cmd) {}, nil); err == nil {
		m.close()
		t.Fatal("invalid SSH configuration unexpectedly started a master")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed startup leaked socket directory: %v %v", entries, err)
	}
}
