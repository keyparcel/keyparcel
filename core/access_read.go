package core

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

//go:embed access_read.sh
var accessRead string

var errRemoteReadPreflight = errors.New("remote file is missing or unreadable (permission denied)")

func (s *Session) metadataContext() (context.Context, func()) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	stop := context.AfterFunc(ctx, func() { s.Close() })
	return ctx, func() { stop(); cancel() }
}

func (s *Session) commandAvailable(ctx context.Context, required bool) (bool, error) {
	if s.access == nil || s.access.mode == "sftp" {
		if required {
			return false, commandUnsupported()
		}
		return false, nil
	}
	ok, err := s.access.available(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			s.Close()
		}
		return false, err
	}
	if !ok && (required || s.access.mode == "ssh") {
		return false, commandUnsupported()
	}
	return ok, nil
}
func commandUnsupported() error {
	return fmt.Errorf("%w; use --access-method sftp", ErrSSHCommandsUnsupported)
}

func commandHeader(r *bufio.Reader) (string, error) {
	b, err := r.ReadSlice('\n')
	if err != nil || len(b) > 160 {
		return "", errors.New("invalid remote read frame")
	}
	return string(b[:len(b)-1]), nil
}

// readCommand exposes a bounded discovery file or streams one exact encrypted
// parcel. Success requires complete framing AND command exit before delivery.
func (s *Session) readCommand(ctx context.Context, path string, discovery bool, consume func(io.Reader) error) error {
	kind := "parcel"
	if discovery {
		kind = "discovery"
	}
	err := s.access.run(ctx, remoteShell(accessRead, path, kind), func(input io.Reader) error {
		r := bufio.NewReaderSize(input, 256)
		header, err := commandHeader(r)
		if err != nil {
			return err
		}
		if header == "KP-NONREGULAR" {
			return errors.New("package is not a regular file")
		}
		if header == "KP-IO" {
			return errRemoteReadPreflight
		}
		if discovery {
			if header != "KP-DISCOVERY-1" {
				return errors.New("invalid discovery command frame")
			}
			return consume(r)
		}
		parts := strings.Split(header, " ")
		if len(parts) != 2 || parts[0] != "KP-PARCEL-1" {
			return errors.New("invalid parcel command frame")
		}
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n < 0 {
			return errors.New("invalid encrypted parcel length")
		}
		bounded := &io.LimitedReader{R: r, N: n}
		if err = consume(bounded); err != nil {
			return err
		}
		if bounded.N != 0 {
			return errors.New("truncated encrypted parcel stream")
		}
		if _, err = r.ReadByte(); err != io.EOF {
			return errors.New("extra encrypted parcel bytes")
		}
		return nil
	})
	if errors.Is(err, errRemoteReadPreflight) {
		// Diagnose a pre-payload failure without restarting a read or guessing that
		// an inaccessible path is an empty inbox. No content is read through SFTP.
		inspect := s.Client.Lstat
		if discovery {
			inspect = s.Client.Stat
		}
		_, statErr := inspect(path)
		if statErr != nil {
			return statErr
		}
	}
	return err
}
