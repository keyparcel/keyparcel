package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"keyparcel/internal/sftptest"
)

func TestProgressBytesThrottleUnknownAndCancellation(t *testing.T) {
	var events []Progress
	ctx, cancel := context.WithCancel(WithProgress(context.Background(), func(p Progress) { events = append(events, p) }))
	r := newProgressReader(ctx, strings.NewReader("123456"), Progress{Phase: "Downloading", Total: 6})
	b := make([]byte, 2)
	r.last = time.Now().Add(time.Hour)
	r.Read(b)
	r.Read(b)
	if len(events) != 1 {
		t.Fatal("small chunks flooded observer", events)
	}
	r.last = time.Now().Add(-time.Second)
	r.Read(b)
	if got := events[len(events)-1]; got.Bytes != 6 || got.Total != 6 {
		t.Fatal("wrong byte accounting", got)
	}
	cancel()
	phase(ctx, "Delivering…")
	r.Read(b)
	if len(events) != 2 {
		t.Fatal("canceled observer updated", events)
	}
	events = nil
	unknown := newProgressReader(WithProgress(context.Background(), func(p Progress) { events = append(events, p) }), strings.NewReader("abc"), Progress{Phase: "Downloading"})
	io.Copy(io.Discard, unknown)
	if got := events[len(events)-1]; got.Total != 0 || got.Bytes != 3 {
		t.Fatal("unknown length invented a percentage", got)
	}
}

func TestTransferProgressCountsCiphertextAndPreservesVerification(t *testing.T) {
	server := sftptest.New(t)
	id := testIdentity(t)
	var events []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { events = append(events, p) })
	s := &Session{ctx: ctx, Client: server.Client, Identity: id, Root: "/parcels"}
	devices := []Device{{Fingerprint: ssh.FingerprintSHA256(id.Public), Discovered: true}}
	deliveries, err := s.SendWithDiscovery(&Config{}, []string{"self"}, "text", nil, strings.NewReader("hello"), devices)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := s.Client.Open(s.InboxDir() + "/" + deliveries[0].ID + ".age")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := io.ReadAll(remote)
	remote.Close()
	if err != nil {
		t.Fatal(err)
	}
	check := func(phaseName string) {
		t.Helper()
		var previous int64
		found := false
		for _, p := range events {
			if p.Phase == phaseName {
				if p.Bytes < previous || p.Total != int64(len(ciphertext)) {
					t.Fatal("nonmonotonic or wrong encrypted size", events)
				}
				previous = p.Bytes
				found = true
			}
		}
		if !found || previous != int64(len(ciphertext)) {
			t.Fatal("missing exact transfer bytes", events)
		}
	}
	check("Uploading")
	if events[len(events)-1].Phase != "Publishing…" {
		t.Fatal("upload claimed delivery before publication", events)
	}
	events = nil
	_, err = s.Receive(&Config{}, deliveries[0].ID, func(_ Manifest, f *os.File) error {
		if events[len(events)-1].Phase != "Delivering…" {
			t.Fatal("missing delivery boundary", events)
		}
		b, e := io.ReadAll(f)
		if e != nil || !bytes.Equal(b, []byte("hello")) {
			t.Fatal("changed delivered bytes", string(b), e)
		}
		return nil
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	check("Downloading")
	// A corrupt parcel must never advance to delivery, regardless of byte count.
	ciphertext[len(ciphertext)-1] ^= 1
	putInboxPackage(t, s, deliveries[0].ID, ciphertext)
	events = nil
	_, err = s.Receive(&Config{}, deliveries[0].ID, func(Manifest, *os.File) error { t.Fatal("corrupt parcel delivered"); return nil }, false)
	if err == nil {
		t.Fatal("corruption accepted")
	}
	for _, p := range events {
		if p.Phase == "Delivering…" {
			t.Fatal("corruption reported delivering")
		}
	}
}

func TestProgressDoesNotCountFailedReadsAsBytes(t *testing.T) {
	var last Progress
	expected := errors.New("read failed")
	r := newProgressReader(WithProgress(context.Background(), func(p Progress) { last = p }), failingProgressReader{expected}, Progress{Phase: "Downloading", Total: 10})
	if _, err := io.Copy(io.Discard, r); !errors.Is(err, expected) || last.Bytes != 0 {
		t.Fatal(last, err)
	}
}

type failingProgressReader struct{ err error }

func (r failingProgressReader) Read([]byte) (int, error) { return 0, r.err }

func TestUploadProgressCountsOnlyAcceptedPartialWrites(t *testing.T) {
	var last Progress
	failure := errors.New("destination failed")
	writer := newProgressWriter(WithProgress(context.Background(), func(p Progress) { last = p }), partialProgressWriter{failure}, Progress{Phase: "Uploading", Total: 100})
	n, err := writer.Write(make([]byte, 100))
	if n != 3 || !errors.Is(err, failure) || last.Bytes != 3 || last.Total != 100 {
		t.Fatal("read-ahead reported as accepted upload", n, err, last)
	}
}

type partialProgressWriter struct{ err error }

func (w partialProgressWriter) Write([]byte) (int, error) { return 3, w.err }

func TestCommandProgressDoesNotDeliverBeforeSuccessfulExit(t *testing.T) {
	server := sftptest.New(t)
	id := testIdentity(t)
	m, wire := receivePackage(t, id, id, []byte("verified payload"))
	var events []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { events = append(events, p) })
	s := &Session{ctx: ctx, Client: server.Client, Identity: id, Root: "/parcels"}
	failure := errors.New("command exit failed")
	s.access = &remoteAccess{mode: "ssh", supported: true, run: func(_ context.Context, _ string, consume func(io.Reader) error) error {
		stream := io.MultiReader(strings.NewReader(fmt.Sprintf("KP-PARCEL-1 %d\n", len(wire))), bytes.NewReader(wire))
		if err := consume(stream); err != nil {
			return err
		}
		return failure
	}}
	s.access.once.Do(func() {})
	_, err := s.Receive(&Config{}, m.ID, func(Manifest, *os.File) error { t.Fatal("failed command delivered"); return nil }, false)
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	found := false
	for _, p := range events {
		if p.Phase == "Delivering…" {
			t.Fatal("100 percent bypassed exit verification")
		}
		if p.Phase == "Downloading" && p.Bytes == int64(len(wire)) && p.Total == int64(len(wire)) {
			found = true
		}
	}
	if !found {
		t.Fatal("SSH frame size/byte progress missing", events)
	}
}

func TestProgressBelongsToRequestViewNotConnectionOwner(t *testing.T) {
	c := NewConnection(context.Background(), func(ctx context.Context) (*Session, error) {
		return &Session{ctx: ctx, closeTransport: func() error { return nil }}, nil
	})
	defer c.Close()
	first := []Progress{}
	second := []Progress{}
	a := WithProgress(context.Background(), func(p Progress) { first = append(first, p) })
	b := WithProgress(context.Background(), func(p Progress) { second = append(second, p) })
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- c.Poll(a, func(s *Session) error {
			phase(s.ctx, "poll")
			close(entered)
			<-release
			phase(s.ctx, "poll finished")
			return nil
		})
	}()
	<-entered
	if err := c.Do(b, func(s *Session) error { phase(s.ctx, "foreground"); return nil }); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].Phase != "poll" || first[1].Phase != "poll finished" || len(second) != 1 || second[0].Phase != "foreground" {
		t.Fatal("observer leaked across immutable views", first, second)
	}
}

func TestUntrustedSmallTotalCannotFloodProgress(t *testing.T) {
	count := 0
	r := newProgressReader(WithProgress(context.Background(), func(Progress) { count++ }), strings.NewReader(strings.Repeat("x", 100)), Progress{Phase: "Downloading", Total: 1})
	for range 100 {
		r.last = time.Now().Add(time.Hour)
		r.Read(make([]byte, 1))
	}
	if count != 2 {
		t.Fatalf("wrong server size bypassed throttle: %d callbacks", count)
	}
}
