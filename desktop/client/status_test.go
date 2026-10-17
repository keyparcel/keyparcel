package main

import (
	"context"
	"keyparcel/core"
	"testing"
	"time"
)

func TestConfirmationReplacementAndExpiry(t *testing.T) {
	now := time.Unix(10, 0)
	value := confirmation{"Copied", now.Add(3 * time.Second)}
	if value.current(now.Add(2*time.Second)) != "Copied" {
		t.Fatal("confirmation vanished early")
	}
	if value.current(now.Add(3*time.Second)) != "" {
		t.Fatal("confirmation survived expiry")
	}
	value = confirmation{"Parcel deleted", now.Add(5 * time.Second)}
	if value.current(now.Add(3*time.Second)) != "Parcel deleted" {
		t.Fatal("old expiry removed replacement")
	}
}

func TestConnectionIndicatorRejectsOtherProfile(t *testing.T) {
	c := core.NewConnection(context.Background(), nil)
	defer c.Close()
	owner := sessionOwner{}
	host := core.Host{}
	owner.current.Store(&ownedConnection{c, "alice", host})
	if owner.state("alice", host) != "Not connected" {
		t.Fatal("idle owner claimed a connection")
	}
	if owner.state("bob", host) != "Not connected" {
		t.Fatal("other profile reused old state")
	}
	c.Close()
	if owner.state("alice", host) != "Suspended" {
		t.Fatal("close not reflected")
	}
	if owner.state("bob", host) != "Not connected" {
		t.Fatal("other profile inherited suspension")
	}
}
