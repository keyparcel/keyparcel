package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"keyparcel/core"
	"keyparcel/internal/buildinfo"
)

// Only fixed events/categories enter this logger; upstream errors and UI values do not.
type diagnostics struct {
	log    *slog.Logger
	mu     sync.Mutex
	outage bool
}

func newDiagnostics(out io.Writer, verbose bool) *diagnostics {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return &diagnostics{log: slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level}))}
}
func (d *diagnostics) startup() {
	if d != nil {
		d.log.Info("startup", "version", buildinfo.Version, "revision", buildinfo.Revision)
	}
}
func diagnosticCategory(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, os.ErrPermission), errors.Is(err, sftp.ErrSSHFxPermissionDenied):
		return "permission"
	case errors.Is(err, os.ErrNotExist), errors.Is(err, sftp.ErrSSHFxNoSuchFile):
		return "not_found"
	}
	var changed *recipientChanged
	if errors.As(err, &changed) || errors.Is(err, errRequestContextChanged) {
		return "context_changed"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "timeout"
	}
	if errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, sftp.ErrSSHFxNoConnection) {
		return "transport"
	}
	return "unknown"
}
func diagnosticOutcome(err error) string {
	switch diagnosticCategory(err) {
	case "none":
		return "success"
	case "cancelled":
		return "cancelled"
	case "context_changed":
		return "discarded"
	default:
		return "failed"
	}
}
func operationName(notice string) string {
	switch notice {
	case "Copy":
		return "copy"
	case "Read enrollment":
		return "read_enrollment"
	case "Refresh":
		return "refresh"
	case "Loading settings…":
		return "load_settings"
	case "Saving shared settings…":
		return "save_settings"
	case "Connecting through OpenSSH…":
		return "connect"
	case "Saving peer approval…":
		return "approve_device"
	case "Forgetting peer approval…":
		return "unapprove_device"
	case "Checking selected file…":
		return "select_file"
	case "Verifying parcel…":
		return "view"
	case "Verifying and saving file…":
		return "save_file"
	case "Deleting remote inbox copy…", "Removing remote transfer…":
		return "delete"
	case "Sending…":
		return "send"
	default:
		return "operation"
	}
}
func (d *diagnostics) start(notice string) {
	if d != nil {
		d.log.Debug("operation_start", "op", operationName(notice))
	}
}
func (d *diagnostics) finish(notice string, started time.Time, value any, err error, discarded bool) {
	if d == nil {
		return
	}
	outcome, category := diagnosticOutcome(err), diagnosticCategory(err)
	if err == nil {
		switch result := value.(type) {
		case profileLoad:
			outcome, category = diagnosticOutcome(result.Err), diagnosticCategory(result.Err)
		case sendResult:
			outcome, category = diagnosticOutcome(result.Err), diagnosticCategory(result.Err)
			delivered, failed, uncertain := false, false, false
			for _, item := range result.Deliveries {
				delivered = delivered || (item.Error == "" && !item.Unconfirmed)
				failed = failed || item.Error != ""
				uncertain = uncertain || item.Unconfirmed
			}
			if uncertain {
				outcome, category = "unconfirmed", "delivery"
			} else if failed && delivered {
				outcome, category = "partial", "delivery"
			} else if failed {
				outcome, category = "failed", "delivery"
			}
		case string:
			if result == "confirm" {
				outcome = "confirmation_required"
			}
		}
	}
	if discarded {
		outcome = "discarded"
	}
	level := slog.LevelDebug
	if outcome == "failed" || outcome == "partial" || outcome == "unconfirmed" {
		level = slog.LevelWarn
	}
	d.log.Log(context.Background(), level, "operation_end", "op", operationName(notice), "outcome", outcome, "category", category, "elapsed_ms", time.Since(started).Milliseconds())
}
func (d *diagnostics) settingsWrite(err error) {
	if d == nil {
		return
	}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	d.log.Log(context.Background(), level, "settings_write", "outcome", diagnosticOutcome(err), "category", diagnosticCategory(err))
}
func (d *diagnostics) profileSwitch() {
	if d == nil {
		return
	}
	d.resetBackground()
	d.log.Info("profile_switch")
}
func (d *diagnostics) resetBackground() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.outage = false
	d.mu.Unlock()
}
func (d *diagnostics) background(err error) {
	if d == nil {
		return
	}
	// Busy/skipped/cancelled polls are not an outage or a recovery.
	if errors.Is(err, core.ErrPollBusy) || diagnosticOutcome(err) == "cancelled" || diagnosticOutcome(err) == "discarded" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		if d.outage {
			d.log.Info("background_refresh", "outcome", "recovered")
			d.outage = false
		}
	} else {
		level := slog.LevelDebug
		if !d.outage {
			level = slog.LevelWarn
			d.outage = true
		}
		d.log.Log(context.Background(), level, "background_refresh", "outcome", "failed", "category", diagnosticCategory(err))
	}
}
func (d *diagnostics) connection(state string) {
	if d == nil {
		return
	}
	// Do not accept transport/UI text as a log field.
	switch state {
	case "Connected":
		state = "connected"
	case "Connecting":
		state = "connecting"
	case "Disconnected":
		state = "disconnected"
	case "Suspended":
		state = "suspended"
	default:
		state = "not_connected"
	}
	d.log.Debug("connection_state", "state", state)
}

// Per-owner state distinguishes first setup from reconnects and suppresses retry noise.
type dialDiagnostics struct {
	diagnostics         *diagnostics
	mu                  sync.Mutex
	attempted, retrying bool
}

func (d *dialDiagnostics) dial(ctx context.Context, connect func() (*core.Session, error)) (*core.Session, error) {
	started := time.Now()
	d.mu.Lock()
	reconnect, announce := d.attempted, d.attempted && !d.retrying
	d.attempted = true
	if announce {
		d.retrying = true
	}
	d.mu.Unlock()
	event, level := "connection_attempt", slog.LevelDebug
	if reconnect {
		event = "reconnect"
	}
	if announce {
		level = slog.LevelInfo
	}
	if d.diagnostics != nil {
		d.diagnostics.log.Log(context.Background(), level, event, "outcome", "started")
	}
	session, err := connect()
	diagnosticError := err
	if ctx.Err() != nil {
		diagnosticError = ctx.Err()
	}
	level = slog.LevelDebug
	if reconnect && diagnosticError == nil {
		level = slog.LevelInfo
	}
	if announce && diagnosticOutcome(diagnosticError) == "failed" {
		level = slog.LevelWarn
	}
	if d.diagnostics != nil {
		d.diagnostics.log.Log(context.Background(), level, event, "outcome", diagnosticOutcome(diagnosticError), "category", diagnosticCategory(diagnosticError), "elapsed_ms", time.Since(started).Milliseconds())
	}
	if err == nil {
		d.mu.Lock()
		d.retrying = false
		d.mu.Unlock()
	}
	return session, err
}
