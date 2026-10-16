//go:build e2e && androidtest

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
)

func TestAndroidTextClient(t *testing.T) {
	const app = "dev.keyparcel.app.scenario"
	sdk := os.Getenv("ANDROID_HOME")
	if sdk == "" {
		t.Fatal("ANDROID_HOME is required")
	}
	serial := os.Getenv("ANDROID_SERIAL")
	if serial == "" {
		serial = "emulator-5556"
	}
	// Direct Go runs must enforce the same clipboard isolation as the shell runner.
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
	out, diag, err := command(ctx, nil, "bash", filepath.Join(repo, "android", "ensure-emulator.sh"), "test")
	cancel()
	if err != nil {
		t.Fatalf("Android UI emulator isolation: %v\n%s%s", err, out, diag)
	}
	// Leave room for the complete picker journey on software-only emulators.
	w := makeWorldWithOptions(t, false, "ed25519", "openssh", worldOptions{timeout: 30 * time.Minute, publishSSH: true})
	adb := func(args ...string) []byte {
		t.Helper()
		out, diag, err := command(w.ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), append([]string{"-s", serial}, args...)...)
		if err != nil {
			t.Fatalf("ADB %v: %v\n%s%s", args, err, out, diag)
		}
		return out
	}
	// Exercise phone keyboard insets even when the emulator also accepts host keys.
	keyboard := strings.TrimSpace(string(adb("shell", "settings", "get", "secure", "show_ime_with_hard_keyboard")))
	if keyboard != "0" && keyboard != "1" && keyboard != "null" {
		t.Fatalf("Unexpected virtual keyboard setting: %q", keyboard)
	}
	adb("shell", "settings", "put", "secure", "show_ime_with_hard_keyboard", "1")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		args := []string{"-s", serial, "shell", "settings", "put", "secure", "show_ime_with_hard_keyboard", keyboard}
		if keyboard == "null" {
			args = []string{"-s", serial, "shell", "settings", "delete", "secure", "show_ime_with_hard_keyboard"}
		}
		command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), args...)
	})
	build := filepath.Join(repo, "android", "client", "app", "build", "outputs", "apk")
	for _, apk := range []string{filepath.Join(build, "scenario", "debug", "app-scenario-debug.apk"), filepath.Join(build, "androidTest", "scenario", "debug", "app-scenario-debug-androidTest.apk")} {
		if !strings.Contains(string(adb("install", "-r", apk)), "Success") {
			t.Fatalf("Could not install %s", apk)
		}
	}
	// This package is separate from both the smoke client and the core verification app.
	if !strings.Contains(string(adb("shell", "pm", "clear", app)), "Success") {
		t.Fatal("Could not reset scenario app")
	}
	portOut, diag, err := container(w.ctx, nil, "port", w.id, "22/tcp")
	if err != nil {
		t.Fatalf("SSH fixture port: %v %s", err, diag)
	}
	_, port, err := net.SplitHostPort(strings.TrimSpace(string(portOut)))
	if err != nil {
		t.Fatal(err)
	}
	adb("reverse", "--no-rebind", "tcp:2444", "tcp:"+port)
	evidence := filepath.Join(repo, "android", "client", "build")
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "reverse", "--remove", "tcp:2444")
		command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "reverse", "--remove", "tcp:2445")
		out, diag, _ := command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "logcat", "-d", "-s", "AndroidRuntime:E")
		os.WriteFile(filepath.Join(evidence, "logcat.txt"), append(out, diag...), 0600)
	})
	phaseWhile := func(name string, monitor func(context.Context) error, args ...string) string {
		t.Helper()
		start := time.Now()
		ctx, cancel := context.WithCancel(w.ctx)
		defer cancel()
		monitored := make(chan error, 1)
		if monitor != nil {
			go func() { monitored <- monitor(ctx) }()
		}
		params := []string{"shell", "am", "instrument", "-w", "-e", "class", "dev.keyparcel.app.ClientJourneyTest#" + name}
		for i := 0; i < len(args); i += 2 {
			params = append(params, "-e", args[i], "'"+strings.ReplaceAll(args[i+1], "'", "'\"'\"'")+"'")
		}
		params = append(params, app+".test/androidx.test.runner.AndroidJUnitRunner")
		out := string(adb(params...))
		os.WriteFile(filepath.Join(evidence, name+".log"), []byte(out), 0600)
		var monitorErr error
		if monitor != nil {
			cancel()
			monitorErr = <-monitored
		}
		if !strings.Contains(out, "OK (1 test)") {
			t.Fatalf("Android UI %s failed:\n%s", name, out)
		}
		if monitorErr != nil {
			t.Fatalf("Android UI %s fixture coordination: %v", name, monitorErr)
		}
		t.Logf("Android UI %s passed in %s", name, time.Since(start).Round(time.Second))
		return out
	}
	phase := func(name string, args ...string) string { return phaseWhile(name, nil, args...) }
	publicValue := func(out, name string) string {
		t.Helper()
		match := regexp.MustCompile(`INSTRUMENTATION_STATUS: ` + name + `=([^\r\n]+)`).FindStringSubmatch(out)
		if len(match) != 2 {
			t.Fatalf("Missing public UI report %s: %s", name, out)
		}
		return strings.TrimSpace(match[1])
	}
	hostKey := strings.TrimSpace(string(w.admin("cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
	enrolled := phase("enrollment", "host_key", hostKey, "port", "2444")
	public := publicValue(enrolled, "phone_public")
	key, err := core.ParsePublic(public)
	if err != nil {
		t.Fatal(err)
	}
	if publicValue(enrolled, "phone_fingerprint") != ssh.FingerprintSHA256(key) {
		t.Fatal("UI fingerprint differs from its displayed public key")
	}
	authorized := w.admin("cat", "/home/parcel/.ssh/authorized_keys")
	w.put("/home/parcel/.ssh/authorized_keys", append(authorized, []byte(public+" phone\n")...))
	phase("connectRememberedServer")
	phase("foregroundRefresh")
	phase("pickerCancellation")
	w.admin("sh", "-c", "rm /etc/ssh/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key.pub && ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key")
	w.admin("kill", "-HUP", "1")
	time.Sleep(100 * time.Millisecond)
	hostKey = strings.TrimSpace(string(w.admin("cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
	phase("changedServerKey", "host_key", hostKey)
	parts := strings.Fields(hostKey)
	for _, c := range w.clients {
		if err := os.WriteFile(filepath.Join(c.dir, "known_hosts"), []byte("inbox "+parts[0]+" "+parts[1]+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	adb("reverse", "--no-rebind", "tcp:2445", "tcp:"+port)
	phase("changedEndpoint", "port", "2445")
	phase("sendToThisDevice")
	w.ok(w.alice, nil, "trust", ssh.FingerprintSHA256(key), "--name", "phone")
	text := []byte("  Keyparcel 世界 🌍\n\n")
	send := func(input []byte, args ...string) string {
		t.Helper()
		out := w.ok(w.alice, input, append([]string{"send", "--to", "phone"}, args...)...)
		id := strings.TrimSpace(strings.TrimPrefix(string(out), "phone: stored "))
		if len(id) != 32 {
			t.Fatalf("Invalid CLI transfer ID: %s", out)
		}
		return id
	}
	textID := send(text, "--stdin")
	block := make([]byte, 64*1024)
	for i := range block {
		block[i] = byte(i % 251)
	}
	fileBytes := append(bytes.Repeat(block, 17*16), block[:31]...)
	file := filepath.Join(w.alice.dir, "file-世界.bin")
	if err := os.WriteFile(file, fileBytes, 0600); err != nil {
		t.Fatal(err)
	}
	fileID := send(nil, "--file", file)
	phase("exchange", "text_id", textID, "file_id", fileID, "peer_fingerprint", w.alice.fingerprint, "other_peer_fingerprint", w.bob.fingerprint)
	inboxDir := "/home/parcel/.local/share/keyparcel/inbox/" + core.DeviceID(key)
	remote := func(id string) string { return inboxDir + "/" + id + ".age" }
	w.assertExists(remote(textID), true)
	w.assertExists(remote(fileID), true)
	// The UI sent actual bytes through the bridge; inspect them with the independent CLI.
	var items []core.Item
	if err := json.Unmarshal(w.ok(w.alice, nil, "inbox", "--json"), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected one Android reply, found %d", len(items))
	}
	equalBytes(t, text, w.ok(w.alice, nil, "receive", items[0].ID, "--stdout"))
	w.admin("chmod", "0555", inboxDir)
	phase("removalFailure", "text_id", textID, "file_id", fileID)
	w.assertExists(remote(textID), true)
	w.assertExists(remote(fileID), true)
	w.admin("chmod", "0755", inboxDir)
	adb("shell", "am", "force-stop", app)
	newID := send(text, "--stdin")
	phase("restartAndRemove", "text_id", textID, "new_text_id", newID, "expected_public", public)
	w.assertExists(remote(textID), false)
	w.assertExists(remote(newID), false)
	w.assertExists(remote(fileID), true)
	deleteTextID := send([]byte("unwanted text"), "--stdin")
	deleteFileID := send(nil, "--file", file)
	phase("inboxDeletion", "delete_text_id", deleteTextID, "delete_file_id", deleteFileID, "file_id", fileID)
	w.assertExists(remote(deleteTextID), false)
	w.assertExists(remote(deleteFileID), false)
	w.assertExists(remote(fileID), true)
	// Inbox deletion must not touch an earlier provider-saved local file.
	equalBytes(t, fileBytes, adb("exec-out", "run-as", app, "cat", "files/document-fixtures/removal-retry.bin"))
	func() {
		out, diag, err := container(w.ctx, nil, "pause", w.id)
		if err != nil {
			t.Fatalf("Could not stall the SSH fixture: %v\n%s%s", err, out, diag)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			out, diag, err := container(ctx, nil, "unpause", w.id)
			if err != nil {
				t.Fatalf("Could not resume the SSH fixture: %v\n%s%s", err, out, diag)
			}
		}()
		phase("discoveryTimeout", "port", "2444")
	}()
	func() {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			out, diag, err := container(ctx, nil, "unpause", w.id)
			if err != nil && !t.Failed() {
				t.Errorf("Could not resume the SSH fixture: %v\n%s%s", err, out, diag)
			}
		}()
		adb("shell", "run-as", app, "rm", "-f", "cache/stalled-ready", "cache/stalled-go")
		phaseWhile("stalledRequest", func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
			defer cancel()
			for {
				_, _, err := command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "shell", "run-as", app, "test", "-f", "cache/stalled-ready")
				if err == nil {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(250 * time.Millisecond):
				}
			}
			if out, diag, err := container(ctx, nil, "pause", w.id); err != nil {
				return fmt.Errorf("pause SSH fixture: %w: %s%s", err, out, diag)
			}
			_, _, err := command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "shell", "run-as", app, "touch", "cache/stalled-go")
			return err
		}, "file_id", fileID)
	}()
	phase("retryAfterStall", "file_id", fileID)
	prefix, tail := "prefix\n", " 世界 🌍\ncomplete-tail\n"
	large := []byte(prefix + strings.Repeat("x", 193638-len(prefix)-len(tail)) + tail)
	largeID := send(large, "--stdin")
	nulID := send([]byte("before\x00after"), "--stdin")
	platformID := send([]byte(strings.Repeat("x", 1024*1024)), "--stdin")
	phase("receivedClipboardCases", "large_id", largeID, "nul_id", nulID, "platform_id", platformID)
	for _, id := range []string{largeID, nulID, platformID} {
		w.assertExists(remote(id), true)
	}
	fileReport := phase("fileTransfers", "file_id", fileID)
	w.assertExists(remote(fileID), false)
	for _, name := range []string{"removal-retry.bin", "renamed-世界.bin", "renamed-世界(1).bin"} {
		equalBytes(t, fileBytes, adb("exec-out", "run-as", app, "cat", "files/document-fixtures/"+name))
	}
	if err := json.Unmarshal(w.ok(w.alice, nil, "inbox", "--json"), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("Expected binary and empty Android file replies, found %d", len(items))
	}
	for _, reply := range []struct {
		id, name string
		data     []byte
	}{
		{publicValue(fileReport, "binary_reply_id"), "payload-世界.bin", fileBytes},
		{publicValue(fileReport, "empty_reply_id"), "empty.bin", nil},
	} {
		found := false
		for _, item := range items {
			if item.ID == reply.id {
				found = item.Type == "file" && item.Filename != nil && *item.Filename == reply.name && item.Size == int64(len(reply.data))
			}
		}
		if !found {
			t.Fatalf("Android file reply lost its signed filename, size, or type: %s", reply.name)
		}
		destination := filepath.Join(w.alice.dir, "received-"+reply.name)
		w.ok(w.alice, nil, "receive", reply.id, "--save-to", destination)
		got, err := os.ReadFile(destination)
		if err != nil {
			t.Fatalf("Read Android file reply %s: %v", reply.name, err)
		}
		equalBytes(t, reply.data, got)
	}
	// Exercise failed-only GUI retries using the existing phone/CLI fixture.
	peerInbox := filepath.Dir(w.remote(w.alice, strings.Repeat("0", 32)))
	beforeOwn := strings.Fields(string(w.admin("ls", "-1", inboxDir)))
	beforePeer := strings.Fields(string(w.admin("ls", "-1", peerInbox)))
	for _, kind := range []string{"text", "file"} {
		for _, stage := range []string{"prepare", "retry"} {
			adb("shell", "run-as", app, "rm", "-f", "cache/multi-"+kind+"-"+stage+"-ready", "cache/multi-"+kind+"-"+stage+"-go")
		}
	}
	multi := phaseWhile("multipleRecipients", func(ctx context.Context) error {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			container(cleanup, nil, "exec", w.id, "chmod", "700", peerInbox)
		}()
		for _, kind := range []string{"text", "file"} {
			for _, stage := range []string{"prepare", "retry"} {
				marker := "cache/multi-" + kind + "-" + stage
				for {
					_, _, err := command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "shell", "run-as", app, "test", "-f", marker+"-ready")
					if err == nil {
						break
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(250 * time.Millisecond):
					}
				}
				mode := "500"
				if stage == "retry" {
					mode = "700"
				}
				if out, diag, err := container(ctx, nil, "exec", w.id, "chmod", mode, peerInbox); err != nil {
					return fmt.Errorf("multi-recipient permission fixture: %w: %s%s", err, out, diag)
				}
				if _, _, err := command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "shell", "run-as", app, "touch", marker+"-go"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if after := strings.Fields(string(w.admin("ls", "-1", inboxDir))); len(after) != len(beforeOwn)+2 {
		t.Fatalf("Multi-recipient retry duplicated or omitted self copies: before %v after %v", beforeOwn, after)
	}
	if after := strings.Fields(string(w.admin("ls", "-1", peerInbox))); len(after) != len(beforePeer)+2 {
		t.Fatalf("Multi-recipient retry duplicated or omitted peer copies: before %v after %v", beforePeer, after)
	}
	for _, kind := range []string{"text", "file"} {
		w.assertExists(remote(publicValue(multi, kind+"_self_id")), true)
		peerID := publicValue(multi, kind+"_peer_id")
		w.assertExists(w.remote(w.alice, peerID), true)
		if kind == "text" {
			equalBytes(t, []byte("multi text 世界\n"), w.ok(w.alice, nil, "receive", peerID, "--stdout"))
		} else {
			destination := filepath.Join(w.alice.dir, "multi.bin")
			w.ok(w.alice, nil, "receive", peerID, "--save-to", destination)
			got, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			equalBytes(t, []byte{0, 1, 2, 255, 42}, got)
		}
	}

	for _, name := range []string{"setup", "server-approval", "host-key-changed", "inbox", "inbox-details", "settings-simple", "peer-approval", "send-draft", "received", "sent", "removal-error", "removed", "timeout", "received-copy", "clipboard-unavailable", "send-file", "saved-file", "send-multiple-text", "send-multiple-file"} {
		out := adb("exec-out", "run-as", app, "cat", "files/"+name+".png")
		if len(out) < 8 || string(out[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Fatalf("Invalid %s screenshot", name)
		}
		if err := os.WriteFile(filepath.Join(evidence, name+".png"), out, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
