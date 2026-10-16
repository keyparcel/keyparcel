//go:build e2e && androidtest

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"keyparcel/core"
)

const androidPackage = "dev.keyparcel.verification"

func TestAndroidVerification(t *testing.T) {
	sdk := os.Getenv("ANDROID_HOME")
	if sdk == "" {
		t.Fatal("ANDROID_HOME is required; see android/README.md")
	}
	serial := os.Getenv("ANDROID_SERIAL")
	if serial == "" {
		serial = "emulator-5554"
	}
	w := makeWorldWithOptions(t, false, "ed25519", "openssh", worldOptions{timeout: 15 * time.Minute, publishSSH: true})
	adb := func(args ...string) []byte {
		t.Helper()
		out, diag, err := command(w.ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), append([]string{"-s", serial}, args...)...)
		if err != nil {
			t.Fatalf("ADB %v: %v\n%s%s", args, err, out, diag)
		}
		return out
	}
	if strings.TrimSpace(string(adb("shell", "getprop", "sys.boot_completed"))) != "1" {
		t.Fatal("Android must finish booting before running verification")
	}
	build := filepath.Join(repo, "android", "build")
	if err := os.Remove(filepath.Join(build, "verification-report.json")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, diag, _ := command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "logcat", "-d", "-s", "KeyparcelVerification:I", "AndroidRuntime:E")
		os.WriteFile(filepath.Join(build, "android-logcat.txt"), append(out, diag...), 0600)
		command(ctx, nil, filepath.Join(sdk, "platform-tools", "adb"), "-s", serial, "reverse", "--remove", "tcp:2222")
	})
	if !strings.Contains(string(adb("install", "-r", filepath.Join(build, "verification.apk"))), "Success") {
		t.Fatal("verification APK did not install successfully")
	}
	// Only this disposable test app is reset; Android stays running across scenarios.
	if !strings.Contains(string(adb("shell", "pm", "clear", androidPackage)), "Success") {
		t.Fatal("verification app data could not be reset")
	}
	out, diag, err := container(w.ctx, nil, "port", w.id, "22/tcp")
	if err != nil {
		t.Fatalf("published fixture SSH port: %v %s", err, diag)
	}
	_, port, err := net.SplitHostPort(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	adb("reverse", "--no-rebind", "tcp:2222", "tcp:"+port)
	type report struct {
		PublicKey   string   `json:"public_key"`
		Fingerprint string   `json:"fingerprint"`
		PID         int      `json:"pid"`
		TextID      string   `json:"text_id"`
		BinaryID    string   `json:"binary_id"`
		Checks      []string `json:"checks"`
	}
	reports := map[string]report{}
	phase := func(name string, arguments ...string) report {
		t.Helper()
		start := time.Now()
		args := []string{"shell", "am", "instrument", "-w", "-r", "-e", "phase", name}
		for i := 0; i < len(arguments); i += 2 {
			// adb shell joins arguments into a command; quote spaces and metacharacters.
			args = append(args, "-e", arguments[i], "'"+strings.ReplaceAll(arguments[i+1], "'", "'\"'\"'")+"'")
		}
		result := adb(append(args, androidPackage+"/"+androidPackage+".Verification")...)
		os.WriteFile(filepath.Join(build, "instrumentation-"+name+".txt"), result, 0600)
		if !bytes.Contains(result, []byte("verification_passed=true")) || !bytes.Contains(result, []byte("INSTRUMENTATION_CODE: -1")) {
			t.Fatalf("Android %s instrumentation failed:\n%s", name, result)
		}
		var got report
		found := false
		for _, line := range strings.Split(string(result), "\n") {
			if body, ok := strings.CutPrefix(line, "INSTRUMENTATION_RESULT: report="); ok {
				if err := json.Unmarshal([]byte(body), &got); err != nil {
					t.Fatalf("Android %s report: %v", name, err)
				}
				found = true
			}
		}
		if !found || got.PublicKey == "" || got.PID == 0 {
			t.Fatalf("Android %s did not return its public identity and process ID", name)
		}
		reports[name] = got
		t.Logf("Android %s passed in %s (PID %d): %s", name, time.Since(start).Round(time.Second), got.PID, strings.Join(got.Checks, "; "))
		return got
	}
	identity := phase("identity")
	key, err := core.ParsePublic(identity.PublicKey)
	if err != nil {
		t.Fatalf("Android-generated public key: %v", err)
	}
	if core.PublicText(key) != identity.PublicKey {
		t.Fatal("Android public key is not canonical SSH text")
	}
	authorized := w.admin("cat", "/home/parcel/.ssh/authorized_keys")
	w.put("/home/parcel/.ssh/authorized_keys", append(authorized, []byte(identity.PublicKey+" android\n")...))
	w.ok(w.alice, nil, "trust", identity.Fingerprint, "--name", "android")
	w.ok(w.carol, nil, "trust", identity.Fingerprint, "--name", "android")
	text := []byte("  Keyparcel 世界 🌍\n\n")
	binary := make([]byte, 4096)
	for i := range binary {
		binary[i] = byte(i)
	}
	send := func(sender client, input []byte, arguments ...string) string {
		t.Helper()
		output := w.ok(sender, input, append([]string{"send", "--to", "android"}, arguments...)...)
		id := strings.TrimSpace(strings.TrimPrefix(string(output), "android: stored "))
		if _, err := core.ResolveID(id, []string{id}); err != nil || len(id) != 32 {
			t.Fatalf("Linux-to-Android send returned an invalid transfer ID: %s", output)
		}
		return id
	}
	textID := send(w.alice, text, "--stdin")
	file := filepath.Join(w.alice.dir, "linux-binary.bin")
	if err := os.WriteFile(file, binary, 0600); err != nil {
		t.Fatal(err)
	}
	binaryID := send(w.alice, nil, "--file", file)
	unapprovedID := send(w.carol, []byte("unapproved sender"), "--stdin")
	corruptedID := send(w.alice, nil, "--file", file)
	remote := func(id string) string {
		return "/home/parcel/.local/share/keyparcel/inbox/" + core.DeviceID(key) + "/" + id + ".age"
	}
	corruptedPath := remote(corruptedID)
	data := w.admin("cat", corruptedPath)
	data[len(data)-1] ^= 1
	w.put(corruptedPath, data)
	peerPublic := strings.TrimSpace(string(w.admin("cat", "/home/parcel/.ssh/authorized_keys")))
	peerPublic = strings.Split(peerPublic, "\n")[0]
	hostKey := strings.TrimSpace(string(w.admin("cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
	arguments := []string{"port", "2222", "host_key", hostKey, "peer_public", peerPublic, "expected_public", identity.PublicKey}
	exchange := phase("exchange", append(arguments, "text_id", textID, "binary_id", binaryID, "unapproved_id", unapprovedID, "corrupted_id", corruptedID)...)
	for _, id := range []string{textID, binaryID} {
		w.assertExists(remote(id), false)
	}
	for _, id := range []string{unapprovedID, corruptedID} {
		w.assertExists(remote(id), true)
	}
	equalBytes(t, text, w.ok(w.alice, nil, "receive", exchange.TextID, "--stdout"))
	saved := filepath.Join(w.alice.dir, "from-android.bin")
	w.ok(w.alice, nil, "receive", exchange.BinaryID, "--save-to", saved)
	got, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	equalBytes(t, binary, got)
	// Re-enrollment is deliberately absent: the existing identity must remain usable.
	adb("shell", "am", "force-stop", androidPackage)
	restartID := send(w.alice, text, "--stdin")
	restarted := phase("restart", append(arguments, "text_id", restartID)...)
	if restarted.PID == exchange.PID {
		t.Fatal("app restart reused the previous process")
	}
	if restarted.PublicKey != identity.PublicKey || restarted.Fingerprint != identity.Fingerprint {
		t.Fatal("app restart replaced the device identity")
	}
	w.assertExists(remote(restartID), false)
	encoded, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(build, "verification-report.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
