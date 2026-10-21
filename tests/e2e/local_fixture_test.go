//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
	"keyparcel/tests/e2e/connection"
)

func localFixtures() bool { return os.Getenv("KEYPARCEL_E2E_FIXTURE") == "local" }

// Docker stays the default; native jobs build the same existing CLI test entrypoint.
func runLocalTests(m *testing.M) int {
	root := os.Getenv("KEYPARCEL_E2E_LOCAL_ROOT")
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || os.Getuid() == 0 {
		fmt.Fprintln(os.Stderr, "Local E2E requires a private 0700 fixture root and a non-root user")
		return 1
	}
	if os.Getenv("KEYPARCEL_INSTALLED_IMAGE") != "" {
		fmt.Fprintln(os.Stderr, "Local and installed-container fixtures cannot be combined")
		return 1
	}
	if installed := os.Getenv("KEYPARCEL_E2E_INSTALLED_CLI"); installed != "" {
		info, err := os.Lstat(installed)
		if installed != "/usr/local/bin/keyparcel" || err != nil || !info.Mode().IsRegular() {
			fmt.Fprintln(os.Stderr, "Installed native E2E requires /usr/local/bin/keyparcel, with no symlink or fallback")
			return 1
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 0 || info.Mode().Perm() != 0755 {
			fmt.Fprintln(os.Stderr, "Installed native CLI must be root-owned with mode0755")
			return 1
		}
		binaryPath = installed
		fmt.Fprintln(os.Stderr, "Using installed native CLI without rebuilding:", binaryPath)
		return m.Run()
	}
	bin, err := os.MkdirTemp(root, "bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(bin)
	binaryPath = filepath.Join(bin, "keyparcel")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, diag, err := command(ctx, nil, "go", "build", "-tags=e2e", "-o", binaryPath, "keyparcel/cmd/keyparcel")
	// Resolve the CLI from this module, even though the test cwd is tests/e2e.
	if err != nil {
		fmt.Fprintf(os.Stderr, "Native CLI build: %v\n%s%s", err, out, diag)
		return 1
	}
	return m.Run()
}

type localOperation struct {
	cmd  *exec.Cmd
	done chan struct{}
}
type localWorld struct {
	home, storage, authorized, log, socketDir string
	daemon                                    *exec.Cmd
	daemonDone                                chan struct{}
	daemonErr                                 error
	operations                                []localOperation
}

func (w *world) storageRoot() string {
	if w.local != nil {
		return w.local.storage
	}
	return "/home/parcel/.local/share/keyparcel"
}
func (w *world) authorizedKeys() string {
	if w.local != nil {
		return w.local.authorized
	}
	return "/home/parcel/.ssh/authorized_keys"
}
func (w *world) localEnv(c client) []string {
	// Do not pass a host agent, askpass, SSH environment or shell startup file.
	return []string{"PATH=" + filepath.Dir(binaryPath) + ":" + os.Getenv("PATH"), "HOME=" + c.dir,
		"XDG_CONFIG_HOME=" + c.dir, "XDG_DATA_HOME=" + c.dir, "XDG_CACHE_HOME=" + c.dir,
		"TMPDIR=" + w.local.socketDir, "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8",
		"ENV=/dev/null", "BASH_ENV=/dev/null", "ZDOTDIR=" + c.dir,
		"KEYPARCEL_TEST_TRANSPORT=" + w.mode}
}
func (w *world) localExec(c client, input []byte, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(w.ctx, args[0], args[1:]...)
	cmd.Env = w.localEnv(c)
	cmd.Dir = c.dir
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = bytes.NewReader(input)
	var out, diag bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diag
	err := cmd.Run()
	return out.Bytes(), diag.Bytes(), err
}
func (w *world) localPath(path string) {
	w.t.Helper()
	if !filepath.IsAbs(path) || !strings.HasPrefix(filepath.Clean(path), w.dir+string(os.PathSeparator)) {
		w.t.Fatalf("server path outside owned fixture: %q", path)
	}
}
func (w *world) localAdmin(args ...string) []byte {
	w.t.Helper()
	// Selected portable scenarios need only these filesystem fixture operations.
	if len(args) == 0 || (args[0] != "cat" && args[0] != "chmod" && args[0] != "find") {
		w.t.Fatalf("Docker-only server operation: %v", args)
	}
	for _, arg := range args[1:] {
		if filepath.IsAbs(arg) {
			w.localPath(arg)
		}
	}
	out, diag, err := w.localExec(client{dir: w.local.home}, nil, args...)
	if err != nil {
		w.t.Fatalf("local server fixture %v: %v %s", args, err, diag)
	}
	return out
}
func (w *world) localStart(c client, args ...string) operation {
	w.t.Helper()
	path := filepath.Join(c.dir, fmt.Sprintf("operation-%d", time.Now().UnixNano()))
	log, err := os.Create(path + ".log")
	if err != nil {
		w.t.Fatal(err)
	}
	args = append(args, "--config", c.config, "--ssh-config", c.sshConfig)
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = w.localEnv(c)
	cmd.Dir = c.dir
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		log.Close()
		w.t.Fatal(err)
	}
	done := make(chan struct{})
	w.local.operations = append(w.local.operations, localOperation{cmd, done})
	if err = os.WriteFile(path+".pid", []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		w.t.Fatal(err)
	}
	go func() {
		status := 0
		if err := cmd.Wait(); err != nil {
			status = 1
			if exit, ok := err.(*exec.ExitError); ok {
				status = exit.ExitCode()
			}
		}
		log.Close()
		_ = os.WriteFile(path+".status", []byte(strconv.Itoa(status)), 0600)
		close(done)
	}()
	return operation{c, path}
}

func makeLocalWorld(t *testing.T, clipboard bool, keyType, mode string, options worldOptions) *world {
	t.Helper()
	if clipboard || options.netAdmin || options.publishSSH || options.serverImage != "" || options.clientImage != "" {
		t.Fatal("Docker-only world options requested for native fixture")
	}
	dir, err := os.MkdirTemp(os.Getenv("KEYPARCEL_E2E_LOCAL_ROOT"), "world-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), options.timeout)
	w := &world{t: t, dir: dir, ctx: ctx, cancel: cancel, mode: mode, local: &localWorld{home: filepath.Join(dir, "server"), log: filepath.Join(dir, "sshd.log")}}
	w.local.storage = filepath.Join(w.local.home, "storage")
	w.local.authorized = filepath.Join(w.local.home, "authorized_keys")
	t.Cleanup(func() { w.closeLocal() })
	w.local.socketDir, err = os.MkdirTemp("", "kp-sock-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(w.local.home, 0700); err != nil {
		t.Fatal(err)
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	w.port = strconv.Itoa(port)
	hostKey := filepath.Join(w.local.home, "host_key")
	w.clientOK(client{dir: w.local.home}, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey)
	pub, err := os.ReadFile(hostKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(pub))
	known := fmt.Sprintf("[127.0.0.1]:%d %s %s\n127.0.0.1 %s %s\n", port, fields[0], fields[1], fields[0], fields[1])
	makeClient := func(name, keyType string) client {
		c := client{dir: filepath.Join(dir, name)}
		c.key = filepath.Join(c.dir, "id_"+keyType)
		c.config = filepath.Join(c.dir, "config.json")
		c.sshConfig = filepath.Join(c.dir, "ssh_config")
		if err := os.MkdirAll(c.dir, 0700); err != nil {
			t.Fatal(err)
		}
		w.clientOK(c, nil, "ssh-keygen", "-q", "-t", keyType, "-b", "2048", "-N", "", "-C", name, "-f", c.key)
		identity, err := core.LoadIdentity(c.key)
		if err != nil {
			t.Fatal(err)
		}
		c.fingerprint = ssh.FingerprintSHA256(identity.Public)
		knownPath := filepath.Join(c.dir, "known_hosts")
		if err = os.WriteFile(knownPath, []byte(known), 0600); err != nil {
			t.Fatal(err)
		}
		cfg := fmt.Sprintf("Host parcel-test\n HostName 127.0.0.1\n Port %d\n User %s\n IdentityFile %s\n IdentityAgent none\n UserKnownHostsFile %s\n GlobalKnownHostsFile /dev/null\n StrictHostKeyChecking yes\n", port, account.Username, strconv.Quote(c.key), strconv.Quote(knownPath))
		if err = os.WriteFile(c.sshConfig, []byte(cfg), 0600); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(map[string]connection.Settings{"parcel-test": {Address: "127.0.0.1", Port: port, User: account.Username, KnownHosts: knownPath}})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(c.sshConfig+".embedded.json", data, 0600); err != nil {
			t.Fatal(err)
		}
		w.clients = append(w.clients, c)
		return c
	}
	w.alice = makeClient("alice", "ed25519")
	w.bob = makeClient("bob", keyType)
	w.carol = makeClient("carol", "ed25519")
	var authorized []byte
	for _, c := range w.clients {
		data, err := os.ReadFile(c.key + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		authorized = append(authorized, data...)
	}
	if err = os.WriteFile(w.local.authorized, authorized, 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`ListenAddress 127.0.0.1
Port %d
HostKey %s
PidFile %s
AuthorizedKeysFile %s
AllowUsers %s
PermitRootLogin no
UsePAM no
PasswordAuthentication no
KbdInteractiveAuthentication no
AuthenticationMethods publickey
PubkeyAuthentication yes
StrictModes yes
DisableForwarding yes
PermitUserRC no
PermitUserEnvironment no
UseDNS no
LogLevel VERBOSE
SetEnv %s %s BASH_ENV=/dev/null ENV=/dev/null
Subsystem sftp internal-sftp -e -l VERBOSE
`, port, strconv.Quote(hostKey), strconv.Quote(filepath.Join(dir, "sshd.pid")), strconv.Quote(w.local.authorized), account.Username, strconv.Quote("HOME="+w.local.home), strconv.Quote("ZDOTDIR="+w.local.home))
	configPath := filepath.Join(dir, "sshd_config")
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	sshd := "/usr/sbin/sshd"
	w.clientOK(client{dir: w.local.home}, nil, sshd, "-t", "-f", configPath)
	if err = os.WriteFile(w.local.log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	daemon := exec.Command(sshd, "-D", "-e", "-f", configPath)
	daemon.Env = w.localEnv(client{dir: w.local.home})
	log, err := os.OpenFile(w.local.log, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	daemon.Stdout = log
	daemon.Stderr = log
	if err = daemon.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	w.local.daemon = daemon
	w.local.daemonDone = make(chan struct{})
	go func() { w.local.daemonErr = daemon.Wait(); log.Close(); close(w.local.daemonDone) }()
	for _, c := range w.clients {
		w.ok(c, nil, "setup", "--host", "parcel-test", "--identity", c.key, "--root", w.local.storage, "--discovery", w.local.authorized, "--access-method", "sftp", "--default")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-w.local.daemonDone:
			t.Fatalf("Owned unprivileged sshd exited: %v\n%s", w.local.daemonErr, w.logs())
		default:
		}
		_, diag, err := w.run(w.alice, nil, "devices", "--json")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Native authenticated SFTP readiness: %v %s\n%s", err, diag, w.logs())
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("Owned sshd user=%s port=%d root=%s", account.Username, port, dir)
	return w
}

// Kill only descendants of fixture-owned PIDs, never all processes of a user/name.
func stopLocalTree(pid int) {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err == nil {
		for _, field := range strings.Fields(string(out)) {
			child, e := strconv.Atoi(field)
			if e == nil && child > 1 {
				stopLocalTree(child)
			}
		}
	}
	if pid > 1 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
func (w *world) closeLocal() {
	for _, op := range w.local.operations {
		select {
		case <-op.done:
			continue
		default:
		}
		_ = op.cmd.Process.Signal(os.Interrupt)
		select {
		case <-op.done:
		case <-time.After(3 * time.Second):
			stopLocalTree(op.cmd.Process.Pid)
			select {
			case <-op.done:
			case <-time.After(3 * time.Second):
				w.t.Error("owned client did not reap")
			}
		}
	}
	if w.local.daemon != nil {
		select {
		case <-w.local.daemonDone:
		default:
			stopLocalTree(w.local.daemon.Process.Pid)
		}
		select {
		case <-w.local.daemonDone:
		case <-time.After(3 * time.Second):
			w.t.Error("owned sshd did not reap")
		}
	}
	w.cancel()
	if w.t.Failed() {
		dest := filepath.Join(repo, "test-artifacts", strings.ReplaceAll(w.t.Name(), "/", "_"), filepath.Base(w.dir))
		_ = os.MkdirAll(dest, 0700)
		_ = os.WriteFile(filepath.Join(dest, "transcript.txt"), w.transcript.Bytes(), 0600)
		if data, err := os.ReadFile(w.local.log); err == nil {
			_ = os.WriteFile(filepath.Join(dest, "server.log"), data, 0600)
		}
		w.t.Log("Failure logs (no fixture keys):", dest)
	}
	// Permission-rejection scenarios may intentionally leave owned directories read-only.
	_ = filepath.WalkDir(w.dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			return os.Chmod(path, 0700)
		}
		return err
	})
	if err := os.RemoveAll(w.dir); err != nil {
		w.t.Error("owned fixture cleanup:", err)
	}
	if w.local.socketDir != "" {
		if err := os.RemoveAll(w.local.socketDir); err != nil {
			w.t.Error(err)
		}
	}
}
