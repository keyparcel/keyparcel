package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"keyparcel/internal/sftptest"
)

func TestPipelinedTransfersProgressFailureAndCancellation(t *testing.T) {
	for _, direction := range []string{"upload", "download"} {
		for _, outcome := range []string{"success", "permission", "cancel"} {
			t.Run(direction+"/"+outcome, func(t *testing.T) {
				server := sftptest.New(t, sftp.UseConcurrentReads(true), sftp.UseConcurrentWrites(true))
				id := testIdentity(t)
				s := &Session{Client: server.Client, Identity: id, Root: "/parcels", closeTransport: server.Client.Close}
				// Cross a full request window, not just a single pipelined batch.
				content := bytes.Repeat([]byte{0, 1, 255}, 1024*1024)
				var manifest Manifest
				if direction == "download" {
					var wire []byte
					manifest, wire = receivePackage(t, id, id, content)
					putInboxPackage(t, s, manifest.ID, wire)
				}
				var mu sync.Mutex
				var events []Progress
				ctx, cancel := context.WithCancel(WithProgress(context.Background(), func(p Progress) {
					mu.Lock()
					defer mu.Unlock()
					events = append(events, p)
				}))
				defer cancel()
				c := NewConnection(context.Background(), func(context.Context) (*Session, error) { return s, nil })
				defer c.Close()
				entered, release := make(chan struct{}), make(chan struct{})
				var unblock sync.Once
				defer unblock.Do(func() { close(release) })
				var requests atomic.Int32
				method := "Write"
				if direction == "download" {
					method = "Read"
				}
				server.OnIO(func(operation, _ string, offset int64) error {
					if operation != method {
						return nil
					}
					if requests.Add(1) == 2 {
						close(entered)
					}
					<-release
					if outcome == "permission" && offset == 32*1024 {
						return sftp.ErrSSHFxPermissionDenied
					}
					return nil
				})
				done := make(chan error, 1)
				var delivered atomic.Bool
				go func() {
					done <- c.Do(ctx, func(view *Session) error {
						if direction == "upload" {
							devices := []Device{{Fingerprint: ssh.FingerprintSHA256(id.Public), Discovered: true}}
							name := "payload.bin"
							_, err := view.SendWithDiscovery(&Config{}, []string{"self"}, "file", &name, bytes.NewReader(content), devices)
							return err
						}
						_, err := view.ReceiveSFTP(&Config{}, manifest.ID, func(_ Manifest, f *os.File) error {
							delivered.Store(true)
							if outcome != "success" {
								return errors.New("failed download reached delivery")
							}
							got, err := io.ReadAll(f)
							if err == nil && !bytes.Equal(got, content) {
								err = errors.New("pipelining changed delivered bytes")
							}
							return err
						}, false)
						return err
					})
				}()
				select {
				case <-entered:
					// Neither request has an acknowledgement: this proves pipelining.
				case err := <-done:
					t.Fatalf("transfer ended before two outstanding requests: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("transfer remained serial with first request held")
				}
				mu.Lock()
				for _, p := range events {
					if p.Bytes != 0 {
						t.Error("unacknowledged/unconsumed bytes reported", p)
					}
				}
				mu.Unlock()
				if outcome == "cancel" {
					cancel()
				}
				unblock.Do(func() { close(release) })
				select {
				case err := <-done:
					if outcome == "success" && err != nil || outcome != "success" && err == nil {
						t.Fatalf("wrong transfer outcome: %v", err)
					}
					if outcome == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation lost: %v", err)
					}
					if outcome == "permission" && direction == "download" && !errors.Is(err, os.ErrPermission) {
						t.Fatalf("download lost pipelined read error: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("pipelined requests did not drain/stop")
				}
				mu.Lock()
				defer mu.Unlock()
				var previous int64
				var total int64
				for _, p := range events {
					if p.Phase == "Uploading" || p.Phase == "Downloading" {
						if p.Bytes < previous || p.Bytes > p.Total || p.Total <= int64(len(content)) {
							t.Fatal("invalid encrypted byte progress", events)
						}
						previous, total = p.Bytes, p.Total
					}
					if outcome != "success" && (p.Phase == "Publishing…" || p.Phase == "Delivering…") {
						t.Fatal("failed transfer advanced to publication/delivery", events)
					}
				}
				if outcome == "success" && previous != total {
					t.Fatal("successful transfer lost final byte count", events)
				}
				if outcome == "permission" && direction == "upload" {
					files, err := s.Client.ReadDir(s.InboxDir())
					if err != nil || len(files) != 0 || server.Calls("Rename") != 0 {
						t.Fatalf("failed pipelined upload was published or not cleaned: %v %v", files, err)
					}
				}
				if outcome == "permission" && direction == "download" {
					if _, err := s.Client.Stat(s.InboxDir() + "/" + manifest.ID + ".age"); err != nil || server.Calls("Remove") != 0 {
						t.Fatal("failed verification removed remote parcel", err)
					}
				}
				if outcome == "cancel" && c.State() == "Connected" {
					t.Fatal("admitted transfer cancellation retained shared owner")
				}
				if direction == "download" && delivered.Load() != (outcome == "success") {
					t.Fatal("download did not preserve verification-before-delivery")
				}
				if outcome != "success" && previous >= total && total != 0 {
					t.Fatal("failed first window reported complete transfer", events)
				}
				if outcome == "success" && direction == "upload" {
					files, err := s.Client.ReadDir(s.InboxDir())
					if err != nil || len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".age") {
						t.Fatalf("successful upload did not publish exactly one parcel: %v %v", files, err)
					}
				}
			})
		}
	}
}

func TestBufferedTransferCancellationBeforePublicationAndDelivery(t *testing.T) {
	for _, direction := range []string{"upload", "download"} {
		t.Run(direction, func(t *testing.T) {
			server := sftptest.New(t, sftp.UseConcurrentReads(true), sftp.UseConcurrentWrites(true))
			id := testIdentity(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &Session{Client: server.Client, Identity: id, Root: "/parcels"}
			content := bytes.Repeat([]byte("buffered"), 16*1024)
			manifest, wire := receivePackage(t, id, id, content)
			if direction == "download" {
				putInboxPackage(t, s, manifest.ID, wire)
			}
			s.ctx = WithProgress(ctx, func(p Progress) {
				if p.Total > 0 && p.Bytes == p.Total {
					cancel()
				}
			})
			var err error
			if direction == "upload" {
				devices := []Device{{Fingerprint: ssh.FingerprintSHA256(id.Public), Discovered: true}}
				outcomes, e := s.SendWithDiscovery(&Config{}, []string{"self"}, "text", nil, bytes.NewReader(content), devices)
				err = e
				if len(outcomes) != 1 || outcomes[0].Unconfirmed || !strings.Contains(outcomes[0].Error, context.Canceled.Error()) {
					t.Fatal("canceled-before-publish result lost certainty", outcomes, err)
				}
				if server.Calls("Rename") != 0 {
					t.Fatal("completed byte count allowed canceled publication")
				}
				files, e := s.Client.ReadDir(s.InboxDir())
				if e != nil || len(files) != 0 {
					t.Fatal("canceled-before-publish temporary not cleaned", files, e)
				}
			} else {
				_, err = s.ReceiveSFTP(&Config{}, manifest.ID, func(Manifest, *os.File) error {
					t.Error("canceled buffered bytes reached delivery")
					return nil
				}, false)
				if !errors.Is(err, context.Canceled) || server.Calls("Remove") != 0 {
					t.Fatal("canceled buffered download lost context/remote parcel", err)
				}
			}
			if err == nil || ctx.Err() == nil {
				t.Fatal("byte observer did not cancel completed transfer", err)
			}
		})
	}
}
