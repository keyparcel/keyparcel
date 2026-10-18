package core

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed inbox_probe.sh
var inboxProbe string

//go:embed inbox_batch.sh
var inboxBatch string

var ErrInboxAccelerationUnsupported = errors.New("inbox acceleration unavailable")
var errInboxBudget = errors.New("inbox acceleration aggregate limit")

const inboxPrefixLimit = 1 << 20
const inboxTotalLimit = 64 << 20
const inboxRecordLimit = 4096

type inboxCommand func(context.Context, string, func(io.Reader) error) error

// Shared by request views of one authenticated owner. Only negotiation is locked;
// listing and foreground work never wait on a whole-operation owner mutex.
type inboxAcceleration struct {
	run       inboxCommand
	mode      string
	once      sync.Once
	supported bool
	probeErr  error
	limited   atomic.Bool
}

func validInboxMode(mode string) bool { return mode == "" || mode == "auto" || mode == "sftp" }

// ValidateInboxMode accepts only the narrow inbox-listing policy.
func ValidateInboxMode(mode string) error {
	if !validInboxMode(mode) {
		return errors.New("inbox mode must be auto or sftp")
	}
	return nil
}
func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func inboxShell(script string, args ...string) string {
	cmd := "sh -c " + shellArgument(script) + " sh"
	for _, arg := range args {
		cmd += " " + shellArgument(arg)
	}
	return cmd
}

func (a *inboxAcceleration) available(ctx context.Context) (bool, error) {
	if a == nil || a.mode == "sftp" || a.limited.Load() {
		return false, nil
	}
	a.once.Do(func() {
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		err := a.run(probe, inboxShell(inboxProbe), func(r io.Reader) error {
			data, err := io.ReadAll(io.LimitReader(r, 1025))
			if err != nil {
				return err
			}
			if !bytes.Equal(data, []byte("KP-CAP-1\n\x00\xff\n")) {
				return ErrInboxAccelerationUnsupported
			}
			return nil
		})
		a.supported = err == nil
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || connectionFailure(err) {
			a.probeErr = err
		}
	})
	return a.supported, a.probeErr
}

// List uses the optional read-only command capability. Mutations, discovery,
// completion and full content retrieval always use SFTP.
func (s *Session) List(c *Config) ([]Item, []Rejected, []string, error) {
	if s.inbox != nil {
		items, rejected, ids, err := s.ListAccelerated(c)
		if err == nil {
			return items, rejected, ids, nil
		}
		if !errors.Is(err, errInboxBudget) && !errors.Is(err, ErrInboxAccelerationUnsupported) {
			return nil, nil, nil, err
		}
	}
	if s.ctx != nil && s.ctx.Err() != nil {
		return nil, nil, nil, s.ctx.Err()
	}
	return s.ListSFTP(c)
}

// ListAccelerated requires the real command path; callers comparing performance
// must not mistake a transparent SFTP fallback for an accelerated measurement.
func (s *Session) ListAccelerated(c *Config) ([]Item, []Rejected, []string, error) {
	if s.inbox == nil || s.inbox.mode == "sftp" || s.inbox.limited.Load() {
		return nil, nil, nil, ErrInboxAccelerationUnsupported
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer func() { stop(); cancel() }()
	available, err := s.inbox.available(ctx)
	if err != nil {
		// A timed-out probe/channel cannot safely be reused as an unsupported tool.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			s.Close()
		}
		return nil, nil, nil, err
	}
	if !available {
		return nil, nil, nil, ErrInboxAccelerationUnsupported
	}
	var items []Item
	var rejected []Rejected
	var ids []string
	err = s.inbox.run(ctx, inboxShell(inboxBatch, s.InboxDir()), func(r io.Reader) error {
		var err error
		items, rejected, ids, err = s.decodeInbox(c, r)
		return err
	})
	if err != nil {
		if errors.Is(err, errInboxBudget) {
			s.inbox.limited.Store(true)
		}
		return nil, nil, nil, err
	}
	return items, rejected, ids, nil
}

func (s *Session) decodeInbox(c *Config, r io.Reader) ([]Item, []Rejected, []string, error) {
	reader := bufio.NewReaderSize(r, 256)
	line := func() (string, error) {
		b, err := reader.ReadSlice('\n')
		if err != nil || len(b) > 160 {
			return "", errors.New("invalid inbox command frame")
		}
		return string(b[:len(b)-1]), nil
	}
	header, err := line()
	if err != nil {
		return nil, nil, nil, err
	}
	if header == "KP-SFTP" {
		return nil, nil, nil, ErrInboxAccelerationUnsupported
	}
	if header != "KP-INBOX-1" {
		return nil, nil, nil, errors.New("invalid inbox command version")
	}
	items := []Item{}
	rejected := []Rejected{}
	ids := []string{}
	seen := map[string]bool{}
	total := int64(0)
	for {
		header, err = line()
		if err != nil {
			return nil, nil, nil, err
		}
		if header == "END" {
			if _, err = reader.ReadByte(); err != io.EOF {
				return nil, nil, nil, errors.New("trailing inbox command data")
			}
			sort.Strings(ids)
			sortInbox(items)
			return items, rejected, ids, nil
		}
		fields := strings.Split(header, " ")
		if len(fields) < 2 || !validHex(fields[1], 32) || seen[fields[1]] {
			return nil, nil, nil, errors.New("invalid or duplicate inbox command ID")
		}
		if len(ids) >= inboxRecordLimit {
			return nil, nil, nil, errInboxBudget
		}
		id := fields[1]
		seen[id] = true
		ids = append(ids, id)
		if fields[0] == "X" && len(fields) == 3 {
			reason := "package is not a regular file"
			if fields[2] == "io" {
				reason = "cannot inspect package"
			} else if fields[2] != "nonregular" {
				return nil, nil, nil, errors.New("invalid inbox rejection")
			}
			rejected = append(rejected, Rejected{id, reason})
			continue
		}
		if fields[0] != "R" || len(fields) != 4 {
			return nil, nil, nil, errors.New("invalid inbox command record")
		}
		seconds, e := strconv.ParseInt(fields[2], 10, 64)
		size, e2 := strconv.ParseInt(fields[3], 10, 64)
		if e != nil || e2 != nil || size < 0 || size > inboxPrefixLimit {
			return nil, nil, nil, errors.New("invalid inbox command size or timestamp")
		}
		// Match SFTP v3's unsigned 32-bit timestamp range; unusual filesystem
		// timestamps use the ordinary listing instead of changing its semantics.
		if seconds < 0 || seconds > int64(^uint32(0)) {
			return nil, nil, nil, ErrInboxAccelerationUnsupported
		}
		total += size
		if total > inboxTotalLimit {
			return nil, nil, nil, errInboxBudget
		}
		data := make([]byte, int(size))
		if _, err = io.ReadFull(reader, data); err != nil {
			return nil, nil, nil, fmt.Errorf("truncated inbox command: %w", err)
		}
		if b, err := reader.ReadByte(); err != nil || b != '\n' {
			return nil, nil, nil, errors.New("truncated inbox command separator")
		}
		item, e := s.readInboxItem(c, id, time.Unix(seconds, 0).UTC(), bytes.NewReader(data))
		if e != nil {
			rejected = append(rejected, Rejected{id, e.Error()})
		} else {
			items = append(items, item)
		}
	}
}
