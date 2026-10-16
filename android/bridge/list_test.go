package bridge

import (
	"context"
	"io"
	"path"
	"strings"
	"testing"

	"keyparcel/core"
	"keyparcel/internal/sftptest"
)

func TestInboxDoesNotReturnJSONForMidListingConnectionLoss(t *testing.T) {
	private, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	id, err := core.ParseIdentity(private)
	if err != nil {
		t.Fatal(err)
	}
	server := sftptest.New(t)
	s := &core.Session{Client: server.Client, Identity: id, Root: "/data"}
	if err := server.Client.MkdirAll(s.InboxDir()); err != nil {
		t.Fatal(err)
	}
	c := &Client{session: s, ctx: context.Background(), config: &core.Config{}}
	if json, err := c.Inbox(); err != nil || json == "" {
		t.Fatalf("initial inbox failed: %q, %v", json, err)
	}
	name := path.Join(s.InboxDir(), "0123456789abcdef0123456789abcdef.age")
	f, err := server.Client.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	server.Fail("Lstat", name, io.ErrClosedPipe)
	json, err := c.Inbox()
	if err == nil || !strings.Contains(err.Error(), "connection lost") || json != "" {
		t.Fatalf("failed listing returned publishable inbox JSON: %q, %v", json, err)
	}
}
