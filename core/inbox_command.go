package core

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed access_probe.sh
var accessProbe string

//go:embed inbox_batch.sh
var inboxBatch string

var ErrSSHCommandsUnsupported = errors.New("SSH command access unavailable")
var errInboxBudget = errors.New("inbox command aggregate limit; use --access-method sftp")
var errInboxPath = errors.New("inbox directory is inaccessible")

const inboxPrefixLimit = 1 << 20
const inboxTotalLimit = 64 << 20
const inboxRecordLimit = 4096

type remoteCommand func(context.Context, string, func(io.Reader) error) error

// Shared by request views of one authenticated owner. Only negotiation is locked;
// listing and foreground work never wait on a whole-operation owner mutex.
type remoteAccess struct {
	run          remoteCommand
	mode         string
	once         sync.Once
	supported    bool
	probeErr     error
	inboxLimited atomic.Bool
}

func validAccessMethod(mode string) bool {
	return mode == "" || mode == "auto" || mode == "ssh" || mode == "sftp"
}

// ValidateAccessMethod accepts only the remote read policy.
func ValidateAccessMethod(mode string) error {
	if !validAccessMethod(mode) {
		return errors.New("access method must be auto, ssh or sftp")
	}
	return nil
}
func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func remoteShell(script string, args ...string) string {
	cmd := "sh -c " + shellArgument(script) + " sh"
	for _, arg := range args {
		cmd += " " + shellArgument(arg)
	}
	return cmd
}

func (a *remoteAccess) available(ctx context.Context) (bool, error) {
	if a == nil || a.mode == "sftp" {
		return false, nil
	}
	a.once.Do(func() {
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		err := a.run(probe, remoteShell(accessProbe), func(r io.Reader) error {
			data, err := io.ReadAll(io.LimitReader(r, 1025))
			if err != nil {
				return err
			}
			if !bytes.Equal(data, []byte("KP-CAP-1\n\x00\xff\n")) {
				return ErrSSHCommandsUnsupported
			}
			return nil
		})
		a.supported = err == nil
		// EOF from exec setup is owner/control failure. Clean unsupported
		// output reaches the comparison above with no reader error.
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || connectionFailure(err) {
			a.probeErr = err
		}
	})
	return a.supported, a.probeErr
}

// List uses the read-only command capability. Auto may use ordinary SFTP
// for unsupported servers or aggregate limits; explicit SSH never hides fallback.
func (s *Session) List(c *Config) ([]Item, []Rejected, []string, error) {
	if s.access != nil {
		items, rejected, ids, err := s.ListAccelerated(c)
		if err == nil {
			return items, rejected, ids, nil
		}
		if s.access.mode == "ssh" || (!errors.Is(err, errInboxBudget) && !errors.Is(err, ErrSSHCommandsUnsupported) && !errors.Is(err, errInboxPath)) {
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
	if s.access != nil && s.access.inboxLimited.Load() {
		return nil, nil, nil, errInboxBudget
	}
	if s.access == nil || s.access.mode == "sftp" {
		return nil, nil, nil, commandUnsupported()
	}
	ctx, done := s.metadataContext()
	defer done()
	_, err := s.commandAvailable(ctx, true)
	if err != nil {
		return nil, nil, nil, err
	}
	var items []Item
	var rejected []Rejected
	var ids []string
	err = s.access.run(ctx, remoteShell(inboxBatch, s.InboxDir()), func(r io.Reader) error {
		var err error
		items, rejected, ids, err = s.decodeInbox(c, r)
		return err
	})
	if errors.Is(err, errInboxPath) {
		_, e := s.Client.Stat(s.InboxDir())
		if errors.Is(e, os.ErrNotExist) {
			return []Item{}, []Rejected{}, []string{}, nil
		}
		if e != nil {
			return nil, nil, nil, e
		}
	}
	if err != nil {
		if errors.Is(err, errInboxBudget) {
			s.access.inboxLimited.Store(true)
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
		return nil, nil, nil, errInboxPath
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
			return nil, nil, nil, ErrSSHCommandsUnsupported
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
