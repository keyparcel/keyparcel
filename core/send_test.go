package core

import (
	"context"
	"io"
	"os"
	"path"
	"strings"
	"testing"

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
