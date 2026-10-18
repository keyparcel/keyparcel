package core

import (
	"context"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"keyparcel/internal/sftptest"
)

func TestSendPreservesPartialDeliveryAndUnconfirmedRename(t *testing.T) {
	for _, fault := range []string{"permission", "rename connection loss", "rename permission"} {
		t.Run(fault, func(t *testing.T) {
			server := sftptest.New(t)
			sender, peer := testIdentity(t), testIdentity(t)
			s := &Session{ctx: context.Background(), Client: server.Client, Identity: sender, Root: "/parcels", DiscoveryPath: "/keys"}
			keys, err := s.Client.Create("/keys")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(keys, PublicText(sender.Public)+"\n"+PublicText(peer.Public)+"\n"); err != nil {
				t.Fatal(err)
			}
			keys.Close()
			cfg := &Config{Peers: map[string]string{"peer": PublicText(peer.Public)}}
			names := []string{"self", "peer"}
			switch fault {
			case "permission":
				server.Fail("Mkdir", path.Join(s.Root, "inbox", DeviceID(peer.Public)), os.ErrPermission)
			case "rename connection loss":
				server.Fail("Rename", "", io.ErrClosedPipe)
				names = []string{"peer"}
			case "rename permission":
				server.Fail("Rename", "", os.ErrPermission)
				names = []string{"peer"}
			}
			deliveries, err := s.Send(cfg, names, "text", nil, strings.NewReader("shared payload"))
			if err == nil || len(deliveries) != len(names) {
				t.Fatalf("lost per-recipient outcomes: %+v %v", deliveries, err)
			}
			if fault == "permission" {
				if deliveries[0].Error != "" || deliveries[1].Error == "" || deliveries[1].Unconfirmed {
					t.Fatalf("incorrect partial outcome: %+v", deliveries)
				}
				if _, err = s.Client.Stat(path.Join(s.InboxDir(), deliveries[0].ID+".age")); err != nil {
					t.Fatal("delivered copy missing:", err)
				}
			} else if got := deliveries[0].Unconfirmed; got != (fault == "rename connection loss") {
				t.Fatalf("incorrect certainty: %+v", deliveries)
			}
		})
	}
}

func TestSendSnapshotIsOptInAndApprovalsRemainCurrent(t *testing.T) {
	server := sftptest.New(t)
	sender, peer := testIdentity(t), testIdentity(t)
	s := &Session{ctx: context.Background(), Client: server.Client, Identity: sender, Root: "/parcels", DiscoveryPath: "/keys"}
	writeKeys := func(text string) {
		f, err := s.Client.Create("/keys")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, text); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeKeys(PublicText(sender.Public) + "\n" + PublicText(peer.Public) + "\n")
	cfg := &Config{Peers: map[string]string{"peer": PublicText(peer.Public)}}
	devices, err := s.Discover(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := server.Calls("Open")
	server.Fail("Open", "/keys", sftp.ErrSSHFxPermissionDenied)
	deliveries, err := s.SendWithDiscovery(cfg, []string{"peer"}, "text", nil, strings.NewReader("cached"), devices)
	if err != nil || len(deliveries) != 1 || deliveries[0].Error != "" || server.Calls("Open") != before {
		t.Fatalf("snapshot send reread discovery or failed: %+v %v", deliveries, err)
	}
	if _, err := s.Send(cfg, []string{"peer"}, "text", nil, strings.NewReader("fresh")); err == nil {
		t.Fatal("default Send silently reused discovery")
	}
	if _, err := s.SendWithDiscovery(&Config{}, []string{"peer"}, "text", nil, strings.NewReader("revoked"), devices); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatal("cached discovery bypassed local approval:", err)
	}
	if _, err := s.SendWithDiscovery(cfg, []string{"peer"}, "text", nil, strings.NewReader("empty"), []Device{}); err == nil || !strings.Contains(err.Error(), "not enrolled") {
		t.Fatal("successful empty discovery was treated as a missing snapshot:", err)
	}
	// Removing enrollment is observed by default Send, while an explicit old snapshot is allowed.
	server.Fail("none", "", nil)
	writeKeys(PublicText(sender.Public) + "\n")
	if _, err := s.Send(cfg, []string{"peer"}, "text", nil, strings.NewReader("fresh")); err == nil || !strings.Contains(err.Error(), "not enrolled") {
		t.Fatal("default Send did not refresh enrollment:", err)
	}
	if _, err := s.SendWithDiscovery(cfg, []string{"peer"}, "text", nil, strings.NewReader("accepted snapshot staleness"), devices); err != nil {
		t.Fatal("explicit cached enrollment was unexpectedly refreshed:", err)
	}
}

func TestSendCreatesInboxOnlyAfterDefiniteNotFound(t *testing.T) {
	for _, name := range []string{"warm", "missing", "permission", "collision", "connection", "mkdir failure", "retry only once", "rename failure"} {
		t.Run(name, func(t *testing.T) {
			server := sftptest.New(t)
			id := testIdentity(t)
			s := &Session{ctx: context.Background(), Client: server.Client, Identity: id, Root: "/parcels"}
			devices := []Device{{Fingerprint: ssh.FingerprintSHA256(id.Public), Discovered: true}}
			if name != "missing" && name != "mkdir failure" && name != "retry only once" {
				if err := s.Client.MkdirAll(s.InboxDir()); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "permission":
				server.Fail("Create", "", sftp.ErrSSHFxPermissionDenied)
			case "collision":
				server.Fail("Create", "", os.ErrExist)
			case "connection":
				server.Fail("Create", "", sftp.ErrSSHFxConnectionLost)
			case "mkdir failure":
				server.Fail("Mkdir", s.Root, sftp.ErrSSHFxPermissionDenied)
			case "retry only once":
				server.Fail("Create", "", os.ErrNotExist)
			case "rename failure":
				server.Fail("Rename", "", sftp.ErrSSHFxPermissionDenied)
			}
			stats, mkdirs, creates := server.Calls("Stat"), server.Calls("Mkdir"), server.Calls("Create")
			deliveries, err := s.SendWithDiscovery(&Config{}, []string{"self"}, "text", nil, strings.NewReader("exclusive upload"), devices)
			if len(deliveries) != 1 {
				t.Fatalf("lost outcome: %+v %v", deliveries, err)
			}
			if name == "warm" || name == "missing" {
				if err != nil || deliveries[0].Error != "" {
					t.Fatalf("send failed: %+v %v", deliveries, err)
				}
			} else if err == nil || deliveries[0].Error == "" || deliveries[0].Unconfirmed {
				t.Fatalf("creation/pre-publication error was not a confirmed failure: %+v %v", deliveries, err)
			}
			wantCreates := 1
			if name == "missing" || name == "retry only once" {
				wantCreates = 2
			}
			if got := server.Calls("Create") - creates; got != wantCreates {
				t.Fatalf("exclusive create attempts=%d, want%d", got, wantCreates)
			}
			fallback := name == "missing" || name == "mkdir failure" || name == "retry only once"
			if !fallback && (server.Calls("Stat") != stats || server.Calls("Mkdir") != mkdirs) {
				t.Fatal("warm or non-not-found creation attempted mkdir fallback")
			}
			if fallback && server.Calls("Mkdir") == mkdirs {
				t.Fatal("not-found creation did not attempt directory setup")
			}
			if name == "rename failure" {
				files, err := s.Client.ReadDir(s.InboxDir())
				if err != nil || len(files) != 0 {
					t.Fatalf("failed upload left a temporary or published parcel: %v %v", files, err)
				}
			}
		})
	}
}
