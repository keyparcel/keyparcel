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
	unrelated := p
	unrelated.Config = &core.Config{Peers: map[string]string{"bob": "key", "other": "other-key"}}
	if got := retainedRecipient("bob", "inbox", host, peers, unrelated); got != "bob" {
		t.Fatalf("unrelated approval lost draft selection: %q", got)
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

func TestInboxDeleteContext(t *testing.T) {
	p := profile{Target: "inbox", Host: core.Host{Identity: "key", Fingerprint: "fp", Root: "root", Discovery: "keys"}, Config: &core.Config{Peers: map[string]string{"bob": "key"}}}
	request := pendingDelete{ID: "0123456789abcdef0123456789abcdef", Profile: p}
	if !request.matches(p) || !containsItem([]core.Item{{ID: request.ID}}, request.ID) {
		t.Fatal("unchanged inbox confirmation was rejected")
	}
	for _, field := range []string{"target", "identity", "fingerprint", "root", "discovery", "approvals"} {
		t.Run(field, func(t *testing.T) {
			changed := p
			switch field {
			case "target":
				changed.Target = "other"
			case "identity":
				changed.Host.Identity = "other"
			case "fingerprint":
				changed.Host.Fingerprint = "other"
			case "root":
				changed.Host.Root = "other"
			case "discovery":
				changed.Host.Discovery = "other"
			case "approvals":
				changed.Config = &core.Config{Peers: map[string]string{}}
			}
			if request.matches(changed) {
				t.Fatal("same-ID parcel in a changed context accepted")
			}
		})
	}
	if containsItem([]core.Item{{ID: "other"}}, request.ID) {
		t.Fatal("missing parcel accepted")
	}
}
