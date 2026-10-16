package main

import (
	"keyparcel/core"
	"testing"
	"time"
)

func TestSuccessFeedbackDisclosureAndExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	rows := []core.Delivery{{Recipient: "self", ID: "0123456789abcdef"}}
	f := newSendFeedback(rows, now)
	rows[0].ID = "mutated"
	if f.headline() != "Sent to This device · 01234567" || f.Expanded {
		t.Fatalf("unexpected collapsed success: %+v", f)
	}
	if f.expire(now.Add(feedbackDuration - time.Millisecond)) {
		t.Fatal("success expired early")
	}
	f.expand(true, now.Add(time.Second))
	if f.expire(now.Add(time.Hour)) {
		t.Fatal("open details expired")
	}
	f.expand(false, now.Add(time.Hour))
	if !f.expire(now.Add(time.Hour+feedbackDuration)) || f.Visible {
		t.Fatal("collapsed success did not expire")
	}
	if f.Deliveries[0].ID != "0123456789abcdef" {
		t.Fatal("expiry or caller mutation lost the original ID")
	}
}
func TestIncompleteFeedbackAndRetryIDs(t *testing.T) {
	now := time.Unix(100, 0)
	a := &sendAttempt{Deliveries: []core.Delivery{{Recipient: "self", ID: "first"}, {Recipient: "bob", Error: "permission denied"}}}
	f := newSendFeedback(a.Deliveries, now)
	if !f.Expanded || f.headline() != "Send incomplete" {
		t.Fatal("problem details not opened")
	}
	f.expand(false, now)
	if f.expire(now.Add(time.Hour)) {
		t.Fatal("incomplete feedback expired")
	}
	a.merge([]core.Delivery{{Recipient: "bob", ID: "second"}})
	f = newSendFeedback(a.Deliveries, now)
	if f.headline() != "Sent to 2 devices" || f.Deliveries[0].ID != "first" || f.Deliveries[1].ID != "second" {
		t.Fatalf("retry conflated IDs: %+v", f)
	}
	f = newSendFeedback([]core.Delivery{{Recipient: "bob", Error: "connection lost", Unconfirmed: true}}, now)
	if f.expire(now.Add(time.Hour)) || !f.Expanded || deliveryOutcome(f.Deliveries[0]) != "unconfirmed — check the inbox before sending again" {
		t.Fatal("unconfirmed delivery was dismissible or retry advice unsafe")
	}
	f = newSendFeedback([]core.Delivery{{Recipient: "self", ID: "common-id"}, {Recipient: "bob", ID: "common-id"}}, now)
	if f.headline() != "Sent to 2 devices · common-i" {
		t.Fatal(f.headline())
	}
}
