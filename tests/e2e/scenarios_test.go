//go:build e2e

package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"keyparcel/core"
)

func TestReceiveTextDeletesPackage(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		input := []byte("  hello 世界\n\n")
		w.ok(w.alice, input, "send", "--to", "bob", "--stdin")
		items := w.items(w.bob)
		if len(items) != 1 {
			t.Fatal("expected one item", items)
		}
		item := items[0]
		if item.Type != "text" || item.From != "alice" || item.Size != int64(len(input)) || item.Filename != nil {
			t.Fatal("unexpected signed metadata", item)
		}
		remote := w.remote(w.bob, item.ID)
		w.assertExists(remote, true)
		got := w.ok(w.bob, nil, "receive", item.ID[:8], "--stdout")
		equalBytes(t, input, got)
		w.assertExists(remote, false)
		if len(w.items(w.bob)) != 0 {
			t.Fatal("inbox not empty after successful receive")
		}
	})
}
func TestFileBytesAndKeep(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		input := bytes.Repeat([]byte{0, 255, 1, 10, 128}, 14000)
		source := filepath.Join(w.alice.dir, "source-世界.bin")
		os.WriteFile(source, input, 0600)
		w.ok(w.alice, nil, "send", "--to", "bob", "--file", source)
		items := w.items(w.bob)
		if len(items) != 1 {
			t.Fatal(items)
		}
		item := items[0]
		if item.Type != "file" || item.Filename == nil || *item.Filename != filepath.Base(source) {
			t.Fatal("file metadata mismatch")
		}
		dest := filepath.Join(w.bob.dir, "downloads")
		os.Mkdir(dest, 0700)
		// Exact IDs need directory traversal, but not an inbox listing.
		inbox := filepath.Dir(w.remote(w.bob, item.ID))
		w.admin("chmod", "u-r", inbox)
		w.fails(w.bob, nil, "permission denied", "receive", item.ID[:8], "--save-to", dest, "--keep")
		w.ok(w.bob, nil, "receive", item.ID, "--save-to", dest, "--keep")
		got, err := os.ReadFile(filepath.Join(dest, filepath.Base(source)))
		if err != nil {
			t.Fatal(err)
		}
		equalBytes(t, input, got)
		w.assertExists(w.remote(w.bob, item.ID), true)
		w.fails(w.bob, nil, "destination already exists", "receive", item.ID, "--save-to", dest)
		w.assertExists(w.remote(w.bob, item.ID), true)
		os.WriteFile(filepath.Join(dest, filepath.Base(source)), []byte("replace me"), 0600)
		w.ok(w.bob, nil, "receive", item.ID, "--save-to", dest, "--overwrite", "--keep")
		got, err = os.ReadFile(filepath.Join(dest, filepath.Base(source)))
		if err != nil {
			t.Fatal(err)
		}
		equalBytes(t, input, got)
		w.assertExists(w.remote(w.bob, item.ID), true)
		renamed := filepath.Join(dest, "renamed.bin")
		w.ok(w.bob, nil, "receive", item.ID, "--save-to", renamed)
		got, err = os.ReadFile(renamed)
		if err != nil {
			t.Fatal(err)
		}
		equalBytes(t, input, got)
		w.assertExists(w.remote(w.bob, item.ID), false)
		w.admin("chmod", "u+r", inbox)
		// Text has no original filename; directory saves and explicit names both work.
		for _, sendFlag := range []string{"--text", "--stdin"} {
			text := []byte("  saved 世界\n\n")
			if sendFlag == "--text" {
				w.ok(w.alice, nil, "send", "--to", "bob", sendFlag, string(text))
			} else {
				w.ok(w.alice, text, "send", "--to", "bob", sendFlag)
			}
			item = w.items(w.bob)[0]
			remote := w.remote(w.bob, item.ID)
			w.fails(w.bob, nil, "nonempty path", "receive", item.ID, "--stdout", "--save-to", "")
			w.fails(w.bob, nil, "exactly one", "receive", item.ID, "--stdout", "--save-to", dest)
			w.fails(w.bob, nil, "directory does not exist", "receive", item.ID, "--save-to", filepath.Join(dest, "missing")+"/")
			w.fails(w.bob, nil, "directory does not exist", "receive", item.ID, "--save-to", filepath.Join(dest, "missing", "text.log"))
			w.assertExists(remote, true)
			w.ok(w.bob, nil, "receive", item.ID, "--save-to", dest+"/", "--keep")
			generated := filepath.Join(dest, item.ID+".txt")
			got, err = os.ReadFile(generated)
			if err != nil {
				t.Fatal(err)
			}
			equalBytes(t, text, got)
			w.fails(w.bob, nil, "destination already exists", "receive", item.ID, "--save-to", generated)
			got, err = os.ReadFile(generated)
			if err != nil {
				t.Fatal(err)
			}
			equalBytes(t, text, got)
			w.assertExists(remote, true)
			named := filepath.Join(dest, sendFlag[2:]+".log")
			os.WriteFile(named, []byte("replace text"), 0600)
			w.ok(w.bob, nil, "receive", item.ID, "--save-to", named, "--overwrite")
			got, err = os.ReadFile(named)
			if err != nil {
				t.Fatal(err)
			}
			equalBytes(t, text, got)
			w.assertExists(remote, false)
		}
	})
}
func TestMultipleRecipientsRetrieveIndependently(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := makeWorld(t, false, "rsa", mode)
		w.approve()
		w.ok(w.alice, nil, "trust", w.carol.fingerprint, "--name", "carol")
		w.ok(w.carol, nil, "trust", w.alice.fingerprint, "--name", "alice")
		w.ok(w.alice, nil, "send", "--to", "bob", "--to", "carol", "--text", "hello")
		a, b := w.items(w.bob), w.items(w.carol)
		if len(a) != 1 || len(b) != 1 || a[0].ID != b[0].ID {
			t.Fatal("expected shared ID for independent copies")
		}
		equalBytes(t, w.admin("cat", w.remote(w.bob, a[0].ID)), w.admin("cat", w.remote(w.carol, b[0].ID)))
		equalBytes(t, []byte("hello"), w.ok(w.bob, nil, "receive", a[0].ID, "--stdout"))
		w.assertExists(w.remote(w.carol, b[0].ID), true)
		equalBytes(t, []byte("hello"), w.ok(w.carol, nil, "receive", b[0].ID, "--stdout"))
		w.ok(w.bob, nil, "send", "--to", "alice", "--text", "RSA reply")
		reply := w.items(w.alice)
		if len(reply) != 1 {
			t.Fatal("expected one RSA-signed reply", reply)
		}
		equalBytes(t, []byte("RSA reply"), w.ok(w.alice, nil, "receive", reply[0].ID, "--stdout"))
	})
}
func TestUnapprovedSenderAndExplicitDelete(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.ok(w.alice, nil, "trust", w.bob.fingerprint, "--name", "bob")
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "hello")
		out := w.fails(w.bob, nil, "unapproved sender", "inbox", "--json")
		var items []core.Item
		if err := json.Unmarshal(out, &items); err != nil || len(items) != 0 {
			t.Fatal("unapproved metadata was listed", string(out))
		}
		listing := strings.Fields(string(w.admin("find", w.storageRoot(), "-name", "*.age")))
		if len(listing) != 1 {
			t.Fatal(listing)
		}
		id := strings.TrimSuffix(filepath.Base(listing[0]), ".age")
		equalBytes(t, nil, w.fails(w.bob, nil, "unapproved sender", "receive", id, "--stdout"))
		w.assertExists(listing[0], true)
		w.ok(w.bob, nil, "delete", id)
		w.assertExists(listing[0], false)
	})
}
func TestTamperingBeyondMetadataAndTruncation(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		input := bytes.Repeat([]byte("x"), 1024*1024)
		source := filepath.Join(w.alice.dir, "large.bin")
		os.WriteFile(source, input, 0600)
		w.ok(w.alice, nil, "send", "--to", "bob", "--file", source)
		item := w.items(w.bob)[0]
		remote := w.remote(w.bob, item.ID)
		cipher := w.admin("cat", remote)
		mutated := append([]byte(nil), cipher...)
		mutated[len(mutated)-1] ^= 1
		w.put(remote, mutated)
		if len(w.items(w.bob)) != 1 {
			t.Fatal("authenticated metadata should still be readable")
		}
		equalBytes(t, nil, w.fails(w.bob, nil, "verification", "receive", item.ID, "--stdout"))
		w.assertExists(remote, true)
		w.put(remote, cipher[:len(cipher)-17])
		equalBytes(t, nil, w.fails(w.bob, nil, "verification", "receive", item.ID, "--stdout"))
		w.assertExists(remote, true)
	})
}
func TestHostKeyAndSelectedIdentityEnforcement(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		known := filepath.Join(w.alice.dir, "known_hosts")
		os.WriteFile(known, nil, 0600)
		unknown, changed, denied := "Host key verification failed", "HOST IDENTIFICATION HAS CHANGED", "Permission denied"
		if mode == "embedded" {
			unknown, changed, denied = "unknown SSH host key", "host key does not match", "unable to authenticate"
		}
		w.fails(w.alice, nil, unknown, "devices")
		if mode == "openssh" {
			backgroundError := filepath.Join(w.alice.dir, "background.stderr")
			background := "keyparcel devices --config " + shellQuote(w.alice.config) + " --ssh-config " + shellQuote(w.alice.sshConfig) + " 2> " + shellQuote(backgroundError) + " & wait $!"
			_, terminal, err := w.exec(w.alice, nil, "timeout", "5s", "script", "-qefc", "bash -mc "+shellQuote(background), "/dev/null")
			diagnostic, _ := os.ReadFile(backgroundError)
			if err == nil || !bytes.Contains(diagnostic, []byte(unknown)) || bytes.Contains(terminal, []byte("Are you sure")) {
				t.Fatalf("background job must fail without prompting or hanging: %v %s %s", err, terminal, diagnostic)
			}
			for _, reply := range []string{"no\n", "\x03"} {
				out, terminal, err := w.terminal(w.alice, reply, nil, "devices", "--json")
				if err == nil || !bytes.Contains(terminal, []byte("Are you sure you want to continue connecting")) {
					t.Fatalf("declined/interrupted approval must fail after OpenSSH's prompt: %v %s", err, terminal)
				}
				equalBytes(t, nil, out)
				contents, _ := os.ReadFile(known)
				equalBytes(t, nil, contents)
			}
			out, terminal, err := w.terminal(w.alice, "", nil, "__complete", "trust", "")
			if err != nil || len(out) != 0 || len(terminal) != 0 {
				t.Fatalf("completion with a controlling terminal must remain quiet: %v %q %q", err, out, terminal)
			}
			contents, _ := os.ReadFile(known)
			equalBytes(t, nil, contents)
			text := []byte("  first connection 世界 🌍\n\n")
			_, terminal, err = w.terminal(w.alice, "yes\n", text, "send", "--to", "self", "--stdin")
			if err != nil || !bytes.Contains(terminal, []byte("ED25519 key fingerprint is SHA256:")) {
				t.Fatalf("OpenSSH approval with piped text failed: %v %s", err, terminal)
			}
			contents, _ = os.ReadFile(known)
			if len(contents) == 0 {
				t.Fatal("OpenSSH did not remember the approved server key")
			}
			items := w.items(w.alice)
			if len(items) != 1 {
				t.Fatalf("first-connection send: expected one self transfer, got %d", len(items))
			}
			equalBytes(t, text, w.ok(w.alice, nil, "receive", items[0].ID, "--stdout"))
			out, terminal, err = w.terminal(w.alice, "", nil, "devices", "--json")
			if err != nil || len(terminal) != 0 || !json.Valid(out) {
				t.Fatalf("remembered trust must connect without prompting and leave JSON clean: %v %s %s", err, out, terminal)
			}
		}
		hostKey := strings.Fields(string(w.admin("cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
		os.WriteFile(known, []byte(fmt.Sprintf("inbox %s %s\n", hostKey[0], hostKey[1])), 0600)
		w.admin("sh", "-c", "cp /etc/ssh/ssh_host_ed25519_key /tmp/old-host-key && rm /etc/ssh/ssh_host_ed25519_key && ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key")
		w.reloadSSHD()
		w.fails(w.alice, nil, changed, "devices")
		if mode == "openssh" {
			before, _ := os.ReadFile(known)
			out, terminal, err := w.terminal(w.alice, "", nil, "devices")
			if err == nil || !bytes.Contains(terminal, []byte("REMOTE HOST IDENTIFICATION HAS CHANGED")) || !bytes.Contains(terminal, []byte("SOMEONE IS DOING SOMETHING NASTY")) {
				t.Fatalf("changed key must show OpenSSH's native warning: %v %s", err, terminal)
			}
			if bytes.Contains(terminal, []byte("Are you sure you want to continue connecting")) {
				t.Fatalf("changed key offered ordinary first-connect approval: %s", terminal)
			}
			equalBytes(t, nil, out)
			after, _ := os.ReadFile(known)
			equalBytes(t, before, after)
		}
		// Restore trust in the new test host key to isolate authentication failures.
		hostKey = strings.Fields(string(w.admin("cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
		os.WriteFile(known, []byte(fmt.Sprintf("inbox %s %s\n", hostKey[0], hostKey[1])), 0600)
		checkSelectedIdentity(t, w, denied)
	})
}
func checkSelectedIdentity(t *testing.T, w *world, denied string) {
	t.Helper()
	bobPub, _ := os.ReadFile(w.bob.key + ".pub")
	w.put(w.authorizedKeys(), bobPub)
	w.fails(w.alice, nil, denied, "devices")
	if w.mode == "openssh" {
		file, err := os.OpenFile(w.alice.sshConfig, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(w.alice.dir, "other_key")
		private, _ := os.ReadFile(w.bob.key)
		os.WriteFile(other, private, 0600)
		fmt.Fprintf(file, " IdentityFile %s\n", other)
		file.Close()
		w.fails(w.alice, nil, "conflicting SSH identity", "devices")
	}
}

func TestSetupOfflineAndProtectedKeyRejected(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		stub := filepath.Join(w.alice.dir, "stub")
		os.Mkdir(stub, 0700)
		os.WriteFile(filepath.Join(stub, "ssh"), []byte("#!/bin/sh\necho 'setup launched ssh' >&2\nexit 99\n"), 0700)
		w.clientOK(w.alice, nil, "env", "PATH="+stub+":/usr/local/bin:/usr/bin:/bin", "keyparcel", "setup", "--host", "unreachable.invalid", "--identity", w.alice.key, "--config", filepath.Join(w.alice.dir, "offline-config"))
		w.clientOK(w.alice, nil, "ssh-keygen", "-q", "-p", "-P", "", "-N", "test-passphrase", "-f", w.alice.key)
		w.fails(w.alice, nil, "passphrase-protected", "setup", "--host", "protected", "--identity", w.alice.key)
	})
}
func TestPrefixCollisionAndIncompleteUploads(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "hello")
		item := w.items(w.bob)[0]
		remote := w.remote(w.bob, item.ID)
		other := item.ID[:8] + strings.Repeat("0", 24)
		if other == item.ID {
			other = item.ID[:8] + strings.Repeat("1", 24)
		}
		w.put(w.remote(w.bob, other), []byte("corrupted"))
		w.put(strings.TrimSuffix(remote, ".age")+".attempt.part", []byte("unfinished"))
		w.fails(w.bob, nil, "ambiguous", "receive", item.ID[:8], "--stdout")
		w.fails(w.bob, nil, "ambiguous", "delete", item.ID[:8])
		out := w.fails(w.bob, nil, "rejected package", "inbox")
		short := core.ShortID(item.ID, []string{item.ID, other})
		if len(short) < 9 || !strings.Contains(string(out), short) {
			t.Fatal("listing did not extend colliding prefix", string(out))
		}
		w.ok(w.bob, nil, "delete", other)
		equalBytes(t, []byte("hello"), w.ok(w.bob, nil, "receive", item.ID, "--stdout"))
		if len(w.items(w.bob)) != 0 {
			t.Fatal("partial upload was listed")
		}
	})
}
func TestSendChecksDiscoveryOnSameConnection(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		before := string(w.admin("cat", "/home/parcel/.ssh/authorized_keys"))
		logsBefore := w.logs()
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "hello")
		logsAfter := w.logs()
		count := func(b []byte) int { return strings.Count(string(b), "Accepted publickey") }
		if count(logsAfter)-count(logsBefore) != 1 {
			t.Fatalf("send expected one authentication, got %d", count(logsAfter)-count(logsBefore))
		}
		alicePub, _ := os.ReadFile(w.alice.key + ".pub")
		w.put("/home/parcel/.ssh/authorized_keys", alicePub)
		w.fails(w.alice, nil, "not enrolled", "send", "--to", "bob", "--text", "blocked")
		out := w.ok(w.alice, nil, "devices")
		if !strings.Contains(string(out), "Known peers not discovered") {
			t.Fatal("absent approved peer not shown")
		}
		w.put("/home/parcel/.ssh/authorized_keys", []byte(before))
	})
}
func TestIndependentAgeAndOpenSSHCompatibility(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := makeWorld(t, false, "rsa", mode)
		w.approve()
		for _, pair := range []struct {
			sender, recipient client
			name              string
		}{
			{w.alice, w.bob, "bob"}, {w.bob, w.alice, "alice"},
		} {
			sender, recipient := pair.sender, pair.recipient
			w.ok(sender, nil, "send", "--to", pair.name, "--text", "hello")
			item := w.items(recipient)[0]
			cipher := w.admin("cat", w.remote(recipient, item.ID))
			plain, diag, err := w.exec(recipient, cipher, agePath, "--decrypt", "-i", recipient.key)
			if err != nil {
				t.Fatalf("upstream age rejected package: %v %s", err, diag)
			}
			r := bytes.NewReader(plain)
			marker := make([]byte, len(core.Marker))
			io.ReadFull(r, marker)
			if string(marker) != core.Marker {
				t.Fatal("wrong package marker")
			}
			var n uint32
			binary.Read(r, binary.BigEndian, &n)
			manifest := make([]byte, n)
			io.ReadFull(r, manifest)
			binary.Read(r, binary.BigEndian, &n)
			signature := make([]byte, n)
			io.ReadFull(r, signature)
			payload, _ := io.ReadAll(r)
			equalBytes(t, []byte("hello"), payload)
			allowed := filepath.Join(sender.dir, "allowed")
			sigfile := filepath.Join(sender.dir, "signature")
			pub, _ := os.ReadFile(sender.key + ".pub")
			os.WriteFile(allowed, append([]byte("sender "), pub...), 0600)
			os.WriteFile(sigfile, signature, 0600)
			_, diag, err = w.exec(sender, manifest, "ssh-keygen", "-Y", "verify", "-I", "sender", "-f", allowed, "-n", "keyparcel/v1", "-s", sigfile)
			if err != nil {
				t.Fatalf("OpenSSH rejected signature: %v %s", err, diag)
			}
			for _, hash := range []string{"sha256", "sha512"} {
				sig, diag, err := w.exec(sender, manifest, "ssh-keygen", "-Y", "sign", "-f", sender.key, "-n", "keyparcel/v1", "-O", "hashalg="+hash)
				if err != nil {
					t.Fatalf("independent signing: %v %s", err, diag)
				}
				var frame bytes.Buffer
				frame.WriteString(core.Marker)
				binary.Write(&frame, binary.BigEndian, uint32(len(manifest)))
				frame.Write(manifest)
				binary.Write(&frame, binary.BigEndian, uint32(len(sig)))
				frame.Write(sig)
				frame.Write(payload)
				pub, _ := os.ReadFile(recipient.key + ".pub")
				cipher, diag, err := w.exec(recipient, frame.Bytes(), agePath, "--encrypt", "-r", strings.TrimSpace(string(pub)))
				if err != nil {
					t.Fatalf("independent encryption: %v %s", err, diag)
				}
				w.put(w.remote(recipient, item.ID), cipher)
				equalBytes(t, payload, w.ok(recipient, nil, "receive", item.ID, "--stdout", "--keep"))
			}

		}
	})
}

// Construct signed fixtures using independent OpenSSH/age tools, not keyparcel's package writer.
func (w *world) fixture(id string, mutate func(*core.Manifest), invalidSignature bool, recipient client) []byte {
	w.t.Helper()
	hash := sha256.Sum256([]byte("hello"))
	m := core.Manifest{Version: 1, ID: id, Sender: w.alice.fingerprint, Recipients: []string{w.bob.fingerprint}, Type: "text", Size: 5, SHA256: hex.EncodeToString(hash[:])}
	if mutate != nil {
		mutate(&m)
	}
	data, _ := json.Marshal(m)
	signature, diag, err := w.exec(w.alice, data, "ssh-keygen", "-Y", "sign", "-f", w.alice.key, "-n", "keyparcel/v1")
	if err != nil {
		w.t.Fatalf("fixture signing: %v %s", err, diag)
	}
	if invalidSignature {
		signature = []byte("invalid signature")
	}
	var plain bytes.Buffer
	plain.WriteString("KEYPARCEL\x00\x01")
	binary.Write(&plain, binary.BigEndian, uint32(len(data)))
	plain.Write(data)
	binary.Write(&plain, binary.BigEndian, uint32(len(signature)))
	plain.Write(signature)
	plain.WriteString("hello")
	pub, _ := os.ReadFile(recipient.key + ".pub")
	cipher, diag, err := w.exec(recipient, plain.Bytes(), agePath, "--encrypt", "-r", strings.TrimSpace(string(pub)))
	if err != nil {
		w.t.Fatalf("fixture encrypt: %v %s", err, diag)
	}
	return cipher
}
func TestWrongKeySignatureHashRecipientAndPathRejections(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "seed")
		item := w.items(w.bob)[0]
		remote := w.remote(w.bob, item.ID)
		cases := []struct {
			name, reason string
			mutate       func(*core.Manifest)
			badSig       bool
			recipient    client
		}{{"wrong key", "decrypt", nil, false, w.carol}, {"invalid signature", "invalid signature", nil, true, w.bob}, {"wrong hash", "size/hash", func(m *core.Manifest) { m.SHA256 = strings.Repeat("0", 64) }, false, w.bob}, {"wrong size", "size/hash", func(m *core.Manifest) { m.Size = 6 }, false, w.bob}, {"wrong recipient", "intended recipient", func(m *core.Manifest) { m.Recipients = []string{w.carol.fingerprint} }, false, w.bob}, {"ID mismatch", "does not match", func(m *core.Manifest) { m.ID = strings.Repeat("0", 32) }, false, w.bob}, {"path traversal", "invalid filename", func(m *core.Manifest) { m.Type = "file"; name := "../escape"; m.Filename = &name }, false, w.bob}}
		protected := filepath.Join(w.bob.dir, "existing-output.txt")
		if err := os.WriteFile(protected, []byte("existing content"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				w.put(remote, w.fixture(item.ID, tc.mutate, tc.badSig, tc.recipient))
				equalBytes(t, nil, w.fails(w.bob, nil, tc.reason, "receive", item.ID, "--stdout"))
				w.fails(w.bob, nil, tc.reason, "receive", item.ID, "--save-to", protected, "--overwrite")
				got, err := os.ReadFile(protected)
				if err != nil {
					t.Fatal(err)
				}
				equalBytes(t, []byte("existing content"), got)
				w.assertExists(remote, true)
			})
		}
		w.fails(w.bob, nil, "hexadecimal", "delete", "../"+item.ID)
		w.assertExists(remote, true)
	})
}
func TestMetadataReadsStayBounded(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		input := bytes.Repeat([]byte("x"), 8*1024*1024)
		source := filepath.Join(w.alice.dir, "large.bin")
		os.WriteFile(source, input, 0600)
		w.ok(w.alice, nil, "send", "--to", "bob", "--file", source)
		before := w.admin("cat", "/var/log/sftp.log")
		// This instrumentation counts SFTP read packets; command-prefix bounds
		// are checked independently with the actual GNU/BusyBox scripts.
		var items []core.Item
		if err := json.Unmarshal(w.ok(w.bob, nil, "inbox", "--json", "--access-method", "sftp"), &items); err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatal(items)
		}
		after := w.admin("cat", "/var/log/sftp.log")
		newLogs := string(after[len(before):])
		var requested int64
		for _, line := range strings.Split(newLogs, "\n") {
			if strings.Contains(line, "request") && strings.Contains(line, "read ") {
				var offset, length int64
				idx := strings.LastIndex(line, "off ")
				if idx >= 0 {
					fmt.Sscanf(line[idx:], "off %d len %d", &offset, &length)
					requested += length
				}
			}
		}
		if requested == 0 {
			t.Fatalf("no SFTP read instrumentation found:\n%s", newLogs)
		}
		if requested > 256*1024 {
			t.Fatalf("metadata listing requested %d bytes for 8 MiB payload", requested)
		}
	})
}
func TestOutputFailureRetainsRemoteAndCleanupFailurePreservesOutput(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "hello")
		item := w.items(w.bob)[0]
		remote := w.remote(w.bob, item.ID)
		_, diag, err := w.exec(w.bob, nil, "sh", "-c", `keyparcel receive "$1" --stdout --config "$2" --ssh-config "$3" > /dev/full`, "sh", item.ID, w.bob.config, w.bob.sshConfig)
		if err == nil || !strings.Contains(string(diag), "delivery failed") {
			t.Fatalf("expected stdout failure: %v %s", err, diag)
		}
		w.assertExists(remote, true)
		w.admin("chmod", "555", filepath.Dir(remote))
		out := w.fails(w.bob, nil, "delivery succeeded; remote cleanup failed", "receive", item.ID, "--stdout")
		equalBytes(t, []byte("hello"), out)
		w.assertExists(remote, true)
	})
}
func TestEmptyTextAndAgeChunkBoundaries(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		for _, size := range []int{0, 65535, 65536, 65537} {
			input := bytes.Repeat([]byte("x"), size)
			w.ok(w.alice, input, "send", "--to", "bob", "--stdin")
			items := w.items(w.bob)
			if len(items) != 1 {
				t.Fatal(items)
			}
			equalBytes(t, input, w.ok(w.bob, nil, "receive", items[0].ID, "--stdout"))
		}
	})
}
func TestBenchmarkUsesRealServer(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		out := w.ok(w.alice, nil, "bench", "--rounds", "2")
		for _, label := range []string{"fresh SSH/SFTP", "persistent inbox list", "local signed age roundtrip", "persistent verified 5-byte transfer"} {
			if !strings.Contains(string(out), label) {
				t.Fatal("missing measurement", label)
			}
		}
		t.Log(string(out))
	})
}
func TestOversizeMetadataRejected(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "seed")
		item := w.items(w.bob)[0]
		id, _ := core.LoadIdentity(w.bob.key)
		recipient, _ := agessh.NewEd25519Recipient(id.Public)
		var b bytes.Buffer
		enc, err := age.Encrypt(&b, recipient)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(enc, core.Marker)
		binary.Write(enc, binary.BigEndian, uint32(core.MaxMetadata+1))
		enc.Close()
		w.put(w.remote(w.bob, item.ID), b.Bytes())
		equalBytes(t, nil, w.fails(w.bob, nil, "metadata length", "receive", item.ID, "--stdout"))
	})
}

func TestMultipleHostsKeepGlobalTrustAndExplicitDefault(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		for _, c := range []client{w.alice, w.bob} {
			if mode == "openssh" {
				b, err := os.ReadFile(c.sshConfig)
				if err != nil {
					t.Fatal(err)
				}
				b = bytes.Replace(b, []byte("Host parcel-test"), []byte("Host parcel-test second"), 1)
				os.WriteFile(c.sshConfig, b, 0600)
			}
			w.ok(c, nil, "setup", "--host", "second", "--identity", c.key, "--root", ".local/share/keyparcel/second")
		}
		cfg, err := core.LoadConfig(w.alice.config)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DefaultHost != "parcel-test" {
			t.Fatal("adding host changed default")
		}
		var devices []core.Device
		if err = json.Unmarshal(w.ok(w.alice, nil, "devices", "--host", "second", "--json"), &devices); err != nil {
			t.Fatal(err)
		}
		approved := false
		for _, d := range devices {
			if d.Fingerprint == w.bob.fingerprint && d.Trusted && d.Name == "bob" {
				approved = true
			}
		}
		if !approved {
			t.Fatal("approval not recognized across hosts")
		}
		w.ok(w.alice, nil, "send", "--host", "second", "--to", "bob", "--text", "hello")
		if len(w.items(w.bob)) != 0 {
			t.Fatal("explicit host send reached default inbox")
		}
		var items []core.Item
		json.Unmarshal(w.ok(w.bob, nil, "inbox", "--host", "second", "--json"), &items)
		if len(items) != 1 {
			t.Fatal("second inbox missing item")
		}
		equalBytes(t, []byte("hello"), w.ok(w.bob, nil, "receive", items[0].ID, "--host", "second", "--stdout"))
		w.fails(w.alice, nil, "not configured", "devices", "--host", "missing")
		cfg, _ = core.LoadConfig(w.alice.config)
		if cfg.DefaultHost != "parcel-test" {
			t.Fatal("override changed default")
		}
		w.ok(w.alice, nil, "untrust", "bob")
		w.fails(w.alice, nil, "not approved", "send", "--host", "second", "--to", "bob", "--text", "blocked")
		w.ok(w.alice, nil, "setup", "--host", "second", "--identity", w.alice.key, "--default", "--root", ".local/share/keyparcel/second")
		cfg, _ = core.LoadConfig(w.alice.config)
		if cfg.DefaultHost != "second" {
			t.Fatal("explicit default not saved")
		}
	})
}
func TestPartialRecipientFailurePreservesCompletedDelivery(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		w.ok(w.alice, nil, "trust", w.carol.fingerprint, "--name", "carol")
		w.ok(w.carol, nil, "trust", w.alice.fingerprint, "--name", "alice")
		carolID, _ := core.LoadIdentity(w.carol.key)
		dir := "/home/parcel/.local/share/keyparcel/inbox/" + core.DeviceID(carolID.Public)
		w.admin("mkdir", "-p", dir)
		w.admin("chown", "-R", "parcel:parcel", "/home/parcel/.local")
		w.admin("chmod", "555", dir)
		out := w.fails(w.alice, nil, "delivery failed", "send", "--to", "bob", "--to", "carol", "--text", "hello")
		if !strings.Contains(string(out), "bob: stored") {
			t.Fatal("completed recipient outcome missing")
		}
		items := w.items(w.bob)
		if len(items) != 1 {
			t.Fatal("completed delivery was discarded")
		}
		if len(w.items(w.carol)) != 0 {
			t.Fatal("failed delivery became an inbox item")
		}
		equalBytes(t, []byte("hello"), w.ok(w.bob, nil, "receive", items[0].ID, "--stdout"))
	})
}
func TestPublishRenameRefusesExistingPackage(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "original")
		item := w.items(w.bob)[0]
		remote := w.remote(w.bob, item.ID)
		original := w.admin("cat", remote)
		tmp := remote + ".attempt.part"
		w.put(tmp, []byte("replacement"))
		_, diag, err := w.exec(w.alice, nil, "sftp-probe", w.alice.config, w.alice.sshConfig, tmp, remote)
		if err == nil || !strings.Contains(string(diag), "publish rename:") {
			t.Fatalf("expected SFTP publish rename rejection: %v %s", err, diag)
		}
		equalBytes(t, original, w.admin("cat", remote))
		w.assertExists(tmp, true)
	})
}
func TestAgentAlternativeAndMultiplexCannotBypassSelectedKey(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "openssh")
	sock := filepath.Join(w.alice.dir, "agent.sock")
	_, diag, err := container(w.ctx, nil, "exec", "-d", w.alice.id, "ssh-agent", "-D", "-a", sock)
	if err != nil {
		t.Fatalf("test agent: %v %s", err, diag)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test agent did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	other := filepath.Join(w.alice.dir, "other_key")
	private, _ := os.ReadFile(w.bob.key)
	os.WriteFile(other, private, 0600)
	w.clientOK(w.alice, nil, "env", "SSH_AUTH_SOCK="+sock, "ssh-add", other)
	cfg, err := os.ReadFile(w.alice.sshConfig)
	if err != nil {
		t.Fatal(err)
	}
	cfg = bytes.Replace(cfg, []byte("IdentityAgent none"), []byte("IdentityAgent "+sock), 1)
	os.WriteFile(w.alice.sshConfig, cfg, 0600)
	bobPub, _ := os.ReadFile(w.bob.key + ".pub")
	w.put("/home/parcel/.ssh/authorized_keys", bobPub)
	_, diag, err = w.exec(w.alice, nil, "ssh", "-F", w.alice.sshConfig, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=no", "parcel-test", "true")
	if err != nil {
		t.Fatalf("alternative key fixture cannot authenticate: %v %s", err, diag)
	}
	w.fails(w.alice, nil, "Permission denied", "devices")
	master := filepath.Join(w.alice.dir, "master.sock")
	masterConfig := filepath.Join(w.alice.dir, "master_config")
	os.WriteFile(masterConfig, bytes.Replace(cfg, []byte(w.alice.key), []byte(other), 1), 0600)
	_, diag, err = w.exec(w.alice, nil, "ssh", "-F", masterConfig, "-M", "-N", "-f", "-S", master, "parcel-test")
	if err != nil {
		t.Fatalf("test master connection: %v %s", err, diag)
	}
	cfg = append(cfg, []byte(" ControlMaster auto\n ControlPath "+master+"\n")...)
	os.WriteFile(w.alice.sshConfig, cfg, 0600)
	w.fails(w.alice, nil, "Permission denied", "devices")
}
func TestIdentityChangedAndRemoteCommentDoesNotApproveReplacement(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		aliceKey, _ := os.ReadFile(w.alice.key)
		bobKey, _ := os.ReadFile(w.bob.key)
		os.WriteFile(w.alice.key, bobKey, 0600)
		w.fails(w.alice, nil, "fingerprint changed", "devices")
		os.WriteFile(w.alice.key, aliceKey, 0600)
		alicePub, _ := os.ReadFile(w.alice.key + ".pub")
		carolPub, _ := os.ReadFile(w.carol.key + ".pub")
		parts := strings.Fields(string(carolPub))
		replacement := []byte(parts[0] + " " + parts[1] + " bob\n")
		keys := append([]byte("# device discovery\n\n"), alicePub...)
		keys = append(keys, replacement...)
		keys = append(keys, []byte("\n# trailing comment\n")...)
		w.put(w.authorizedKeys(), keys)
		var devices []core.Device
		json.Unmarshal(w.ok(w.alice, nil, "devices", "--json"), &devices)
		for _, d := range devices {
			if d.Fingerprint == w.carol.fingerprint && d.Trusted {
				t.Fatal("same remote comment established trust")
			}
			if d.Fingerprint == w.bob.fingerprint && (!d.Trusted || d.Discovered) {
				t.Fatal("missing approved key lost approval or remained discovered")
			}
		}
		w.fails(w.alice, nil, "not enrolled", "send", "--to", "bob", "--text", "blocked")
		w.fails(w.alice, nil, "already approved", "trust", w.carol.fingerprint, "--name", "bob")
	})
}
func TestInterruptedUploadNeverPublishesPartialPackage(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		source := filepath.Join(w.alice.dir, "large.bin")
		f, err := os.Create(source)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(64 * 1024 * 1024); err != nil {
			t.Fatal(err)
		}
		f.Close()
		op := w.start(w.alice, "send", "--to", "bob", "--file", source)
		deadline := time.Now().Add(15 * time.Second)
		found := false
		for time.Now().Before(deadline) {
			paths, _, e := container(w.ctx, nil, "exec", w.id, "find", "/home/parcel/.local/share/keyparcel", "-name", "*.part")
			if e == nil && len(bytes.TrimSpace(paths)) > 0 {
				found = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !found {
			logs, _ := os.ReadFile(op.path + ".log")
			t.Fatalf("did not observe in-progress upload: %s", logs)
		}
		w.interrupt(op)
		if w.wait(op) == 0 {
			t.Fatal("interrupted sender reported success")
		}
		if len(w.items(w.bob)) != 0 {
			t.Fatal("interrupted upload published a completed package")
		}
	})
}
func startReusedWatch(t *testing.T, w *world) operation {
	t.Helper()
	before := strings.Count(string(w.logs()), "Accepted publickey")
	op := w.start(w.alice, "inbox", "--watch", "--interval", "100ms")
	logPath := op.path + ".log"
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(logPath)
		if strings.Count(string(b), "Inbox empty.") >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watch did not refresh")
		}
		time.Sleep(20 * time.Millisecond)
	}
	after := strings.Count(string(w.logs()), "Accepted publickey")
	if after-before != 1 {
		t.Fatalf("three polls expected one authentication, got %d", after-before)
	}
	return op
}

func TestWatchReusesConnectionAndRechecksConfigurationOnReconnect(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		op := startReusedWatch(t, w)
		logPath := op.path + ".log"
		rejection := "conflicting SSH identity"
		if mode == "openssh" {
			cfg, err := os.OpenFile(w.alice.sshConfig, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(w.alice.dir, "other_key")
			private, _ := os.ReadFile(w.bob.key)
			os.WriteFile(other, private, 0600)
			fmt.Fprintf(cfg, " IdentityFile %s\n", other)
			cfg.Close()
		} else {
			private, err := os.ReadFile(w.bob.key)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(w.alice.key, private, 0600); err != nil {
				t.Fatal(err)
			}
			rejection = "fingerprint changed"
		}

		w.admin("pkill", "-u", "parcel")
		deadline := time.Now().Add(5 * time.Second)
		for {
			b, _ := os.ReadFile(logPath)
			if strings.Contains(string(b), rejection) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("reconnect did not recheck configuration:\n%s", b)
			}
			time.Sleep(20 * time.Millisecond)
		}
		w.interrupt(op)
		if status := w.wait(op); status != 0 {
			t.Fatalf("watch cancellation: status %d", status)
		}
	})
}

func TestCopyUsesRealIsolatedX11Clipboard(t *testing.T) {
	t.Parallel()
	w := newClipboardWorld(t)
	w.approve()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, _, err := w.exec(w.bob, nil, "test", "-S", "/tmp/.X11-unix/X99")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated X server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	checkClipboardTransfers(t, w, func() []byte {
		out, diag, err := w.exec(w.bob, nil, "xclip", "-selection", "clipboard", "-out")
		if err != nil {
			t.Fatalf("real clipboard read: %v %s", err, diag)
		}
		return out
	})
}

func checkClipboardTransfers(t *testing.T, w *world, read func() []byte) {
	t.Helper()
	input := []byte("  copy 世界\n\n")
	w.ok(w.alice, input, "send", "--to", "bob", "--stdin")
	item := w.items(w.bob)[0]
	w.ok(w.bob, nil, "receive", item.ID, "--copy", "--keep")
	out := read()
	equalBytes(t, input, out)
	w.assertExists(w.remote(w.bob, item.ID), true)
	w.ok(w.bob, nil, "receive", item.ID, "--copy")
	out = read()
	equalBytes(t, input, out)
	w.assertExists(w.remote(w.bob, item.ID), false)
	source := filepath.Join(w.alice.dir, "file.txt")
	fileText := []byte("  file content 世界\n\n")
	os.WriteFile(source, fileText, 0600)
	w.ok(w.alice, nil, "send", "--to", "bob", "--file", source)
	item = w.items(w.bob)[0]
	w.ok(w.bob, nil, "receive", item.ID, "--copy")
	out = read()
	equalBytes(t, fileText, out)
	w.assertExists(w.remote(w.bob, item.ID), false)
	for _, tc := range []struct {
		data   []byte
		reason string
	}{
		{[]byte{0xff}, "valid UTF-8"},
		{[]byte("text\x00binary"), "without NUL"},
		{bytes.Repeat([]byte("a"), 16*1024*1024+1), "16 MiB"},
	} {
		os.WriteFile(source, tc.data, 0600)
		w.ok(w.alice, nil, "send", "--to", "bob", "--file", source)
		item = w.items(w.bob)[0]
		w.fails(w.bob, nil, tc.reason, "receive", item.ID, "--copy")
		w.assertExists(w.remote(w.bob, item.ID), true)
		out = read()
		equalBytes(t, fileText, out)
		w.ok(w.bob, nil, "delete", item.ID)
	}
	// Large text still streams through stdin/stdout; only clipboard operations are capped.
	large := bytes.Repeat([]byte("a"), 16*1024*1024+1)
	w.ok(w.alice, large, "send", "--to", "bob", "--stdin")
	item = w.items(w.bob)[0]
	w.fails(w.bob, nil, "16 MiB", "receive", item.ID, "--copy")
	w.assertExists(w.remote(w.bob, item.ID), true)
	equalBytes(t, large, w.ok(w.bob, nil, "receive", item.ID, "--stdout"))
	w.assertExists(w.remote(w.bob, item.ID), false)
}

func TestJumpHostUsesSeparateLocalKey(t *testing.T) {
	t.Parallel()
	targetWorld := newWorld(t, "openssh")
	targetWorld.approve()
	gatewayWorld := newWorld(t, "openssh")
	gatewayWorld.admin("sed", "-i", "s/DisableForwarding yes/DisableForwarding no/", "/etc/ssh/sshd_config")
	gatewayWorld.reloadSSHD()
	_, diag, err := container(targetWorld.ctx, nil, "network", "connect", "--alias", "gateway", targetWorld.network, gatewayWorld.id)
	if err != nil {
		t.Fatalf("connect gateway network: %v %s", err, diag)
	}
	ip := strings.TrimSpace(string(targetWorld.admin("hostname", "-i")))
	original, err := os.ReadFile(targetWorld.alice.sshConfig)
	if err != nil {
		t.Fatal(err)
	}
	original = bytes.Replace(original, []byte("HostName inbox"), []byte("HostName "+ip), 1)
	if err = os.WriteFile(targetWorld.alice.sshConfig, original, 0600); err != nil {
		t.Fatal(err)
	}
	gatewayKey := filepath.Join(targetWorld.alice.dir, "gateway_key")
	private, _ := os.ReadFile(gatewayWorld.alice.key)
	os.WriteFile(gatewayKey, private, 0600)
	knownPath := filepath.Join(targetWorld.alice.dir, "known_hosts")
	known, err := os.OpenFile(knownPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	targetPub := strings.Fields(string(targetWorld.admin("cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
	fmt.Fprintf(known, "%s %s %s\n", ip, targetPub[0], targetPub[1])
	known.Close()
	cfg, err := os.OpenFile(targetWorld.alice.sshConfig, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(cfg, " ProxyJump gateway\nHost gateway\n HostName gateway\n Port 22\n User parcel\n IdentityFile %s\n IdentitiesOnly yes\n IdentityAgent none\n UserKnownHostsFile %s\n GlobalKnownHostsFile /dev/null\n", gatewayKey, knownPath)
	cfg.Close()
	// Regression: the gateway keeps normal prompting defaults for this background job.
	before, err := os.ReadFile(knownPath)
	if err != nil {
		t.Fatal(err)
	}
	backgroundError := filepath.Join(targetWorld.alice.dir, "gateway-background.stderr")
	background := "keyparcel devices --config " + shellQuote(targetWorld.alice.config) + " --ssh-config " + shellQuote(targetWorld.alice.sshConfig) + " 2> " + shellQuote(backgroundError) + " & wait $!"
	terminal, diag, err := targetWorld.exec(targetWorld.alice, nil, "timeout", "-k", "1s", "5s", "script", "-qefc", "bash -mc "+shellQuote(background), "/dev/null")
	terminal = append(terminal, diag...)
	diagnostic, readErr := os.ReadFile(backgroundError)
	if readErr != nil {
		t.Fatalf("background jump-host diagnostic: %v; terminal: %s", readErr, terminal)
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("background jump-host command must fail normally, without suspension or timeout: %v; terminal: %s; diagnostic: %s", err, terminal, diagnostic)
	}
	if !bytes.Contains(diagnostic, []byte("Host key verification failed")) || bytes.Contains(terminal, []byte("Are you sure")) || bytes.Contains(diagnostic, []byte("Are you sure")) {
		t.Fatalf("unknown background gateway must fail without prompting: terminal: %s; diagnostic: %s", terminal, diagnostic)
	}
	after, err := os.ReadFile(knownPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("background gateway failure changed known_hosts: %v; before: %q; after: %q", err, before, after)
	}
	known, err = os.OpenFile(knownPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	pub := strings.Fields(string(gatewayWorld.admin("cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
	fmt.Fprintf(known, "gateway %s %s\n", pub[0], pub[1])
	known.Close()
	targetWorld.ok(targetWorld.alice, nil, "send", "--to", "bob", "--text", "through gateway")
	items := targetWorld.items(targetWorld.bob)
	if len(items) != 1 {
		t.Fatal("jump-host transfer missing")
	}
	equalBytes(t, []byte("through gateway"), targetWorld.ok(targetWorld.bob, nil, "receive", items[0].ID, "--stdout"))
	if !strings.Contains(string(gatewayWorld.logs()), "Accepted publickey for parcel") || !strings.Contains(string(gatewayWorld.logs()), gatewayWorld.alice.fingerprint) {
		t.Fatal("gateway did not authenticate with its separate local key")
	}
	if !strings.Contains(string(targetWorld.logs()), targetWorld.alice.fingerprint) {
		t.Fatal("destination did not authenticate with selected device key")
	}
}

func TestServerUmaskControlsNewRemotePermissions(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		w.admin("sed", "-i", "s/LOCAL0/LOCAL0 -u 0077/", "/etc/ssh/sshd_config")
		w.reloadSSHD()
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "hello")
		item := w.items(w.bob)[0]
		remote := w.remote(w.bob, item.ID)
		for _, tc := range []struct{ file, mode string }{{remote, "600"}, {filepath.Dir(remote), "700"}, {"/home/parcel/.local/share/keyparcel", "700"}} {
			actual := strings.TrimSpace(string(w.admin("stat", "-c", "%a", tc.file)))
			mode, err := strconv.ParseUint(actual, 8, 32)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := strconv.ParseUint(tc.mode, 8, 32)
			if err != nil {
				t.Fatal(err)
			}
			// The server may inherit setgid from its parent; check only permission bits.
			if mode&0777 != expected {
				t.Fatalf("server umask: %s: expected permissions %s, got mode %s", tc.file, tc.mode, actual)
			}
		}
	})
}

func TestPersonalAgentEnvironmentIsExcluded(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "openssh")
	for _, c := range w.clients {
		env := string(w.clientOK(c, nil, "env"))
		for _, peer := range w.clients {
			if peer.id != c.id {
				w.clientOK(c, nil, "test", "!", "-e", peer.key)
			}
		}
		for _, key := range []string{"SSH_AUTH_SOCK=", "SSH_AGENT_PID=", "WAYLAND_DISPLAY=", "DISPLAY="} {
			if strings.Contains(env, key) {
				t.Fatalf("personal environment leaked: %s", key)
			}
		}
		mounts, diag, err := container(w.ctx, nil, "inspect", "--format", "{{json .Mounts}}", c.id)
		if err != nil {
			t.Fatalf("inspect isolation: %v %s", err, diag)
		}
		var entries []struct{ Source string }
		if err = json.Unmarshal(mounts, &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Source != c.dir {
			t.Fatalf("unexpected client mounts: %s", mounts)
		}
	}
}

func TestInboxTimesAndBatchDeletion(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		w.ok(w.alice, nil, "trust", w.carol.fingerprint, "--name", "carol")
		w.ok(w.carol, nil, "trust", w.alice.fingerprint, "--name", "alice")
		for i := 0; i < 3; i++ {
			w.ok(w.alice, nil, "send", "--to", "bob", "--to", "carol", "--text", "same content")
		}
		items := w.items(w.bob)
		if len(items) != 3 {
			t.Fatal(items)
		}
		ids := []string{items[0].ID, items[1].ID, items[2].ID}
		sort.Strings(ids)
		old := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
		newest := old.Add(time.Hour)
		w.admin("touch", "-t", "202610111000.00", w.remote(w.bob, ids[0]))
		w.admin("touch", "-t", "202610111100.00", w.remote(w.bob, ids[1]), w.remote(w.bob, ids[2]))
		raw := w.ok(w.bob, nil, "inbox", "--json")
		if !bytes.Contains(raw, []byte(`"uploaded_at":"2026-10-11T11:00:00Z"`)) {
			t.Fatalf("timestamp not UTC RFC3339: %s", raw)
		}
		items = w.items(w.bob)
		if items[0].ID != ids[1] || items[1].ID != ids[2] || items[2].ID != ids[0] || !items[0].UploadedAt.Equal(newest) || !items[2].UploadedAt.Equal(old) {
			t.Fatalf("timestamp ordering: %+v", items)
		}
		table := string(w.clientOK(w.bob, nil, "env", "TZ=UTC", "keyparcel", "inbox", "--config", w.bob.config, "--ssh-config", w.bob.sshConfig))
		if !strings.Contains(table, "UPLOADED") || !strings.Contains(table, "2026-10-11 11:00:00 +00:00") || strings.Index(table, ids[1][:8]) > strings.Index(table, ids[0][:8]) {
			t.Fatal("human timestamp/order", table)
		}
		out := w.fails(w.bob, nil, "invalid", "delete", "invalid", ids[0], ids[0][:8], strings.Repeat("f", 32))
		if string(out) != "Deleted 1 transfer(s)\n" {
			t.Fatalf("partial/deduplicated result: %s", out)
		}
		w.assertExists(w.remote(w.bob, ids[0]), false)
		// An undeletable entry must not block remaining valid deletions.
		blocked := strings.Repeat("b", 32)
		w.admin("mkdir", w.remote(w.bob, blocked))
		w.put(w.remote(w.bob, blocked)+"/child", []byte("block directory removal"))
		inbox := filepath.Dir(w.remote(w.bob, ids[1]))
		w.admin("chmod", "u-r", inbox)
		w.fails(w.bob, nil, "permission denied", "delete", ids[1][:8])
		w.assertExists(w.remote(w.bob, ids[1]), true)
		out = w.fails(w.bob, nil, "deletion failed", "delete", blocked, ids[1])
		if string(out) != "Deleted 1 transfer(s)\n" {
			t.Fatal("deletion did not continue", string(out))
		}
		w.assertExists(w.remote(w.bob, ids[1]), false)
		w.assertExists(w.remote(w.bob, ids[2]), true)
		w.admin("chmod", "u+r", inbox)
		w.admin("rm", "-r", w.remote(w.bob, blocked))
		// Ambiguous prefixes select nothing, while a valid full ID still deletes.
		other := ids[2][:8] + strings.Repeat("0", 24)
		if other == ids[2] {
			other = ids[2][:8] + strings.Repeat("1", 24)
		}
		w.put(w.remote(w.bob, other), []byte("corrupt"))
		out = w.fails(w.bob, nil, "ambiguous", "delete", ids[2][:8], ids[2])
		if string(out) != "Deleted 1 transfer(s)\n" {
			t.Fatal("ambiguous partial result", string(out))
		}
		w.assertExists(w.remote(w.bob, other), true)
		part := w.remote(w.bob, ids[0]) + ".attempt.part"
		unrelated := filepath.Dir(part) + "/notes"
		w.put(part, []byte("unfinished"))
		w.put(unrelated, []byte("unrelated"))
		// Include an intact unapproved package, plus identical-content transfers.
		w.ok(w.carol, nil, "trust", w.bob.fingerprint, "--name", "bob")
		w.ok(w.carol, nil, "send", "--to", "bob", "--text", "unapproved")
		for i := 0; i < 2; i++ {
			w.ok(w.alice, nil, "send", "--to", "bob", "--text", "same content")
		}
		out = w.ok(w.bob, nil, "delete", "--all")
		if string(out) != "Deleted 4 transfer(s)\n" {
			t.Fatal("all count", string(out))
		}
		w.assertExists(part, true)
		w.assertExists(unrelated, true)
		if len(w.items(w.carol)) != 3 {
			t.Fatal("deleted another device's inbox")
		}
		if string(w.ok(w.bob, nil, "delete", "--all")) != "Deleted 0 transfer(s)\n" {
			t.Fatal("empty deletion count")
		}
	})
}

func TestInboxCommandBatchKeepsVerificationAndSFTPFallback(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		w.approve()
		text := []byte("inbox command metadata — 世界\n")
		w.ok(w.alice, text, "send", "--to", "bob", "--stdin")
		file := filepath.Join(w.alice.dir, "binary.bin")
		data := []byte{0, 255, 10, 128, 32}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		w.ok(w.alice, nil, "send", "--to", "bob", "--file", file)
		baseline := w.ok(w.bob, nil, "inbox", "--json", "--access-method", "sftp")
		beforeAuth := bytes.Count(w.logs(), []byte("Accepted publickey for parcel"))
		auto := w.ok(w.bob, nil, "inbox", "--json")
		if got := bytes.Count(w.logs(), []byte("Accepted publickey for parcel")); got != beforeAuth+1 {
			t.Fatalf("inbox command opened extra authenticated TCP: %d -> %d", beforeAuth, got)
		}
		equalBytes(t, baseline, auto)
		var items []core.Item
		if err := json.Unmarshal(auto, &items); err != nil || len(items) != 2 {
			t.Fatalf("expected both verified parcels: %s %v", auto, err)
		}
		var textID, fileID string
		for _, item := range items {
			if item.Type == "text" {
				textID = item.ID
			} else {
				fileID = item.ID
			}
		}
		if textID == "" || fileID == "" {
			t.Fatal("text/file metadata missing")
		}
		w.ok(w.bob, nil, "untrust", "alice")
		equalBytes(t, []byte("[]\n"), w.fails(w.bob, nil, "unapproved sender", "inbox", "--json"))
		w.fails(w.bob, nil, "unapproved sender", "receive", textID, "--stdout")
		w.assertExists(w.remote(w.bob, textID), true)
		w.ok(w.bob, nil, "trust", w.alice.fingerprint, "--name", "alice")
		equalBytes(t, baseline, w.ok(w.bob, nil, "inbox", "--json"))
		equalBytes(t, text, w.ok(w.bob, nil, "receive", textID, "--stdout", "--keep"))
		equalBytes(t, data, w.ok(w.bob, nil, "receive", fileID, "--stdout", "--keep"))
		w.ok(w.bob, nil, "send", "--to", "self", "--text", "command cleanup")
		cleanupID := ""
		for _, item := range w.items(w.bob) {
			if item.From == "self" {
				cleanupID = item.ID
			}
		}
		if cleanupID == "" {
			t.Fatal("self cleanup parcel missing")
		}
		equalBytes(t, []byte("command cleanup"), w.ok(w.bob, nil, "receive", cleanupID, "--stdout"))
		w.assertExists(w.remote(w.bob, cleanupID), false)
		w.assertExists(w.remote(w.bob, textID), true)
		w.assertExists(w.remote(w.bob, fileID), true)
		// CLI Auto requires commands; explicit SFTP remains available on restricted accounts.
		w.admin("sh", "-c", "printf '\nForceCommand internal-sftp\n' >> /etc/ssh/sshd_config")
		w.reloadSSHD()
		w.fails(w.bob, nil, "--access-method sftp", "inbox", "--json")
		equalBytes(t, baseline, w.ok(w.bob, nil, "inbox", "--json", "--access-method", "sftp"))
		equalBytes(t, text, w.ok(w.bob, nil, "receive", textID, "--stdout", "--access-method", "sftp"))
		w.assertExists(w.remote(w.bob, textID), false)
		w.assertExists(w.remote(w.bob, fileID), true)
		equalBytes(t, data, w.ok(w.bob, nil, "receive", fileID, "--stdout", "--keep", "--access-method", "sftp"))
		w.ok(w.bob, nil, "delete", fileID)
		w.assertExists(w.remote(w.bob, fileID), false)
	})
}
