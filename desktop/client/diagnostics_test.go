package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"keyparcel/core"
)

func TestDiagnosticsPrivacyAndLevels(t *testing.T) {
	const secret = "PRIVATE-MARKER-user@host/path-ssh-ed25519-parcel-id-content"
	for _, verbose := range []bool{false, true} {
		var out bytes.Buffer
		d := newDiagnostics(&out, verbose)
		d.startup()
		d.start(secret)
		sensitive := fmt.Errorf("%s: %w", secret, &os.PathError{Op: secret, Path: secret, Err: os.ErrPermission})
		d.finish(secret, time.Now(), nil, sensitive, false)
		d.finish("Loading settings…", time.Now(), profileLoad{Profile: profile{Target: secret}, Err: errors.New(secret)}, nil, false)
		d.finish("Sending…", time.Now(), sendResult{Deliveries: []core.Delivery{{ID: secret, Recipient: secret, Error: secret, Unconfirmed: true}}}, nil, false)
		d.settingsWrite(sensitive)
		d.connection(secret)
		d.background(&net.OpError{Op: secret, Net: secret, Err: errors.New(secret)})
		text := out.String()
		if strings.Contains(text, secret) {
			t.Fatal("sensitive input reached application logs:", text)
		}
		if strings.Contains(text, "level=DEBUG") != verbose {
			t.Fatal("verbose level not respected:", text)
		}
		for _, field := range []string{"msg=startup", "version=", "revision=", "category=permission", "outcome=unconfirmed", "elapsed_ms="} {
			if !strings.Contains(text, field) {
				t.Fatalf("missing safe diagnostic %s: %s", field, text)
			}
		}
	}
}

func TestDiagnosticOperationOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		value     any
		err       error
		discarded bool
		outcome   string
		warning   bool
	}{
		{name: "cancel", err: fmt.Errorf("wrapped: %w", context.Canceled), outcome: "cancelled"},
		{name: "stale", err: &recipientChanged{}, outcome: "discarded"},
		{name: "stale request", err: fmt.Errorf("wrapped: %w", errRequestContextChanged), outcome: "discarded"},
		{name: "superseded", err: errors.New("old error"), discarded: true, outcome: "discarded"},
		{name: "timeout", err: context.DeadlineExceeded, outcome: "failed", warning: true},
		{name: "confirmation", value: "confirm", outcome: "confirmation_required"},
		{name: "partial", value: sendResult{Err: errors.New("failure"), Deliveries: []core.Delivery{{}, {Error: "failure"}}}, outcome: "partial", warning: true},
		{name: "uncertain", value: sendResult{Deliveries: []core.Delivery{{Unconfirmed: true}}}, outcome: "unconfirmed", warning: true},
		{name: "embedded error", value: sendResult{Err: errors.New("failure")}, outcome: "failed", warning: true},
		{name: "success", value: sendResult{Deliveries: []core.Delivery{{}}}, outcome: "success"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			newDiagnostics(&out, true).finish("Sending…", time.Now(), tc.value, tc.err, tc.discarded)
			if !strings.Contains(out.String(), "outcome="+tc.outcome) || strings.Contains(out.String(), "level=WARN") != tc.warning {
				t.Fatal(out.String())
			}
		})
	}
}

func TestBackgroundDiagnosticOutageLifetime(t *testing.T) {
	var out bytes.Buffer
	d := newDiagnostics(&out, false)
	for i := 0; i < 4; i++ {
		d.background(os.ErrPermission)
	}
	d.background(context.Canceled)
	d.background(core.ErrPollBusy)
	d.background(&recipientChanged{})
	d.background(nil)
	d.background(nil)
	if strings.Count(out.String(), "level=WARN") != 1 || strings.Count(out.String(), "outcome=recovered") != 1 {
		t.Fatal("outage flooded or recovery lost:", out.String())
	}
	d.background(os.ErrNotExist)
	d.profileSwitch()
	d.background(os.ErrNotExist)
	if strings.Count(out.String(), "level=WARN") != 3 {
		t.Fatal("new profile did not reset outage suppression:", out.String())
	}
}

func TestReconnectDiagnosticsDoNotChangeDialResults(t *testing.T) {
	var out bytes.Buffer
	report := dialDiagnostics{diagnostics: newDiagnostics(&out, false)}
	failure := errors.New("sensitive upstream")
	session := &core.Session{}
	for _, want := range []error{nil, failure, failure, context.Canceled, nil} {
		got, err := report.dial(context.Background(), func() (*core.Session, error) { return session, want })
		if got != session || err != want {
			t.Fatal("logging changed connection result")
		}
	}
	text := out.String()
	if strings.Contains(text, "sensitive upstream") || strings.Contains(text, "connection_attempt") || strings.Contains(text, "outcome=cancelled") {
		t.Fatal(text)
	}
	if strings.Count(text, "outcome=started") != 1 || strings.Count(text, "outcome=failed") != 1 || strings.Count(text, "outcome=success") != 1 {
		t.Fatal("reconnect flood or missing recovery:", text)
	}
}

func TestActualSettingsWriteDiagnostics(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	s := store{path: filepath.Join(dir, "settings"), diagnostics: newDiagnostics(&out, false)}
	key := identity(t, dir)
	if err := s.setup("secret-target", key, "secret-root", "secret-discovery"); err != nil {
		t.Fatal(err)
	}
	if err := s.setup("", key, "secret-root", "secret-discovery"); err == nil {
		t.Fatal("invalid setup accepted")
	}
	if strings.Count(out.String(), "msg=settings_write") != 1 || !strings.Contains(out.String(), "outcome=success") {
		t.Fatal("validation was logged as a write:", out.String())
	}
	s.path = filepath.Join(dir, "unwritable-target")
	err := s.update(func(*core.Config) error { return os.Mkdir(s.path, 0700) })
	if err == nil || strings.Count(out.String(), "msg=settings_write") != 2 || !strings.Contains(out.String(), "outcome=failed") {
		t.Fatal("failed actual write missing:", err, out.String())
	}
	for _, secret := range []string{dir, key, "secret-target", "secret-root", "secret-discovery"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("settings values logged:", out.String())
		}
	}
}
