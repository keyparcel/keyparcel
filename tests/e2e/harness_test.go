//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
	"keyparcel/tests/e2e/connection"
)

var docker []string
var repo string

var binaryPath = "keyparcel"

const agePath = "age"

const image = "keyparcel-test-sshd:local"

var clientImage = "keyparcel-test-client:local"

const clipboardImage = "keyparcel-test-clipboard:local"

func command(ctx context.Context, input []byte, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = bytes.NewReader(input)
	var out, errout bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errout
	err := cmd.Run()
	return out.Bytes(), errout.Bytes(), err
}
func container(ctx context.Context, input []byte, args ...string) ([]byte, []byte, error) {
	if localFixtures() {
		return nil, nil, fmt.Errorf("Docker-only operation in local fixture: %v", args)
	}
	return command(ctx, input, docker[0], append(docker[1:], args...)...)
}
func TestMain(m *testing.M) {
	flag.Parse()
	// Discovery lists the compiled tests without building Docker fixtures.
	if flag.Lookup("test.list").Value.String() != "" {
		os.Exit(m.Run())
	}
	// Keep the runner isolated too; client containers receive no host agent environment.
	os.Unsetenv("SSH_AUTH_SOCK")
	os.Unsetenv("SSH_AGENT_PID")
	var err error
	repo, err = filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	if fixture := os.Getenv("KEYPARCEL_E2E_FIXTURE"); fixture != "" {
		if fixture != "local" {
			fmt.Fprintln(os.Stderr, "Unknown E2E fixture:", fixture)
			os.Exit(1)
		}
		os.Exit(runLocalTests(m))
	}
	docker = strings.Fields(os.Getenv("KEYPARCEL_DOCKER"))
	if len(docker) == 0 {
		docker = []string{"docker"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	must := func(name string, args ...string) {
		out, diag, e := command(ctx, nil, name, args...)
		if e != nil {
			fmt.Fprintf(os.Stderr, "E2E prerequisite failed: %s %v: %v\n%s%s\n", name, args, e, out, diag)
			os.Exit(1)
		}
	}
	if installed := os.Getenv("KEYPARCEL_INSTALLED_IMAGE"); installed != "" {
		clientImage, binaryPath = installed, "/usr/bin/keyparcel"
		must(docker[0], append(docker[1:], "image", "inspect", installed)...)
		fmt.Fprintln(os.Stderr, "Using installed /usr/bin/keyparcel from", installed)
	} else {
		fmt.Fprintln(os.Stderr, "Preparing E2E binaries and Docker images (reusing cached layers)...")
		must("bash", filepath.Join(repo, "tests/build-images.sh"))
	}
	code := m.Run()
	os.Exit(code)
}

type client struct {
	key, config, sshConfig string
	id, dir                string
	fingerprint            string
}
type world struct {
	t                      *testing.T
	dir, id, port, network string
	clients                []client
	alice, bob, carol      client
	transcript             bytes.Buffer
	ctx                    context.Context
	mode                   string
	local                  *localWorld
	netAdmin               bool
	cancel                 context.CancelFunc
}

// Each transport gets its own fixture; assertions remain in a single scenario body.
func runTransports(t *testing.T, scenario func(*testing.T, string)) {
	t.Helper()
	t.Parallel()
	modes := []string{"openssh", "embedded"}
	if os.Getenv("KEYPARCEL_INSTALLED_IMAGE") != "" ||
		(localFixtures() && os.Getenv("KEYPARCEL_E2E_INSTALLED_CLI") != "") {
		modes = []string{"openssh"} // Production CLI uses OpenSSH; embedded coverage remains in source tests.
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) { scenario(t, mode) })
	}
}

func newWorld(t *testing.T, mode string) *world { return makeWorld(t, false, "ed25519", mode) }
func newClipboardWorld(t *testing.T) *world     { return makeWorld(t, true, "ed25519", "openssh") }
func makeWorld(t *testing.T, clipboard bool, bobKeyType, mode string) *world {
	t.Helper()
	return makeWorldWithOptions(t, clipboard, bobKeyType, mode, worldOptions{timeout: 90 * time.Second})
}

type worldOptions struct {
	timeout     time.Duration
	publishSSH  bool
	netAdmin    bool
	serverImage string
	clientImage string
}

func makeWorldWithOptions(t *testing.T, clipboard bool, bobKeyType, mode string, options worldOptions) *world {
	t.Helper()
	if localFixtures() {
		return makeLocalWorld(t, clipboard, bobKeyType, mode, options)
	}
	dir, err := os.MkdirTemp("", "keyparcel-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), options.timeout)
	w := &world{t: t, dir: dir, ctx: ctx, cancel: cancel, mode: mode, netAdmin: options.netAdmin}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		logs, logErr, _ := container(cleanupCtx, nil, "logs", w.id)
		logs = append(logs, logErr...)
		sftpLogs, _, _ := container(cleanupCtx, nil, "exec", w.id, "cat", "/var/log/sftp.log")
		logs = append(logs, sftpLogs...)
		keep := t.Failed() && os.Getenv("KEYPARCEL_KEEP_FAILED") == "1"
		if t.Failed() {
			dest := filepath.Join(repo, "test-artifacts", strings.ReplaceAll(t.Name(), "/", "_"), filepath.Base(w.dir))
			os.MkdirAll(dest, 0700)
			os.WriteFile(filepath.Join(dir, "transcript.txt"), w.transcript.Bytes(), 0600)
			os.WriteFile(filepath.Join(dir, "server.log"), logs, 0600)
			data, _, e := container(cleanupCtx, nil, "exec", w.id, "tar", "-czf", "-", "-C", "/home/parcel", ".local")
			if e == nil {
				os.WriteFile(filepath.Join(dir, "server-data.tar.gz"), data, 0600)
			}
			for _, c := range w.clients {
				out, diag, _ := container(cleanupCtx, nil, "logs", c.id)
				os.WriteFile(filepath.Join(c.dir, "container.log"), append(out, diag...), 0600)
			}
			if e := copyTree(dir, dest); e != nil {
				t.Log("artifact capture:", e)
			}
			t.Log("Failure artifacts:", dest)
			if keep {
				t.Logf("Kept server %s and network %s", w.id, w.network)
				for _, c := range w.clients {
					t.Logf("Inspect client: %s exec -it %s sh", strings.Join(docker, " "), c.id)
				}
				t.Log("Retained fixture directory:", dir)
			}
		}
		if !keep {
			for _, c := range w.clients {
				container(cleanupCtx, nil, "rm", "-f", c.id)
			}
			if w.id != "" {
				container(cleanupCtx, nil, "rm", "-f", w.id)
			}
			if w.network != "" {
				container(cleanupCtx, nil, "network", "rm", w.network)
			}
		}
		cancel()
		if !keep {
			os.RemoveAll(dir)
		}
	})
	w.network = filepath.Base(dir)
	networkArgs := []string{"network", "create"}
	// Internal-only Docker networks do not expose the loopback port Android needs.
	if !options.publishSSH {
		networkArgs = append(networkArgs, "--internal")
	}
	out, diag, err := container(ctx, nil, append(networkArgs, w.network)...)
	if err != nil {
		t.Fatalf("create network: %v %s", err, diag)
	}
	serverArgs := []string{"run", "-d", "--network", w.network, "--network-alias", "inbox"}
	if options.netAdmin {
		serverArgs = append(serverArgs, "--cap-add", "NET_ADMIN")
	}
	if options.publishSSH {
		serverArgs = append(serverArgs, "-p", "127.0.0.1::22")
	}
	serverImage := image
	if options.serverImage != "" {
		serverImage = options.serverImage
	}
	out, diag, err = container(ctx, nil, append(serverArgs, serverImage)...)
	if err != nil {
		t.Fatalf("start SSH container: %v %s", err, diag)
	}
	w.id = strings.TrimSpace(string(out))
	w.port = "22"
	selectedClientImage := clientImage
	if options.clientImage != "" {
		selectedClientImage = options.clientImage
	}
	w.alice = w.makeClient("alice", selectedClientImage, "ed25519")
	bobImage := selectedClientImage
	if clipboard {
		bobImage = clipboardImage
	}
	w.bob = w.makeClient("bob", bobImage, bobKeyType)
	w.carol = w.makeClient("carol", selectedClientImage, "ed25519")
	authorized := []byte{}
	for _, c := range []client{w.alice, w.bob, w.carol} {
		b, e := os.ReadFile(c.key + ".pub")
		if e != nil {
			t.Fatal(e)
		}
		authorized = append(authorized, b...)
	}
	w.put("/home/parcel/.ssh/authorized_keys", authorized)
	w.admin("chown", "parcel:parcel", "/home/parcel/.ssh/authorized_keys")
	w.admin("chmod", "600", "/home/parcel/.ssh/authorized_keys")
	var hostKey []byte
	deadline := time.Now().Add(10 * time.Second)
	for {
		hostKey, _, err = container(ctx, nil, "exec", w.id, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host key readiness", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	fields := strings.Fields(string(hostKey))
	known := fmt.Sprintf("inbox %s %s\n", fields[0], fields[1])
	for _, c := range w.clients {
		if err = os.WriteFile(filepath.Join(c.dir, "known_hosts"), []byte(known), 0600); err != nil {
			t.Fatal(err)
		}
		w.ok(c, nil, "setup", "--host", "parcel-test", "--identity", c.key, "--default")
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		_, _, err = w.run(w.alice, nil, "devices", "--json")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authenticated SFTP readiness", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return w
}
func (w *world) makeClient(name, imageName, keyType string) client {
	w.t.Helper()
	dir := filepath.Join(w.dir, name)
	os.MkdirAll(dir, 0700)
	c := client{dir: dir, key: filepath.Join(dir, "id_"+keyType), config: filepath.Join(dir, "config.json"), sshConfig: filepath.Join(dir, "ssh_config")}
	args := []string{"run", "-d", "--init"}
	if w.netAdmin {
		args = append(args, "--cap-add", "NET_ADMIN")
	}
	out, diag, err := container(w.ctx, nil, append(args, "--network", w.network, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--env", "KEYPARCEL_TEST_TRANSPORT="+w.mode, "--env", "HOME="+dir, "--workdir", dir, "--mount", "type=bind,src="+dir+",dst="+dir, imageName)...)
	if err != nil {
		w.t.Fatalf("start client: %v %s", err, diag)
	}
	c.id = strings.TrimSpace(string(out))
	w.clients = append(w.clients, c)
	out, diag, err = w.exec(c, nil, "ssh-keygen", "-q", "-t", keyType, "-b", "2048", "-N", "", "-C", name, "-f", c.key)
	if err != nil {
		w.t.Fatalf("fixture key: %v %s%s", err, out, diag)
	}
	id, err := core.LoadIdentity(c.key)
	if err != nil {
		w.t.Fatal(err)
	}
	c.fingerprint = ssh.FingerprintSHA256(id.Public)
	cfg := fmt.Sprintf("Host parcel-test\n HostName inbox\n Port %s\n User parcel\n IdentityFile %s\n IdentityAgent none\n UserKnownHostsFile %s\n GlobalKnownHostsFile /dev/null\n StrictHostKeyChecking yes\n", w.port, c.key, filepath.Join(c.dir, "known_hosts"))
	if err = os.WriteFile(c.sshConfig, []byte(cfg), 0600); err != nil {
		w.t.Fatal(err)
	}
	settings := map[string]connection.Settings{
		"parcel-test": {Address: "inbox", Port: 22, User: "parcel", KnownHosts: filepath.Join(c.dir, "known_hosts")},
		"second":      {Address: "inbox", Port: 22, User: "parcel", KnownHosts: filepath.Join(c.dir, "known_hosts")},
	}
	data, err := json.Marshal(settings)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(c.sshConfig+".embedded.json", data, 0600); err != nil {
		w.t.Fatal(err)
	}
	return c
}
func (w *world) run(c client, input []byte, args ...string) ([]byte, []byte, error) {
	w.t.Helper()
	args = append(args, "--config", c.config, "--ssh-config", c.sshConfig)
	fmt.Fprintf(&w.transcript, "$ keyparcel %s\n", strings.Join(args, " "))
	out, diag, err := w.exec(c, input, append([]string{binaryPath}, args...)...)
	fmt.Fprintf(&w.transcript, "result=%v stdout=%q stderr=%q\n", err, out, diag)
	w.t.Logf("keyparcel %s → %v", strings.Join(args[:len(args)-4], " "), err)
	return out, diag, err
}
func (w *world) ok(c client, input []byte, args ...string) []byte {
	w.t.Helper()
	out, diag, err := w.run(c, input, args...)
	if err != nil {
		w.t.Fatalf("expected success: %v\nstdout: %s\nstderr: %s", err, out, diag)
	}
	return out
}
func (w *world) fails(c client, input []byte, reason string, args ...string) []byte {
	w.t.Helper()
	out, diag, err := w.run(c, input, args...)
	if err == nil {
		w.t.Fatal("expected failure", args)
	}
	if !strings.Contains(string(diag), reason) {
		w.t.Fatalf("expected failure containing %q, got %s", reason, diag)
	}
	return out
}
func (w *world) approve() {
	w.ok(w.alice, nil, "trust", w.bob.fingerprint, "--name", "bob")
	w.ok(w.bob, nil, "trust", w.alice.fingerprint, "--name", "alice")
}
func (w *world) items(c client) []core.Item {
	w.t.Helper()
	out := w.ok(c, nil, "inbox", "--json")
	var items []core.Item
	if err := json.Unmarshal(out, &items); err != nil {
		w.t.Fatal(err)
	}
	return items
}
func (w *world) admin(args ...string) []byte {
	w.t.Helper()
	if w.local != nil {
		return w.localAdmin(args...)
	}
	out, diag, err := container(w.ctx, nil, append([]string{"exec", w.id}, args...)...)
	if err != nil {
		w.t.Fatalf("server fixture command %v: %v %s", args, err, diag)
	}
	return out
}
func (w *world) put(remote string, b []byte) {
	w.t.Helper()
	if w.local != nil {
		w.localPath(remote)
		if err := os.WriteFile(remote, b, 0600); err != nil {
			w.t.Fatal(err)
		}
		return
	}
	out, diag, err := container(w.ctx, b, "exec", "-i", w.id, "sh", "-c", `cat > "$1"`, "sh", remote)
	if err != nil {
		w.t.Fatalf("fixture write: %v %s%s", err, out, diag)
	}
}
func (w *world) remote(c client, id string) string {
	w.t.Helper()
	identity, err := core.LoadIdentity(c.key)
	if err != nil {
		w.t.Fatal(err)
	}
	return w.storageRoot() + "/inbox/" + core.DeviceID(identity.Public) + "/" + id + ".age"
}
func (w *world) exists(remote string) bool {
	w.t.Helper()
	if w.local != nil {
		w.localPath(remote)
		_, err := os.Stat(remote)
		return err == nil
	}
	_, _, err := container(w.ctx, nil, "exec", w.id, "test", "-e", remote)
	return err == nil
}
func (w *world) assertExists(remote string, expected bool) {
	w.t.Helper()
	if actual := w.exists(remote); actual != expected {
		w.t.Fatalf("remote existence: %s: expected %v, got %v", remote, expected, actual)
	}
}
func equalBytes(t *testing.T, want, got []byte) {
	t.Helper()
	if !bytes.Equal(want, got) {
		t.Fatalf("exact byte comparison failed: expected %d bytes, got %d", len(want), len(got))
	}
}
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

// Wait for the restarted daemon, rather than connecting during SIGHUP re-exec.
func (w *world) reloadSSHD() {
	w.t.Helper()
	const listening = "Server listening on 0.0.0.0 port 22."
	before := bytes.Count(w.logs(), []byte(listening))
	w.admin("kill", "-HUP", "1")
	ctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()
	var logs []byte
	for {
		out, diag, err := container(ctx, nil, "logs", w.id)
		if err != nil {
			w.t.Fatalf("SSH reload readiness: read daemon log: %v\n%s%s\nLast daemon log:\n%s", err, out, diag, logs)
		}
		logs = append(out, diag...)
		if bytes.Count(logs, []byte(listening)) > before {
			return
		}
		select {
		case <-ctx.Done():
			w.t.Fatalf("SSH reload readiness: no new listener within 10 seconds: %v\n%s", ctx.Err(), logs)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (w *world) logs() []byte {
	w.t.Helper()
	if w.local != nil {
		data, err := os.ReadFile(w.local.log)
		if err != nil {
			w.t.Fatal(err)
		}
		return data
	}
	out, diag, err := container(w.ctx, nil, "logs", w.id)
	if err != nil {
		w.t.Fatal(err)
	}
	return append(out, diag...)
}

// Each client can see only its own disposable fixture directory.
func (w *world) exec(c client, input []byte, args ...string) ([]byte, []byte, error) {
	if w.local != nil {
		return w.localExec(c, input, args...)
	}
	return container(w.ctx, input, append([]string{"exec", "-i", c.id}, args...)...)
}
func (w *world) clientOK(c client, input []byte, args ...string) []byte {
	w.t.Helper()
	out, diag, err := w.exec(c, input, args...)
	if err != nil {
		w.t.Fatalf("client fixture %v: %v %s%s", args, err, out, diag)
	}
	return out
}

// Start and signal the actual client process, rather than the host docker exec process.
type operation struct {
	c    client
	path string
}

func (w *world) start(c client, args ...string) operation {
	if w.local != nil {
		return w.localStart(c, args...)
	}
	path := filepath.Join(c.dir, fmt.Sprintf("operation-%d", time.Now().UnixNano()))
	script := `base=$1; shift; "$@" > "$base.log" 2>&1 & pid=$!; echo "$pid" > "$base.pid"; wait "$pid"; echo "$?" > "$base.status"`
	_, diag, err := container(w.ctx, nil, append([]string{"exec", "-d", c.id, "sh", "-c", script, "sh", path, "keyparcel"}, append(args, "--config", c.config, "--ssh-config", c.sshConfig)...)...)
	if err != nil {
		w.t.Fatalf("start client operation: %v %s", err, diag)
	}
	return operation{c, path}
}
func (w *world) interrupt(op operation) {
	w.clientOK(op.c, nil, "sh", "-c", `kill -INT "$(cat "$1.pid")"`, "sh", op.path)
}
func (w *world) wait(op operation) int {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(op.path + ".status")
		if err == nil && len(bytes.TrimSpace(b)) > 0 {
			var status int
			if _, err = fmt.Sscanf(string(b), "%d", &status); err != nil {
				w.t.Fatal(err)
			}
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := os.ReadFile(op.path + ".log")
	w.t.Fatalf("client operation did not exit: %s", b)
	return -1
}
