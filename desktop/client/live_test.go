package main

import (
	"context"
	"keyparcel/core"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestProfileInvalidationKeepsTransportOnlyForPeerChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := core.NewConnection(ctx, func(context.Context) (*core.Session, error) { t.Fatal("unexpected dial"); return nil, nil })
	host := core.Host{Identity: "selected", Fingerprint: "pin", Root: "parcels", Discovery: "keys"}
	owner := sessionOwner{ctx: ctx, connection: c, target: "server", host: host}
	owner.current.Store(&ownedConnection{c, "server", host})
	before := owner.stamp()
	owner.invalidate("server", host) // Peer approval changes invalidate results, not SSH.
	if owner.connection != c || c.State() == "Suspended" || owner.stamp() == before {
		t.Fatal("approval invalidation lost transport or retained old generation")
	}
	_, err := owner.request(ctx, profile{}, "", "", false, true, before, func(*core.Session, profile) (any, error) { t.Fatal("stale request executed"); return nil, nil })
	if err == nil {
		t.Fatal("old approval request was admitted")
	}
	changed := host
	changed.Fingerprint = "replacement"
	owner.invalidate("server", changed)
	if owner.connection != nil || c.State() != "Suspended" || owner.state("server", changed) != "Not connected" {
		t.Fatal("identity change retained old owner")
	}
}

func TestStalePollCannotRestoreDeletedRowsOrTrustResults(t *testing.T) {
	item := core.Item{Manifest: core.Manifest{ID: "deleted"}}
	for _, change := range []string{"delete", "profile", "approval", "busy", "closed"} {
		t.Run(change, func(t *testing.T) {
			m := &model{profileVersion: 1, hasSnapshot: true}
			snapshot := liveSnapshot{Items: []core.Item{item}, Devices: []core.Device{{Fingerprint: "stale discovery"}}, InboxOK: true, DevicesOK: true}
			version := m.profileVersion
			switch change {
			case "delete", "profile", "approval":
				m.profileVersion++
			case "busy":
				m.active = func() {}
			case "closed":
				m.closed = true
			}
			if m.publishSnapshot(snapshot, version) || len(m.items) != 0 || len(m.devices) != 0 {
				t.Fatal("stale poll restored invalidated rows")
			}
		})
	}
}

func testGTKStaleManualRefreshKeepsForegroundProgressAndCancel(t *testing.T, m *model) {
	oldPath, oldTarget, oldTimeout := m.store.path, m.target, m.options.timeout
	defer func() { m.store.path, m.target, m.options.timeout = oldPath, oldTarget, oldTimeout }()
	fifo := filepath.Join(t.TempDir(), "held-config")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	m.store.path, m.target, m.options.timeout = fifo, "held", 5*time.Second
	m.requestRefresh()
	// Opening the writer proves the refresh reached its blocked config read.
	writer, err := os.OpenFile(fifo, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	entered, canceled := make(chan struct{}), make(chan struct{})
	if !m.run("Viewing…", func(ctx context.Context) (any, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}, func(any) { t.Error("canceled View completed") }) {
		t.Fatal("foreground work was refused")
	}
	<-entered
	if m.operationProgress() != "Viewing…" {
		t.Fatal("foreground spinner/progress/Cancel not active before old refresh completion")
	}
	if _, err := writer.Write([]byte(`{"hosts":{},"peers":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for m.manualCancel != nil && time.Now().Before(deadline) {
		iterate()
		runtime.Gosched()
	}
	if m.manualCancel != nil {
		t.Fatal("held manual refresh did not complete")
	}
	m.target = "" // Keep later timer ticks from opening the fixture FIFO again.
	if m.active == nil || m.operationProgress() != "Viewing…" {
		t.Fatal("stale manual refresh cleared newer foreground progress or Cancel")
	}
	if !m.activateCancel() {
		t.Fatal("native foreground Cancel is disabled")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("native Cancel did not reach foreground operation")
	}
	deadline = time.Now().Add(time.Second)
	for m.active != nil && time.Now().Before(deadline) {
		iterate()
		runtime.Gosched()
	}
	if m.active != nil {
		t.Fatal("canceled foreground action did not restore controls")
	}
}
