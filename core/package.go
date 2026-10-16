package core

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

const Marker = "KEYPARCEL\x00\x01"
const Namespace = "keyparcel/v1"
const MaxMetadata = 64 * 1024

type Manifest struct {
	Version    int      `json:"version"`
	ID         string   `json:"transfer_id"`
	Sender     string   `json:"sender"`
	Recipients []string `json:"recipients"`
	Type       string   `json:"type"`
	Filename   *string  `json:"filename"`
	Size       int64    `json:"size"`
	SHA256     string   `json:"sha256"`
}

func validHex(s string, n int) bool {
	if len(s) != n || s != strings.ToLower(s) {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (m Manifest) Validate() error {
	if m.Version != 1 || !validHex(m.ID, 32) || m.Size < 0 || !validHex(m.SHA256, 64) {
		return errors.New("invalid manifest version, ID, size or hash")
	}
	if m.Type == "text" {
		if m.Filename != nil {
			return errors.New("text must not have a filename")
		}
	} else if m.Type == "file" {
		if m.Filename == nil || !SafeFilename(*m.Filename) {
			return errors.New("invalid filename")
		}
	} else {
		return errors.New("unsupported content type")
	}
	if !validFingerprint(m.Sender) || len(m.Recipients) == 0 {
		return errors.New("invalid sender or recipients")
	}
	seen := map[string]bool{}
	for _, r := range m.Recipients {
		if !validFingerprint(r) || seen[r] {
			return errors.New("invalid or duplicate recipient")
		}
		seen[r] = true
	}
	return nil
}
func validFingerprint(s string) bool {
	return strings.HasPrefix(s, "SHA256:") && len(s) == 50 && !strings.ContainsAny(s, " \t\n\r")
}
func SafeFilename(n string) bool {
	return n != "" && n != "." && n != ".." && path.Base(n) == n && !strings.ContainsAny(n, "/\\\x00")
}
func NewPackage(ctx context.Context, id *Identity, keys []ssh.PublicKey, kind string, filename *string, input io.Reader) (*os.File, Manifest, error) {
	m := Manifest{Version: 1, Sender: ssh.FingerprintSHA256(id.Public), Type: kind, Filename: filename}
	staging, err := os.CreateTemp("", "keyparcel-content-*")
	if err != nil {
		return nil, m, err
	}
	defer func() { staging.Close(); os.Remove(staging.Name()) }()
	hash := sha256.New()
	m.Size, err = io.Copy(io.MultiWriter(staging, hash), input)
	if err != nil {
		return nil, m, err
	}
	m.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if kind == "text" {
		if _, err = staging.Seek(0, 0); err != nil {
			return nil, m, err
		}
		if err = validateText(staging); err != nil {
			return nil, m, err
		}
	}
	rawID := make([]byte, 16)
	if _, err = rand.Read(rawID); err != nil {
		return nil, m, err
	}
	m.ID = hex.EncodeToString(rawID)
	recipients := make([]age.Recipient, 0, len(keys))
	for _, p := range keys {
		r, e := agessh.ParseRecipient(PublicText(p))
		if e != nil {
			return nil, m, e
		}
		recipients = append(recipients, r)
		m.Recipients = append(m.Recipients, ssh.FingerprintSHA256(p))
	}
	if err = m.Validate(); err != nil {
		return nil, m, err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, m, err
	}
	sig, err := sign(ctx, id, data)
	if err != nil {
		return nil, m, err
	}
	if len(data)+len(sig) > MaxMetadata {
		return nil, m, errors.New("metadata exceeds 64 KiB")
	}
	out, err := os.CreateTemp("", "keyparcel-package-*")
	if err != nil {
		return nil, m, err
	}
	success := false
	defer func() {
		if !success {
			out.Close()
			os.Remove(out.Name())
		}
	}()
	enc, err := age.Encrypt(out, recipients...)
	if err != nil {
		return nil, m, err
	}
	if err = WriteHeader(enc, data, sig); err != nil {
		return nil, m, err
	}
	if _, err = staging.Seek(0, 0); err != nil {
		return nil, m, err
	}
	if _, err = io.Copy(enc, staging); err != nil {
		return nil, m, err
	}
	if err = enc.Close(); err != nil {
		return nil, m, err
	}
	if _, err = out.Seek(0, 0); err != nil {
		return nil, m, err
	}
	success = true
	return out, m, nil
}
func WriteHeader(w io.Writer, data, sig []byte) error {
	if len(data)+len(sig) > MaxMetadata {
		return errors.New("metadata exceeds 64 KiB")
	}
	if _, err := io.WriteString(w, Marker); err != nil {
		return err
	}
	for _, b := range [][]byte{data, sig} {
		if err := binary.Write(w, binary.BigEndian, uint32(len(b))); err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return nil
}
func ReadPackage(ctx context.Context, r io.Reader, id *Identity, peers map[string]string, expectedID string) (Manifest, io.Reader, error) {
	var m Manifest
	identity, err := agessh.ParseIdentity(id.private)
	if err != nil {
		return m, nil, err
	}
	plain, err := age.Decrypt(r, identity)
	if err != nil {
		return m, nil, fmt.Errorf("decrypt: %w", err)
	}
	marker := make([]byte, len(Marker))
	if _, err = io.ReadFull(plain, marker); err != nil {
		return m, nil, err
	}
	if string(marker) != Marker {
		return m, nil, errors.New("unsupported package marker")
	}
	total := 0
	fields := make([][]byte, 2)
	for i := range fields {
		var n uint32
		if err = binary.Read(plain, binary.BigEndian, &n); err != nil {
			return m, nil, err
		}
		if n == 0 || uint64(total)+uint64(n) > MaxMetadata {
			return m, nil, errors.New("invalid metadata length")
		}
		total += int(n)
		fields[i] = make([]byte, n)
		if _, err = io.ReadFull(plain, fields[i]); err != nil {
			return m, nil, err
		}
	}
	if err = decodeJSON(fields[0], &m); err != nil {
		return m, nil, fmt.Errorf("manifest: %w", err)
	}
	if err = m.Validate(); err != nil {
		return m, nil, err
	}
	if m.ID != expectedID {
		return m, nil, errors.New("manifest transfer ID does not match filename")
	}
	intended := false
	for _, fp := range m.Recipients {
		if fp == ssh.FingerprintSHA256(id.Public) {
			intended = true
		}
	}
	if !intended {
		return m, nil, errors.New("device is not an intended recipient")
	}
	var sender ssh.PublicKey
	if m.Sender == ssh.FingerprintSHA256(id.Public) {
		sender = id.Public
	}
	for _, key := range peers {
		p, e := ParsePublic(key)
		if e == nil && ssh.FingerprintSHA256(p) == m.Sender {
			sender = p
			break
		}
	}
	if sender == nil {
		return m, nil, errors.New("unapproved sender")
	}
	if err = verify(ctx, sender, fields[0], fields[1]); err != nil {
		return m, nil, err
	}
	return m, plain, nil
}
func VerifyContent(r io.Reader, m Manifest) (*os.File, error) {
	f, err := os.CreateTemp("", "keyparcel-receive-*")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.CopyN(io.MultiWriter(f, h), r, m.Size)
	if err == io.EOF {
		return nil, fmt.Errorf("content size/hash mismatch: expected %d bytes, got %d", m.Size, n)
	}
	if err != nil {
		return nil, fmt.Errorf("content verification: %w", err)
	}
	var extra [1]byte
	_, err = io.ReadFull(r, extra[:])
	if err == nil {
		return nil, errors.New("content size/hash mismatch: trailing content")
	}
	if err != io.EOF {
		return nil, fmt.Errorf("content verification: %w", err)
	}
	if n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return nil, errors.New("content size/hash mismatch")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	if m.Type == "text" {
		if err = validateText(f); err != nil {
			return nil, err
		}
		if _, err = f.Seek(0, 0); err != nil {
			return nil, err
		}
	}
	success = true
	return f, nil
}
func validateText(r io.Reader) error {
	b := bufio.NewReader(r)
	for {
		rune, n, err := b.ReadRune()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if rune == utf8.RuneError && n == 1 {
			return errors.New("text is not valid UTF-8")
		}
	}
}
