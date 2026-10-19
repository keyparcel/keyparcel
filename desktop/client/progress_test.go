package main

import (
	"context"
	"keyparcel/core"
	"testing"
	"time"
)

// Reuses the single registered GtkApplication and actual native Cancel controls.
func checkGTKTransferProgressOwnership(t *testing.T, m *model) {
	old := m.options.timeout
	m.options.timeout = 5 * time.Second
	defer func() { m.options.timeout = old }()
	await := func(name string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !condition() && time.Now().Before(deadline) {
			iterate()
			time.Sleep(time.Millisecond)
		}
		if !condition() {
			t.Fatal(name)
		}
	}
	contexts := make(chan context.Context, 1)
	start := func(label string) {
		t.Helper()
		if !m.run(label, func(ctx context.Context) (any, error) { contexts <- ctx; <-ctx.Done(); return nil, ctx.Err() }, func(any) { t.Error("canceled work published") }) {
			t.Fatal("work refused")
		}
	}
	start("Preparing…")
	ctx := <-contexts
	sequence, version := m.sequence, m.profileVersion
	m.queueProgress(ctx, sequence, version, core.Progress{Phase: "Uploading", Bytes: 25, Total: 100, Item: 1, Items: 2})
	await("native percentage/bar missing", func() bool { return m.transferFraction() == 0.25 })
	if m.operationProgress() != "Uploading 1/2 · 25%" {
		t.Fatal("per-recipient label", m.operationProgress())
	}
	m.queueProgress(ctx, sequence, version, core.Progress{Phase: "Verifying…"})
	await("verification kept determinate percentage", func() bool { return m.transferFraction() < 0 && m.operationProgress() == "Verifying…" })
	m.profileVersion++
	m.queueProgress(ctx, sequence, version, core.Progress{Phase: "Uploading", Bytes: 99, Total: 100})
	iterate()
	if m.operationProgress() != "Verifying…" {
		t.Fatal("changed trust/profile accepted old progress")
	}
	if !m.activateCancel() {
		t.Fatal("Cancel disabled during transfer phase")
	}
	await("Cancel did not release operation", func() bool { return m.active == nil })
	start("New operation…")
	<-contexts
	m.queueProgress(ctx, sequence, version, core.Progress{Phase: "Uploading", Bytes: 100, Total: 100})
	// Drain the old callback while the newer action owns progress.
	iterate()
	if m.operationProgress() != "New operation…" || m.transferFraction() >= 0 {
		t.Fatal("old transfer overwrote new progress", m.operationProgress())
	}
	if !m.activateCancel() {
		t.Fatal("stale progress disabled new Cancel")
	}
	await("new cancel did not settle", func() bool { return m.active == nil })
}
