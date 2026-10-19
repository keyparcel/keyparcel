package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

func sshArgs(h Host, config string, interactive bool) []string {
	args := []string{}
	if config != "" {
		args = append(args, "-F", config)
	}
	if interactive {
		args = append(args, "-o", "BatchMode=no", "-o", "StrictHostKeyChecking=ask")
	} else {
		args = append(args, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no")
	}
	return append(args, "-i", h.Identity, "-o", "IdentitiesOnly=yes", "-o", "PreferredAuthentications=publickey", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no", "-o", "CertificateFile=none", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2", "-o", "ConnectTimeout=10")
}

func foregroundTerminal() *os.File {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	// A background job may have a controlling terminal but cannot answer a prompt.
	group, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	if err != nil || group != syscall.Getpgrp() {
		tty.Close()
		return nil
	}
	return tty
}
func Connect(ctx context.Context, target string, h Host, sshConfig string) (*Session, error) {
	return connect(ctx, target, h, sshConfig, false, "")
}

// ConnectWithAskpass lets a desktop caller supply a host-approval helper.
// OpenSSH still verifies and stores host trust; credential restrictions remain.
func ConnectWithAskpass(ctx context.Context, target string, h Host, sshConfig, askpass string) (*Session, error) {
	if askpass == "" {
		return nil, errors.New("an SSH approval helper is required")
	}
	return connect(ctx, target, h, sshConfig, false, askpass)
}

// Completion connections have no controlling terminal or credential prompts.
func ConnectForCompletion(ctx context.Context, target string, h Host, sshConfig string) (*Session, error) {
	return connect(ctx, target, h, sshConfig, true, "")
}
func connect(ctx context.Context, target string, h Host, sshConfig string, completion bool, askpass string) (*Session, error) {
	if err := ValidateTarget(target); err != nil {
		return nil, err
	}
	id, err := LoadIdentity(h.Identity)
	if err != nil {
		return nil, err
	}
	if ssh.FingerprintSHA256(id.Public) != h.Fingerprint {
		return nil, errors.New("selected identity fingerprint changed; run setup explicitly")
	}
	h.Identity = id.Path
	var tty *os.File
	if !completion && askpass == "" {
		tty = foregroundTerminal()
		if tty != nil {
			defer tty.Close()
		}
	}
	quiet := func(cmd *exec.Cmd) {
		cmd.Env = append(os.Environ(), "SSH_ASKPASS_REQUIRE=never")
		if askpass != "" {
			cmd.Env = append(os.Environ(), "SSH_ASKPASS="+askpass, "SSH_ASKPASS_REQUIRE=force", "KEYPARCEL_HOST_APPROVAL=1")
		}
		if tty == nil {
			// ProxyJump children must not inherit a terminal for background prompts.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			// Cancellation also closes approval/jump children of background sessions.
			cmd.Cancel = func() error {
				err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				if errors.Is(err, syscall.ESRCH) {
					return os.ErrProcessDone
				}
				return err
			}
		}
	}
	args := sshArgs(h, sshConfig, tty != nil || askpass != "")
	// Evaluate the same no-session configuration used for authentication, not
	// the later mux child's subsystem configuration.
	pre := exec.CommandContext(ctx, "ssh", append(append([]string{"-G"}, openSSHMasterArgs(args, "none")...), "-N", target)...)
	quiet(pre)
	pre.WaitDelay = 2 * time.Second
	var diag bytes.Buffer
	pre.Stderr = &diag
	effective, err := pre.Output()
	if err != nil {
		return nil, fmt.Errorf("SSH configuration: %w: %s", err, diag.String())
	}
	settings := map[string]string{}
	for _, line := range strings.Split(string(effective), "\n") {
		key, val, ok := strings.Cut(line, " ")
		if ok {
			settings[key] = strings.TrimSpace(val)
		}
	}
	home, _ := os.UserHomeDir()
	replacer := strings.NewReplacer("%%", "%", "%d", home, "%h", settings["hostname"], "%r", settings["user"], "%p", settings["port"])
	selected := false
	for _, line := range strings.Split(string(effective), "\n") {
		raw, ok := strings.CutPrefix(line, "identityfile ")
		if !ok || raw == "none" {
			continue
		}
		file := replacer.Replace(raw)
		if strings.Contains(file, "%") {
			return nil, fmt.Errorf("unsupported identity path token in %q; use a dedicated SSH alias", raw)
		}
		file, err = ResolvePath(file)
		if err != nil {
			return nil, err
		}
		if file == id.Path {
			selected = true
			continue
		}
		for _, candidate := range []string{file, file + ".pub"} {
			_, e := os.Stat(candidate)
			if e == nil {
				return nil, fmt.Errorf("conflicting SSH identity %q; use a dedicated SSH alias with only the selected IdentityFile", file)
			}
			if !errors.Is(e, os.ErrNotExist) {
				return nil, e
			}
		}
	}
	if !selected {
		return nil, errors.New("SSH effective configuration does not include selected identity")
	}
	master, err := startOpenSSHMaster(ctx, target, args, quiet, tty)
	if err != nil {
		return nil, err
	}
	s := &Session{Identity: id, ctx: ctx}
	s.openSFTP = func() (*sftpChannel, error) {
		client, closeChannel, err := master.openSFTP()
		if err != nil {
			return nil, err
		}
		return newSFTPChannel(client, closeChannel), nil
	}
	s.waitTransport = func() error { <-master.done; return master.err }
	s.channel, err = s.openChannel()
	if err != nil {
		master.close()
		return nil, err
	}
	s.Client = s.channel.client
	s.closeTransport = func() error {
		// Retire the owner first, so a blocked channel cannot hold shutdown hostage.
		master.close()
		return nil
	}
	cwd, err := s.Client.Getwd()
	if err != nil {
		s.Close()
		return nil, err
	}
	s.Root = remotePath(cwd, h.Root)
	s.DiscoveryPath = remotePath(cwd, h.Discovery)
	return s, nil
}
