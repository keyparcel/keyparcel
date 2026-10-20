package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"keyparcel/core"
)

func checkGTKClipboardPaste(t *testing.T, m *model) {
	t.Helper()
	m.page("send")
	m.queueProgress(context.Background(), ^uint64(0), m.profileVersion, core.Progress{})
	// A retired operation can still own a queued release. Drain those before
	// measuring the clipboard request's lifetime, rather than accepting an
	// unrelated release as completion of the read we just started.
	deadline := time.Now().Add(3 * time.Second)
	for len(m.workers) != 0 && time.Now().Before(deadline) {
		iterate()
		time.Sleep(time.Millisecond)
	}
	if len(m.workers) != 0 {
		t.Fatal("previous worker did not retire")
	}
	flushUI()
	baseline := m.pendingCallbacks()
	await := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for m.pendingCallbacks() != baseline && time.Now().Before(deadline) {
			iterate()
			time.Sleep(time.Millisecond)
		}
		if m.pendingCallbacks() != baseline {
			t.Fatal("clipboard read did not settle")
		}
	}
	for _, test := range []struct {
		text       string
		start, end int
		clip, want string
		cursor     int
	}{
		{"", 0, 0, "fresh", "fresh", 5},
		{"abcd", 2, 2, "世界\n", "ab世界\ncd", 5},
		{"abcd", 3, 1, "X", "aXd", 2},
		{"draft", 1, 3, "", "draft", 1},
	} {
		m.pasteFixture(test.text, test.start, test.end, test.clip)
		m.activatePaste()
		await()
		if m.nativeDraft() != test.want || m.draftCursor() != test.cursor {
			t.Fatalf("paste: %q cursor%d, want %q cursor%d", m.nativeDraft(), m.draftCursor(), test.want, test.cursor)
		}
	}
	for _, stale := range []string{"edit", "page", "busy", "lock", "profile"} {
		m.pasteFixture("draft", 1, 3, "old")
		m.activatePaste() // GDK callback cannot run before the main loop is dispatched.
		switch stale {
		case "edit":
			m.pasteFixture("new draft", 0, 0, "old")
		case "page":
			m.page("settings")
			m.page("send")
		case "busy":
			m.setNativeBusy(true)
			m.setNativeBusy(false)
		case "lock":
			m.setPasteLocked(true)
			m.setPasteLocked(false)
		case "profile":
			m.clearResults()
		}
		await()
		want := "draft"
		if stale == "edit" {
			want = "new draft"
		}
		if m.nativeDraft() != want {
			t.Fatalf("%s accepted stale clipboard: %q", stale, m.nativeDraft())
		}
	}
	m.setNativeBusy(true)
	m.activatePaste()
	await()
	m.setNativeBusy(false)
	m.setPasteLocked(true)
	m.activatePaste()
	await()
	m.setPasteLocked(false)
	if m.nativeDraft() != "draft" {
		t.Fatal("locked editor accepted paste")
	}
	m.pasteFixture("abcd", 3, 1, "X")
	flushUI()
	windows, err := exec.Command("xdotool", "search", "--onlyvisible", "--pid", strconv.Itoa(os.Getpid()), "--name", "^keyparcel$").Output()
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Fields(string(windows))
	bounds := m.pasteState()
	if !bounds.mapped || len(ids) == 0 {
		t.Fatal("paste control not mapped", bounds)
	}
	if output, err := exec.Command("xdotool", "mousemove", "--window", ids[len(ids)-1], strconv.Itoa(bounds.x+bounds.width/2), strconv.Itoa(bounds.y+bounds.height/2), "click", "1").CombinedOutput(); err != nil {
		t.Fatalf("pointer paste: %v %s", err, output)
	}
	deadline = time.Now().Add(3 * time.Second)
	for m.nativeDraft() != "aXd" && time.Now().Before(deadline) {
		iterate()
		time.Sleep(time.Millisecond)
	}
	if m.nativeDraft() != "aXd" || !m.pasteState().focused || m.draftCursor() != 2 {
		t.Fatal("real pointer lost selection/focus/caret", m.nativeDraft(), m.pasteState())
	}
	m.pasteFixture("", 0, 0, "")
	m.page("settings")
}
