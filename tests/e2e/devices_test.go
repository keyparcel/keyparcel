//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"keyparcel/core"
)

func TestSelfIdentityDiscoveryAndHostSelection(t *testing.T) {
	runTransports(t, func(t *testing.T, mode string) {
		w := newWorld(t, mode)
		var devices []core.Device
		json.Unmarshal(w.ok(w.alice, nil, "devices", "--json"), &devices)
		if len(devices) != 3 || devices[0].Fingerprint != w.alice.fingerprint || !devices[0].Self || devices[0].Name != "self" || devices[1].Fingerprint != w.bob.fingerprint {
			t.Fatal("discovery order or self identity", devices)
		}
		w.fails(w.alice, nil, "reserved", "trust", w.bob.fingerprint, "--name", "self")
		w.ok(w.alice, nil, "send", "--to", "self", "--text", "own")
		item := w.items(w.alice)[0]
		if item.From != "self" {
			t.Fatal(item)
		}
		equalBytes(t, []byte("own"), w.ok(w.alice, nil, "receive", item.ID, "--stdout"))
		// The alternate host selects Bob's key, locally available in Alice's container.
		other := filepath.Join(w.alice.dir, "second_identity")
		private, _ := os.ReadFile(w.bob.key)
		os.WriteFile(other, private, 0600)
		if mode == "openssh" {
			bobConfig, _ := os.ReadFile(w.bob.sshConfig)
			second := bytes.Replace(bobConfig, []byte("Host parcel-test"), []byte("Host second"), 1)
			second = bytes.ReplaceAll(second, []byte(w.bob.key), []byte(other))
			second = bytes.ReplaceAll(second, []byte(filepath.Join(w.bob.dir, "known_hosts")), []byte(filepath.Join(w.alice.dir, "known_hosts")))
			config, _ := os.OpenFile(w.alice.sshConfig, os.O_APPEND|os.O_WRONLY, 0600)
			config.Write(second)
			config.Close()
		}
		w.ok(w.alice, nil, "setup", "--host", "second", "--identity", other)
		w.ok(w.alice, nil, "send", "--host", "second", "--to", "self", "--text", "second identity")
		var items []core.Item
		json.Unmarshal(w.ok(w.alice, nil, "inbox", "--host", "second", "--json"), &items)
		if len(items) != 1 || items[0].Sender != w.bob.fingerprint || items[0].From != "self" {
			t.Fatal(items)
		}
		equalBytes(t, []byte("second identity"), w.ok(w.alice, nil, "receive", items[0].ID, "--host", "second", "--stdout"))
		if len(w.items(w.alice)) != 0 {
			t.Fatal("self override used default key")
		}
		pub, _ := os.ReadFile(w.bob.key + ".pub")
		w.put("/home/parcel/.ssh/without_self", pub)
		w.admin("chmod", "644", "/home/parcel/.ssh/without_self")
		w.ok(w.alice, nil, "setup", "--host", "parcel-test", "--identity", w.alice.key, "--discovery", ".ssh/without_self")
		w.fails(w.alice, nil, "not enrolled", "send", "--to", "self", "--text", "not enrolled")
		w.ok(w.alice, nil, "setup", "--host", "parcel-test", "--identity", w.alice.key)
		w.ok(w.alice, nil, "trust", w.bob.fingerprint, "--name", "z-last")
		w.ok(w.alice, nil, "trust", w.carol.fingerprint, "--name", "a-first")
		alicePub, _ := os.ReadFile(w.alice.key + ".pub")
		w.put("/home/parcel/.ssh/without_self", alicePub)
		w.ok(w.alice, nil, "setup", "--host", "parcel-test", "--identity", w.alice.key, "--discovery", ".ssh/without_self")
		json.Unmarshal(w.ok(w.alice, nil, "devices", "--json"), &devices)
		if len(devices) != 3 || devices[0].Name != "self" || devices[1].Name != "a-first" || devices[2].Name != "z-last" || devices[1].Discovered || devices[2].Discovered {
			t.Fatal("missing peers not sorted by name", devices)
		}

	})
}
