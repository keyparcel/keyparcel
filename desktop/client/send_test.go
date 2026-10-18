package main

import (
	"keyparcel/core"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSendRetryKeepsDeliveredAndUnconfirmedRecipients(t *testing.T) {
	a := &sendAttempt{Deliveries: []core.Delivery{
		{Recipient: "self", ID: "delivered"},
		{Recipient: "bob", Error: "permission denied"},
		{Recipient: "unknown", Error: "connection lost", Unconfirmed: true},
	}}
	if got := a.failed(); len(got) != 1 || got[0] != "bob" {
		t.Fatalf("unsafe retry recipients: %v", got)
	}
	a.merge([]core.Delivery{{Recipient: "bob", ID: "retried"}})
	if a.Deliveries[0].ID != "delivered" || a.Deliveries[1].ID != "retried" || !a.Deliveries[2].Unconfirmed || len(a.failed()) != 0 {
		t.Fatalf("retry lost previous delivery outcomes: %+v", a.Deliveries)
	}
	a.merge([]core.Delivery{{Recipient: "bob", Error: "cancelled", Unconfirmed: true}})
	if a.Deliveries[0].ID != "delivered" {
		t.Fatal("cancelled retry forgot delivered copy")
	}
}

func TestSendContextCapturesEveryApprovedKey(t *testing.T) {
	first, err := core.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	own, err := core.ParseIdentity(first)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := core.ParseIdentity(second)
	if err != nil {
		t.Fatal(err)
	}
	p := profile{Target: "inbox", Host: core.Host{Identity: "id", Fingerprint: "host"}, Public: core.PublicText(own.Public), Config: &core.Config{Peers: map[string]string{"bob": core.PublicText(peer.Public)}}}
	a := &sendAttempt{Profile: p, Keys: map[string]string{"self": sendKey(p, "self"), "bob": sendKey(p, "bob")}}
	if !a.matches(p) {
		t.Fatal("captured context does not match itself")
	}
	if !sameDiscoveryContext(p, p) {
		t.Fatal("discovery context does not match itself")
	}
	changed := p
	changed.Config = &core.Config{Peers: map[string]string{"bob": p.Config.Peers["bob"], "other": p.Public}}
	if !a.matches(changed) {
		t.Fatal("unrelated approval invalidates send")
	}
	if sameDiscoveryContext(p, changed) {
		t.Fatal("approval change reused old discovery context")
	}
	changed.Config = &core.Config{Peers: map[string]string{}}
	if a.matches(changed) {
		t.Fatal("removed additional recipient was silently omitted")
	}
	changed = p
	changed.Target = "other"
	if a.matches(changed) {
		t.Fatal("same keys accepted on another profile")
	}
	changed = p
	changed.Public = p.Config.Peers["bob"]
	if sameDiscoveryContext(p, changed) {
		t.Fatal("replacement identity reused old discovery context")
	}
	if a.matches(changed) {
		t.Fatal("replacement self identity accepted")
	}
}

func TestCancelledSendCannotReopenRemovedStagingPath(t *testing.T) {
	stage, err := os.CreateTemp(t.TempDir(), "send-")
	if err != nil {
		t.Fatal(err)
	}
	attempt := &sendAttempt{Stage: stage}
	m := &model{sending: attempt}
	m.clearSendAttempt()
	target := filepath.Join(t.TempDir(), "keep.txt")
	if err = os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, stage.Name()); err != nil {
		t.Fatal(err)
	}
	if _, err = attempt.Stage.WriteString("late worker payload"); err == nil {
		t.Fatal("cancelled worker retained writable staging handle")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "keep" {
		t.Fatalf("cancelled send wrote to a replacement staging path: %q %v", got, err)
	}
	if m.sending != nil {
		t.Fatal("cancelled attempt remained active")
	}
}

func TestRecipientChooserExcludesSelectedKeys(t *testing.T) {
	own, err := core.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := core.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ownIdentity, err := core.ParseIdentity(own)
	if err != nil {
		t.Fatal(err)
	}
	peerIdentity, err := core.ParseIdentity(peer)
	if err != nil {
		t.Fatal(err)
	}
	m := &model{public: core.PublicText(ownIdentity.Public), recipient: "self",
		peers: map[string]string{"own-alias": core.PublicText(ownIdentity.Public), "bob": core.PublicText(peerIdentity.Public)}}
	if got := m.candidates(); !reflect.DeepEqual(got, []string{"bob"}) {
		t.Fatalf("chooser included selected key or omitted peer: %v", got)
	}
	m.extraRecipients = []string{"bob"}
	if got := m.candidates(); len(got) != 0 {
		t.Fatalf("chooser offered selected recipients: %v", got)
	}
	if got := m.selectedRecipients(); !reflect.DeepEqual(got, []string{"self", "bob"}) {
		t.Fatalf("draft recipients lost: %v", got)
	}
}
