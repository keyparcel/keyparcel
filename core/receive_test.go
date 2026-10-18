package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"keyparcel/internal/sftptest"
)

func receivePackage(t *testing.T, sender, recipient *Identity, data []byte) (Manifest, []byte) {
	t.Helper()
	name := "original.bin"
	f, m, err := NewPackage(context.Background(), sender, []ssh.PublicKey{recipient.Public}, "file", &name, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	defer os.Remove(f.Name())
	ciphertext, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return m, ciphertext
}

func putInboxPackage(t *testing.T, s *Session, name string, ciphertext []byte) {
	t.Helper()
	if err := s.Client.MkdirAll(s.InboxDir()); err != nil {
		t.Fatal(err)
	}
	f, err := s.Client.Create(path.Join(s.InboxDir(), name+".age"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(ciphertext); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReceiveFullIDWithoutInboxListing(t *testing.T) {
	sender, recipient := testIdentity(t), testIdentity(t)
	server := sftptest.New(t)
	s := &Session{ctx: context.Background(), Client: server.Client, Identity: recipient, Root: "/parcels"}
	cfg := &Config{Peers: map[string]string{"sender": PublicText(sender.Public)}}
	content := []byte{0, 255, 10, 128, 32}
	m, ciphertext := receivePackage(t, sender, recipient, content)
	other, otherCiphertext := receivePackage(t, sender, recipient, []byte("unrelated"))
	putInboxPackage(t, s, m.ID, ciphertext)
	putInboxPackage(t, s, other.ID, otherCiphertext)
	server.Fail("List", s.InboxDir(), sftp.ErrSSHFxPermissionDenied)
	if _, err := s.IDs(); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("fixture must deny inbox listing: %v", err)
	}
	var staging string
	deliver := func(got Manifest, f *os.File) error {
		staging = f.Name()
		data, err := io.ReadAll(f)
		if err != nil || !bytes.Equal(data, content) || got.ID != m.ID {
			t.Fatalf("exact receive changed verified bytes/ID: %x %+v %v", data, got, err)
		}
		return nil
	}
	if _, err := s.Receive(cfg, m.ID, deliver, true); err != nil {
		t.Fatalf("full ID must not require listing: %v", err)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified staging file was not removed: %v", err)
	}
	remote := path.Join(s.InboxDir(), m.ID+".age")
	if _, err := s.Client.Stat(remote); err != nil {
		t.Fatal("keep removed the remote parcel:", err)
	}
	failed := errors.New("destination unavailable")
	if _, err := s.Receive(cfg, m.ID, func(Manifest, *os.File) error { return failed }, false); !errors.Is(err, failed) {
		t.Fatalf("delivery failure lost its cause: %v", err)
	}
	if _, err := s.Client.Stat(remote); err != nil {
		t.Fatal("failed delivery removed the remote parcel:", err)
	}
	if _, err := s.Receive(cfg, m.ID, deliver, false); err != nil {
		t.Fatal("successful delivery without listing:", err)
	}
	if _, err := s.Client.Stat(remote); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful delivery did not delete its remote parcel: %v", err)
	}
	if _, err := s.Receive(cfg, m.ID, deliver, true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing full ID must fail its exact lookup, not listing: %v", err)
	}
	if _, err := s.Client.Stat(path.Join(s.InboxDir(), other.ID+".age")); err != nil {
		t.Fatal("receive changed an unrelated parcel:", err)
	}
}

func TestReceiveAbbreviatedIDsStillResolveFromListing(t *testing.T) {
	id := testIdentity(t)
	server := sftptest.New(t)
	s := &Session{ctx: context.Background(), Client: server.Client, Identity: id, Root: "/parcels"}
	m, ciphertext := receivePackage(t, id, id, []byte("prefix lookup"))
	putInboxPackage(t, s, m.ID, ciphertext)
	collision := m.ID[:8] + "0" + m.ID[9:]
	if collision == m.ID {
		collision = m.ID[:8] + "1" + m.ID[9:]
	}
	putInboxPackage(t, s, collision, ciphertext)
	neverDeliver := func(Manifest, *os.File) error { t.Fatal("unresolved prefix reached delivery"); return nil }
	if _, err := s.Receive(&Config{}, m.ID[:8], neverDeliver, false); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix changed semantics: %v", err)
	}
	missing := strings.Repeat("f", 8)
	if missing == m.ID[:8] {
		missing = strings.Repeat("0", 8)
	}
	if _, err := s.Receive(&Config{}, missing, neverDeliver, false); err == nil || err.Error() != "transfer not found" {
		t.Fatalf("missing prefix changed semantics: %v", err)
	}
	if err := s.Client.Remove(path.Join(s.InboxDir(), collision+".age")); err != nil {
		t.Fatal(err)
	}
	for n := 8; n < 32; n++ {
		got, err := s.Receive(&Config{}, m.ID[:n], func(_ Manifest, f *os.File) error {
			data, err := io.ReadAll(f)
			if string(data) != "prefix lookup" {
				t.Fatalf("%d-character prefix changed bytes: %q", n, data)
			}
			return err
		}, true)
		if err != nil || got.ID != m.ID {
			t.Fatalf("unique %d-character prefix failed: %+v %v", n, got, err)
		}
	}
	server.Fail("List", s.InboxDir(), sftp.ErrSSHFxPermissionDenied)
	if _, err := s.Receive(&Config{}, m.ID[:31], neverDeliver, false); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("abbreviated ID bypassed listing failure: %v", err)
	}
}

func TestTransferActionsRejectInvalidIDBeforeRemoteAccess(t *testing.T) {
	for _, id := range []string{"", "1234567", "../12345678", strings.Repeat("a", 31) + "/", strings.Repeat("A", 32), strings.Repeat("a", 31) + "g", strings.Repeat("a", 31) + "\x00", strings.Repeat("a", 33), strings.Repeat("a", 32) + ".age"} {
		// No transport or identity: invalid input must be rejected before either is used.
		if _, err := (&Session{}).Receive(&Config{}, id, nil, false); err == nil || !strings.Contains(err.Error(), "8–32 lowercase hexadecimal") {
			t.Fatalf("invalid ID %q reached remote access: %v", id, err)
		}
		count, rejected, err := (&Session{}).Delete([]string{id}, false)
		if err == nil || count != 0 || len(rejected) != 1 || !strings.Contains(rejected[0].Reason, "8–32 lowercase hexadecimal") {
			t.Fatalf("invalid deletion ID %q reached remote access: %d %+v %v", id, count, rejected, err)
		}
	}
}

func TestReceiveExactIDStillVerifiesBeforeDelivery(t *testing.T) {
	sender, recipient, other := testIdentity(t), testIdentity(t), testIdentity(t)
	content := []byte("fully verified bytes")
	baseline, _ := receivePackage(t, sender, recipient, content)
	for _, name := range []string{"unapproved sender", "wrong decryption key", "unlisted recipient", "filename mismatch", "signature", "hash", "short content", "trailing content", "truncated ciphertext", "symlink", "directory"} {
		t.Run(name, func(t *testing.T) {
			server := sftptest.New(t)
			s := &Session{ctx: context.Background(), Client: server.Client, Identity: recipient, Root: "/parcels"}
			cfg := &Config{Peers: map[string]string{"sender": PublicText(sender.Public)}}
			m, encryptionKey := baseline, recipient.Public
			requestedID, want := m.ID, ""
			switch name {
			case "unapproved sender":
				cfg.Peers, want = nil, "unapproved sender"
			case "wrong decryption key":
				encryptionKey, want = other.Public, "decrypt"
			case "unlisted recipient":
				m.Recipients, want = []string{ssh.FingerprintSHA256(other.Public)}, "not an intended recipient"
			case "filename mismatch":
				requestedID, want = "0"+m.ID[1:], "does not match filename"
				if requestedID == m.ID {
					requestedID = "1" + m.ID[1:]
				}
			case "signature":
				want = "signature"
			case "hash":
				m.SHA256, want = strings.Repeat("0", 64), "size/hash mismatch"
			case "short content":
				m.Size++
				want = "size/hash mismatch"
			case "trailing content":
				m.Size--
				want = "trailing content"
			case "symlink", "directory":
				want = "not a regular file"
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			signer := sender
			if name == "signature" {
				signer = other
			}
			sig, err := sign(context.Background(), signer, data)
			if err != nil {
				t.Fatal(err)
			}
			key, err := agessh.ParseRecipient(PublicText(encryptionKey))
			if err != nil {
				t.Fatal(err)
			}
			var ciphertext bytes.Buffer
			enc, err := age.Encrypt(&ciphertext, key)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteHeader(enc, data, sig); err != nil {
				t.Fatal(err)
			}
			if _, err := enc.Write(content); err != nil {
				t.Fatal(err)
			}
			if err := enc.Close(); err != nil {
				t.Fatal(err)
			}
			wire := ciphertext.Bytes()
			if name == "truncated ciphertext" {
				wire = wire[:len(wire)-1]
			}
			putInboxPackage(t, s, requestedID, wire)
			remote := path.Join(s.InboxDir(), requestedID+".age")
			if name == "symlink" || name == "directory" {
				if err := s.Client.Rename(remote, remote+".original"); err != nil {
					t.Fatal(err)
				}
				if name == "symlink" {
					err = s.Client.Symlink(remote+".original", remote)
				} else {
					err = s.Client.Mkdir(remote)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			server.Fail("List", s.InboxDir(), sftp.ErrSSHFxPermissionDenied)
			_, err = s.Receive(cfg, requestedID, func(Manifest, *os.File) error {
				t.Fatal("unverified content reached delivery")
				return nil
			}, false)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s was not rejected by verification: %v", name, err)
			}
			if _, err := s.Client.Lstat(remote); err != nil {
				t.Fatal("verification failure removed remote parcel:", err)
			}
		})
	}
}
