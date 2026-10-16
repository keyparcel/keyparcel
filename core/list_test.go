package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"syscall"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"keyparcel/internal/sftptest"
)

func TestListDistinguishesConnectionFailuresFromRejectedPackages(t *testing.T) {
	id := testIdentity(t)
	packages := map[string][]byte{}
	var names []string
	for range 2 {
		f, manifest, err := NewPackage(context.Background(), id, []ssh.PublicKey{id.Public}, "text", nil, bytes.NewBufferString("last good inbox"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		defer os.Remove(f.Name())
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
		name := manifest.ID + ".age"
		packages[name] = data
		names = append(names, name)
	}
	sort.Strings(names)
	for _, tc := range []struct {
		name, method string
		fault        error
		fatal        bool
	}{
		{"disconnect on stat", "Lstat", io.ErrClosedPipe, true},
		{"disconnect on open", "Open", io.ErrClosedPipe, true},
		{"disconnect on metadata read", "Read", io.ErrClosedPipe, true},
		{"disconnect on close", "Close", io.ErrClosedPipe, true},
		{"server lost connection", "Lstat", sftp.ErrSSHFxConnectionLost, true},
		{"server has no connection", "Read", sftp.ErrSSHFxNoConnection, true},
		{"broken request pipe", "WriteLstat", syscall.EPIPE, true},
		{"closed request stream", "WriteLstat", io.EOF, true},
		{"EOF closing package", "Close", io.EOF, true},
		{"package permission denied", "Lstat", os.ErrPermission, false},
		{"package removed during refresh", "Open", os.ErrNotExist, false},
		{"truncated package", "Read", io.EOF, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := sftptest.New(t)
			s := &Session{ctx: context.Background(), Client: server.Client, Identity: id, Root: "/inbox"}
			if err := s.Client.MkdirAll(s.InboxDir()); err != nil {
				t.Fatal(err)
			}
			for name, data := range packages {
				name = path.Join(s.InboxDir(), name)
				remote, err := s.Client.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := remote.Write(data); err != nil {
					t.Fatal(err)
				}
				if err := remote.Close(); err != nil {
					t.Fatal(err)
				}
			}
			items, rejected, _, err := s.List(&Config{})
			if err != nil || len(items) != 2 || len(rejected) != 0 {
				t.Fatalf("initial inbox: %v, %v, %v", items, rejected, err)
			}
			name := path.Join(s.InboxDir(), names[1])
			server.Fail(tc.method, name, tc.fault)
			items, rejected, ids, err := s.List(&Config{})
			if tc.fatal {
				if err == nil || items != nil || rejected != nil || ids != nil {
					t.Fatalf("connection failure exposed a successful/partial snapshot: %v, %v, %v, %v", items, rejected, ids, err)
				}
				var status *sftp.StatusError
				if !errors.Is(err, tc.fault) && !errors.Is(err, sftp.ErrSSHFxConnectionLost) && !(errors.As(err, &status) && (status.FxCode() == sftp.ErrSSHFxConnectionLost || status.FxCode() == sftp.ErrSSHFxNoConnection)) {
					t.Fatalf("connection failure lost its cause: %v", err)
				}
			} else if err != nil || len(items) != 1 || len(rejected) != 1 || len(ids) != 2 {
				t.Fatalf("isolated package rejection failed the whole refresh: %v, %v, %v, %v", items, rejected, ids, err)
			}
		})
	}
}
