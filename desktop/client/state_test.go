package main

import (
	"keyparcel/core"
	"testing"
)

func TestRecipientBelongsToHostAndApprovedKey(t *testing.T) {
	host := core.Host{Identity: "identity", Fingerprint: "fingerprint", Root: "storage", Discovery: "keys"}
	peers := map[string]string{"bob": "key"}
	p := profile{Target: "inbox", Host: host, Config: &core.Config{Peers: peers}}
	if got := retainedRecipient("bob", "inbox", host, peers, p); got != "bob" {
		t.Fatalf("navigation lost unchanged peer: %q", got)
	}
	if got := retainedRecipient("self", "inbox", host, peers, p); got != "self" {
		t.Fatalf("navigation lost this device: %q", got)
	}
	for _, test := range []struct {
		name    string
		profile profile
	}{
		{"removed approval", profile{Target: "inbox", Host: host, Config: &core.Config{Peers: map[string]string{}}}},
		{"replaced approval", profile{Target: "inbox", Host: host, Config: &core.Config{Peers: map[string]string{"bob": "other-key"}}}},
		{"changed target", profile{Target: "other", Host: host, Config: p.Config}},
		{"changed identity", profile{Target: "inbox", Host: core.Host{Identity: "other"}, Config: p.Config}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := retainedRecipient("bob", "inbox", host, peers, test.profile); got != "" {
				t.Fatalf("stale recipient remained selected: %q", got)
			}
		})
	}
	if got := retainedRecipient("unknown", "inbox", host, peers, p); got != "" {
		t.Fatalf("unknown peer remained selected: %q", got)
	}
	if got := retainedRecipient("self", "other", host, peers, p); got != "" {
		t.Fatalf("host change retained self selection: %q", got)
	}
}

func TestNavigationParents(t *testing.T) {
	var n navigation
	for _, page := range []string{"inbox", "send", "devices"} {
		n.open(page)
		if n.parent() != "" {
			t.Fatalf("top-level %s unexpectedly has a parent", page)
		}
		n.open("settings")
		n.open("settings")
		if n.parent() != page {
			t.Fatalf("Settings from %s returns to %s", page, n.parent())
		}
	}
	for _, page := range []string{"text", "saved"} {
		n.open(page)
		if n.parent() != "inbox" {
			t.Fatalf("result %s does not return to Inbox", page)
		}
		n.open("settings")
		if n.parent() != "inbox" {
			t.Fatalf("Settings retained an abandoned %s result", page)
		}
	}
}
