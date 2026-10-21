//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
)

// One server lifecycle covers completion's real-data and failure behavior.
func TestShellCompletionScenario(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := makeWorld(t, false, "rsa", mode)
		// Run the same real-data assertions through both native wrappers.
		query := func(c client, args ...string) string {
			words := append([]string{args[0], "--config", c.config, "--ssh-config", c.sshConfig}, args[1:]...)
			result := strings.TrimSpace(string(w.clientOK(c, nil, append([]string{"keyparcel", "__complete"}, words...)...)))
			for _, shell := range []string{"bash", "zsh"} {
				body := `source /usr/local/share/keyparcel/completion.bash || exit
compopt() { :; }
COMP_WORDS=(keyparcel "$@"); COMP_CWORD=$((${#COMP_WORDS[@]}-1))
_keyparcel
for candidate in "${COMPREPLY[@]}"; do eval "set -- $candidate"; printf '%s\n' "$1"; done`
				if shell == "zsh" {
					body = `[[ -r /usr/share/zsh/site-functions/_keyparcel ]] || exit 2
fpath=(/usr/share/zsh/site-functions $fpath)
autoload -Uz _keyparcel
words=(keyparcel "$@"); CURRENT=${#words}
compadd() { [[ $1 == -V && $2 == keyparcel && $3 == -- ]] || return 2; shift 3; print -rl -- "$@"; }
_keyparcel || [[ $? == 1 ]]`
				}
				args := []string{shell, "-f", "-c", body, shell}
				if shell == "bash" {
					args = []string{shell, "--noprofile", "--norc", "-c", body, shell}
				}
				args = append(args, words...)
				started := time.Now()
				out, diag, err := w.exec(c, nil, args...)
				if err != nil {
					t.Fatalf("%s wrapper failed: %v %s", shell, err, diag)
				}
				got := strings.TrimSpace(string(out))
				// Five-second query deadline plus the existing process allowance, per shell.
				if time.Since(started) > 8*time.Second {
					t.Fatalf("%s completion did not return promptly", shell)
				}
				if got != result {
					t.Fatalf("%s completion %q: got %q, helper %q diagnostics %s", shell, words, got, result, diag)
				}
			}
			return result
		}
		initialConfig, _ := os.ReadFile(w.alice.config)
		unsupported := filepath.Join(w.alice.dir, "unsupported_key")
		w.clientOK(w.alice, nil, "ssh-keygen", "-q", "-t", "ecdsa", "-b", "256", "-N", "", "-f", unsupported)
		unsupportedPub, _ := os.ReadFile(unsupported + ".pub")
		weak := filepath.Join(w.alice.dir, "weak_rsa")
		w.clientOK(w.alice, nil, "ssh-keygen", "-q", "-t", "rsa", "-b", "1024", "-N", "", "-f", weak)
		weakPub, err := os.ReadFile(weak + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		weakKey, _, _, _, err := ssh.ParseAuthorizedKey(weakPub)
		if err != nil {
			t.Fatal(err)
		}
		enrolled := w.admin("cat", "/home/parcel/.ssh/authorized_keys")
		w.put("/home/parcel/.ssh/authorized_keys", append(append(enrolled, unsupportedPub...), weakPub...))
		w.fails(w.alice, nil, "RSA key size is too small", "trust", ssh.FingerprintSHA256(weakKey), "--name", "weak")
		afterRejection, err := os.ReadFile(w.alice.config)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(initialConfig, afterRejection) {
			t.Fatal("weak RSA approval changed configuration")
		}
		got := query(w.alice, "trust", "")
		if len(strings.Split(got, "\n")) != 2 {
			t.Fatalf("unsupported key suggested: %s", got)
		}
		if !strings.Contains(got, w.bob.fingerprint) || !strings.Contains(got, w.carol.fingerprint) || strings.Contains(got, w.alice.fingerprint) {
			t.Fatalf("expected RSA Bob and Ed25519 Carol fingerprint candidates: %s", got)
		}
		if strings.Contains(got, ssh.FingerprintSHA256(weakKey)) {
			t.Fatal("weak RSA key suggested for approval", got)
		}
		for _, peer := range []struct{ fingerprint, name string }{{w.bob.fingerprint, "bob"}, {w.carol.fingerprint, "carol"}} {
			if got = query(w.alice, "trust", peer.fingerprint, ""); got != "--name" {
				t.Fatalf("next flag for %s: %q", peer.name, got)
			}
			if got = query(w.alice, "trust", peer.fingerprint, "--name", ""); got != peer.name {
				t.Fatalf("name suggestion for %s: %q", peer.name, got)
			}
		}
		// Exercise the actual Bash wrapper, including split unquoted fingerprint words.
		bashQuery := func(words []string, index string) string {
			args := []string{"bash", "-c", `source /usr/local/share/keyparcel/completion.bash; COMP_CWORD=$1; shift; COMP_WORDS=("$@"); _keyparcel; printf '%s\n' "${COMPREPLY[@]}"`, "sh", index}
			args = append(args, words...)
			return strings.TrimSpace(string(w.clientOK(w.alice, nil, args...)))
		}
		base := []string{"keyparcel", "trust", "--config", w.alice.config, "--ssh-config", w.alice.sshConfig}
		if got = bashQuery(append(append([]string{}, base...), w.bob.fingerprint, ""), "7"); got != "--name" {
			t.Fatalf("Bash next flag: %s", got)
		}
		parts := strings.SplitN(w.bob.fingerprint, ":", 2)
		if got = bashQuery(append(append([]string{}, base...), parts[0], ":", parts[1][:6]), "8"); got != parts[1] {
			t.Fatalf("Bash colon insertion: %s", got)
		}
		// Remote comments cannot become shell commands or invalid local names.
		bobPub, _ := os.ReadFile(w.bob.key + ".pub")
		alicePub, _ := os.ReadFile(w.alice.key + ".pub")
		carolPub, _ := os.ReadFile(w.carol.key + ".pub")
		bobFields := strings.Fields(string(bobPub))
		marker := filepath.Join(w.alice.dir, "injected")
		for _, comment := range []string{"self", "bad name", "$(touch " + marker + ")"} {
			keys := append(append([]byte{}, alicePub...), []byte(bobFields[0]+" "+bobFields[1]+" "+comment+"\n")...)
			keys = append(keys, carolPub...)
			w.put("/home/parcel/.ssh/authorized_keys", keys)
			if got = query(w.alice, "trust", w.bob.fingerprint, "--name", ""); got != "" {
				t.Fatalf("unsafe name suggested: %q", got)
			}
		}
		// A syntactically valid name containing shell substitution must be inserted literally.
		malicious := "$(touch${IFS}" + marker + ")"
		unsafeKeys := append(append([]byte{}, alicePub...), []byte(bobFields[0]+" "+bobFields[1]+" "+malicious+"\n")...)
		unsafeKeys = append(unsafeKeys, carolPub...)
		w.put("/home/parcel/.ssh/authorized_keys", unsafeKeys)
		quoted := bashQuery(append(append([]string{}, base...), w.bob.fingerprint, "--name", ""), "8")
		inserted := w.clientOK(w.alice, nil, "bash", "-c", `candidate=$1; eval "set -- $candidate"; printf '%s' "$1"`, "sh", quoted)
		if string(inserted) != malicious {
			t.Fatalf("name quoting: %q", inserted)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("comment executed")
		}
		keys := append(append(append([]byte{}, alicePub...), bobPub...), carolPub...)
		w.put("/home/parcel/.ssh/authorized_keys", keys)
		afterConfig, _ := os.ReadFile(w.alice.config)
		equalBytes(t, initialConfig, afterConfig)
		w.ok(w.alice, nil, "trust", w.carol.fingerprint, "--name", "bob")
		if got = query(w.alice, "trust", w.bob.fingerprint, "--name", ""); got != "" {
			t.Fatal("taken name suggested", got)
		}
		w.ok(w.alice, nil, "untrust", "bob")
		w.approve()
		if got = query(w.alice, "trust", ""); strings.Contains(got, w.bob.fingerprint) {
			t.Fatal("approved key suggested")
		}
		if got = query(w.alice, "send", "--to", ""); got != "bob\nself" {
			t.Fatal(got)
		}
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "completion")
		item := w.items(w.bob)[0]
		if got = query(w.bob, "receive", ""); got != item.ID {
			t.Fatal(got)
		}
		if got = query(w.bob, "delete", ""); got != item.ID {
			t.Fatal(got)
		}
		w.ok(w.alice, nil, "send", "--to", "bob", "--text", "second completion")
		incoming := w.items(w.bob)
		var secondID string
		for _, candidate := range incoming {
			if candidate.ID != item.ID {
				secondID = candidate.ID
			}
		}
		if secondID == "" {
			t.Fatal("second completion transfer missing")
		}
		for _, selected := range []string{item.ID, item.ID[:8]} {
			if got = query(w.bob, "delete", selected, ""); got != secondID {
				t.Fatalf("selected ID not excluded: %s", got)
			}
		}
		if got = query(w.bob, "delete", item.ID, secondID, ""); got != "" {
			t.Fatalf("selected IDs suggested: %s", got)
		}
		if got = query(w.bob, "delete", "--all", ""); got != "" {
			t.Fatalf("IDs suggested with --all: %s", got)
		}
		wantIDs := []string{item.ID, secondID}
		sort.Strings(wantIDs)
		for _, value := range []string{"0", "f", "F", "false", "False", "FALSE"} {
			if got = query(w.bob, "delete", "--all="+value, ""); got != strings.Join(wantIDs, "\n") {
				t.Fatalf("false --all=%s suppressed IDs: %s", value, got)
			}
		}
		if got = query(w.bob, "delete", "invalid", ""); got != strings.Join(wantIDs, "\n") {
			t.Fatalf("invalid argument excluded an ID: %s", got)
		}
		// Filename suggestions neither approve senders nor open parcel contents.
		w.ok(w.bob, nil, "untrust", "alice")
		if got = query(w.bob, "receive", ""); got != strings.Join(wantIDs, "\n") {
			t.Fatalf("receive completion required sender approval: %q", got)
		}
		if out := w.fails(w.bob, nil, "unapproved sender", "receive", item.ID, "--stdout"); len(out) != 0 {
			t.Fatal("suggested unapproved parcel exposed plaintext", string(out))
		}
		w.assertExists(w.remote(w.bob, item.ID), true)
		w.admin("chmod", "u-r", w.remote(w.bob, item.ID))
		if got = query(w.bob, "receive", item.ID[:8]); got != item.ID {
			t.Fatalf("receive completion opened an unreadable parcel or lost prefix filtering: %q", got)
		}
		if out := w.fails(w.bob, nil, "permission denied", "receive", item.ID, "--stdout"); len(out) != 0 {
			t.Fatal("unreadable parcel exposed output", string(out))
		}
		w.assertExists(w.remote(w.bob, item.ID), true)
		w.admin("chmod", "u+r", w.remote(w.bob, item.ID))
		w.ok(w.bob, nil, "trust", w.alice.fingerprint, "--name", "alice")
		invalidID := strings.Repeat("c", 32)
		if invalidID == item.ID || invalidID == secondID {
			t.Fatal("invalid fixture ID collided with a generated parcel")
		}
		w.put(w.remote(w.bob, invalidID), []byte("not an age parcel"))
		inbox := filepath.Dir(w.remote(w.bob, item.ID))
		for _, filename := range []string{strings.ToUpper(invalidID) + ".age", "1234567.age", invalidID + ".part", invalidID + ".age.extra"} {
			w.put(filepath.Join(inbox, filename), []byte("ignored filename"))
		}
		withInvalid := append(append([]string{}, wantIDs...), invalidID)
		sort.Strings(withInvalid)
		if got = query(w.bob, "receive", ""); got != strings.Join(withInvalid, "\n") {
			t.Fatalf("receive completion verified contents or accepted an invalid filename: %q", got)
		}
		if out := w.fails(w.bob, nil, "decrypt", "receive", invalidID, "--stdout"); len(out) != 0 {
			t.Fatal("suggested invalid parcel exposed output", string(out))
		}
		w.assertExists(w.remote(w.bob, invalidID), true)
		w.ok(w.bob, nil, "delete", invalidID)
		w.ok(w.bob, nil, "delete", secondID)
		w.ok(w.bob, nil, "receive", item.ID, "--stdout")
		if got = query(w.bob, "receive", ""); got != "" {
			t.Fatal("received ID suggested")
		}
		// Overrides select a distinct inbox, rather than querying the default host.
		if mode == "openssh" {
			cfg, _ := os.ReadFile(w.bob.sshConfig)
			os.WriteFile(w.bob.sshConfig, bytes.Replace(cfg, []byte("Host parcel-test"), []byte("Host parcel-test second"), 1), 0600)
		}
		w.ok(w.bob, nil, "setup", "--host", "second", "--identity", w.bob.key, "--root", ".local/share/keyparcel/second")
		w.ok(w.bob, nil, "send", "--host", "second", "--to", "self", "--text", "second")
		var items []core.Item
		json.Unmarshal(w.ok(w.bob, nil, "inbox", "--host", "second", "--json"), &items)
		if got = query(w.bob, "receive", "--host", "second", ""); len(items) != 1 || got != items[0].ID {
			t.Fatal("host override", got)
		}
		w.ok(w.bob, nil, "send", "--to", "self", "--text", "default inbox")
		w.ok(w.bob, nil, "delete", "--all", "--host", "second")
		if len(w.items(w.bob)) != 1 {
			t.Fatal("bulk deletion affected default host inbox")
		}
		w.ok(w.bob, nil, "delete", "--all")
		// Unknown host keys and stopped servers fail quietly and within a fixed bound.
		known := filepath.Join(w.alice.dir, "known_hosts")
		saved, _ := os.ReadFile(known)
		os.WriteFile(known, nil, 0600)
		if got = query(w.alice, "trust", ""); got != "" {
			t.Fatal(got)
		}
		b, _ := os.ReadFile(known)
		equalBytes(t, nil, b)
		os.WriteFile(known, saved, 0600)
		before, _ := os.ReadFile(w.alice.config)
		_, diag, err := container(w.ctx, nil, "pause", w.id)
		if err != nil {
			t.Fatalf("pause server: %v %s", err, diag)
		}
		defer container(w.ctx, nil, "unpause", w.id)
		if got = query(w.alice, "trust", ""); got != "" {
			t.Fatal(got)
		}
		after, _ := os.ReadFile(w.alice.config)
		equalBytes(t, before, after)
		if got = query(w.alice, "send", "--to", ""); got != "bob\nself" {
			t.Fatal("local suggestions require server", got)
		}
	})
}
