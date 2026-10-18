package core

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"keyparcel/internal/sftptest"
)

func TestDeleteFullIDsWithoutInboxListing(t *testing.T) {
	server := sftptest.New(t)
	s := &Session{ctx: context.Background(), Client: server.Client, Identity: testIdentity(t), Root: "/parcels"}
	first, second, unrelated := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	for _, id := range []string{first, second, unrelated} {
		// Explicit deletion does not require a valid or approved parcel.
		putInboxPackage(t, s, id, []byte("unverified parcel"))
	}
	remote := func(id string) string { return path.Join(s.InboxDir(), id+".age") }
	server.Fail("List", s.InboxDir(), sftp.ErrSSHFxPermissionDenied)
	if _, err := s.IDs(); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("fixture must deny inbox listing: %v", err)
	}
	count, rejected, err := s.Delete([]string{first, first}, false)
	if err != nil || count != 1 || len(rejected) != 0 {
		t.Fatalf("full ID deletion must skip listing and duplicates: %d %+v %v", count, rejected, err)
	}
	if _, err := s.Client.Stat(remote(first)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted parcel still exists: %v", err)
	}
	// Listing failures must not partially apply a mixed full-ID/prefix batch.
	count, rejected, err = s.Delete([]string{second, unrelated[:8]}, false)
	if !errors.Is(err, os.ErrPermission) || count != 0 || rejected != nil {
		t.Fatalf("mixed batch changed listing-failure behavior: %d %+v %v", count, rejected, err)
	}
	if _, err := s.Client.Stat(remote(second)); err != nil {
		t.Fatal("failed prefix lookup removed the full-ID parcel:", err)
	}
	count, rejected, err = s.Delete(nil, true)
	if !errors.Is(err, os.ErrPermission) || count != 0 || rejected != nil {
		t.Fatalf("delete all bypassed listing failure: %d %+v %v", count, rejected, err)
	}
	server.Fail("Remove", remote(second), sftp.ErrSSHFxPermissionDenied)
	count, rejected, err = s.Delete([]string{second, second}, false)
	if err == nil || count != 0 || len(rejected) != 1 || rejected[0].ID != second {
		t.Fatalf("failed deletion lost its per-item outcome or duplicate guard: %d %+v %v", count, rejected, err)
	}
	if _, err := s.Client.Stat(remote(second)); err != nil {
		t.Fatal("failed deletion lost the parcel:", err)
	}
	server.Fail("List", s.InboxDir(), sftp.ErrSSHFxPermissionDenied)
	count, rejected, err = s.Delete([]string{second}, false)
	if err != nil || count != 1 || len(rejected) != 0 {
		t.Fatalf("retry without listing failed: %d %+v %v", count, rejected, err)
	}
	count, rejected, err = s.Delete([]string{first}, false)
	if err == nil || count != 0 || len(rejected) != 1 || rejected[0].ID != first {
		t.Fatalf("missing full ID was counted as deleted: %d %+v %v", count, rejected, err)
	}
	if _, err := s.Client.Stat(remote(unrelated)); err != nil {
		t.Fatal("exact deletion changed an unrelated parcel:", err)
	}
}

func TestDeleteAbbreviatedIDsStillResolveFromListing(t *testing.T) {
	server := sftptest.New(t)
	s := &Session{ctx: context.Background(), Client: server.Client, Identity: testIdentity(t), Root: "/parcels"}
	first, second := "12345678"+strings.Repeat("a", 24), "12345678"+strings.Repeat("b", 24)
	other := strings.Repeat("c", 32)
	for _, id := range []string{first, second, other} {
		putInboxPackage(t, s, id, []byte("unverified parcel"))
	}
	for _, tc := range []struct{ prefix, reason string }{{first[:8], "ambiguous"}, {"deadbeef", "transfer not found"}} {
		count, rejected, err := s.Delete([]string{tc.prefix}, false)
		if err == nil || count != 0 || len(rejected) != 1 || !strings.Contains(rejected[0].Reason, tc.reason) {
			t.Fatalf("prefix resolution changed: %d %+v %v", count, rejected, err)
		}
	}
	count, rejected, err := s.Delete([]string{first[:9], first, other}, false)
	if err != nil || count != 2 || len(rejected) != 0 {
		t.Fatalf("mixed unique prefix/full IDs lost deduplication: %d %+v %v", count, rejected, err)
	}
	if _, err := s.Client.Stat(path.Join(s.InboxDir(), second+".age")); err != nil {
		t.Fatal("prefix deletion changed an unrelated parcel:", err)
	}
	count, rejected, err = s.Delete(nil, true)
	if err != nil || count != 1 || len(rejected) != 0 {
		t.Fatalf("delete all changed: %d %+v %v", count, rejected, err)
	}
	if ids, err := s.IDs(); err != nil || len(ids) != 0 {
		t.Fatalf("delete all left completed parcels: %v %v", ids, err)
	}
}
