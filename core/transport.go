package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Session struct {
	Client         *sftp.Client
	Identity       *Identity
	Root           string
	DiscoveryPath  string
	ctx            context.Context
	closeTransport func() error
	channel        *sftpChannel
	openSFTP       func() (*sftpChannel, error)
	waitTransport  func() error
	access         *remoteAccess
	channelMu      sync.Mutex
	channels       []*sftpChannel
	channelsClosed bool
	closeOnce      sync.Once
	closeErr       error
}

func remotePath(cwd, p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(cwd, p)
}
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.channelMu.Lock()
		s.channelsClosed = true
		channels := s.channels
		s.channels = nil
		s.channelMu.Unlock()
		s.closeErr = s.closeTransport()
		for _, channel := range channels {
			channel.close()
		}
	})
	return s.closeErr
}
func (s *Session) InboxDir() string { return path.Join(s.Root, "inbox", DeviceID(s.Identity.Public)) }

type Device struct {
	Name        string `json:"name,omitempty"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
	Comment     string `json:"comment,omitempty"`
	Trusted     bool   `json:"trusted"`
	Self        bool   `json:"self"`
	Discovered  bool   `json:"discovered"`
	Supported   bool   `json:"supported"`
}

func (s *Session) Discover(c *Config) ([]Device, error) {
	use, err := s.commandAvailable(s.ctx, false)
	if err != nil {
		return nil, err
	}
	if use {
		return s.DiscoverAccelerated(c)
	}
	return s.DiscoverSFTP(c)
}
func (s *Session) DiscoverAccelerated(c *Config) ([]Device, error) {
	ctx, done := s.metadataContext()
	defer done()
	if _, err := s.commandAvailable(ctx, true); err != nil {
		return nil, err
	}
	var b []byte
	err := s.readCommand(ctx, s.DiscoveryPath, true, func(r io.Reader) error {
		var err error
		b, err = io.ReadAll(io.LimitReader(r, 4*1024*1024+1))
		if err == nil && len(b) > 4*1024*1024 {
			err = errors.New("discovery exceeds 4 MiB")
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	return s.parseDiscovery(c, b)
}
func (s *Session) DiscoverSFTP(c *Config) ([]Device, error) {
	f, err := s.Client.Open(s.DiscoveryPath)
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	b, readErr := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(b) > 4*1024*1024 {
		return nil, errors.New("discovery exceeds 4 MiB")
	}
	return s.parseDiscovery(c, b)
}
func (s *Session) parseDiscovery(c *Config, b []byte) ([]Device, error) {
	devices := []Device{}
	seen := map[string]bool{}
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		key, comment, _, _, e := ssh.ParseAuthorizedKey(line)
		if e != nil {
			return nil, fmt.Errorf("invalid discovery list: %w", e)
		}
		fp := ssh.FingerprintSHA256(key)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		_, supportedErr := ParsePublic(PublicText(key))
		d := Device{Fingerprint: fp, PublicKey: PublicText(key), Comment: comment, Discovered: true, Supported: supportedErr == nil}
		if bytes.Equal(key.Marshal(), s.Identity.Public.Marshal()) {
			d.Self = true
			d.Name = "self"
		}
		for name, pub := range c.Peers {
			p, _ := ParsePublic(pub)
			if p != nil && bytes.Equal(p.Marshal(), key.Marshal()) {
				if !d.Self {
					d.Name = name
				}
				d.Trusted = true
				break
			}
		}
		devices = append(devices, d)
	}
	missing := []Device{}
	for name, pub := range c.Peers {
		p, _ := ParsePublic(pub)
		fp := ssh.FingerprintSHA256(p)
		if !seen[fp] {
			missing = append(missing, Device{Name: name, Fingerprint: fp, PublicKey: pub, Trusted: true, Supported: true})
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Name < missing[j].Name })
	devices = append(devices, missing...)
	return devices, nil
}

type Delivery struct {
	Recipient   string `json:"recipient"`
	ID          string `json:"transfer_id"`
	Error       string `json:"error,omitempty"`
	Unconfirmed bool   `json:"unconfirmed,omitempty"`
}

func (s *Session) Send(c *Config, names []string, kind string, filename *string, input io.Reader) ([]Delivery, error) {
	return s.SendWithDiscovery(c, names, kind, filename, input, nil)
}

// SendWithDiscovery accepts a caller's discovery snapshot for this server/identity.
// Nil fetches fresh discovery; local recipient approvals are always checked.
func (s *Session) SendWithDiscovery(c *Config, names []string, kind string, filename *string, input io.Reader, devices []Device) ([]Delivery, error) {
	if len(names) == 0 {
		return nil, errors.New("at least one recipient is required")
	}
	if devices == nil {
		var err error
		devices, err = s.Discover(c)
		if err != nil {
			return nil, err
		}
	}
	present := map[string]bool{}
	for _, d := range devices {
		if d.Discovered {
			present[d.Fingerprint] = true
		}
	}
	keys := []ssh.PublicKey{}
	seen := map[string]bool{}
	for _, name := range names {
		pub, ok := c.Peers[name]
		if name == "self" {
			pub, ok = PublicText(s.Identity.Public), true
		}
		if !ok {
			return nil, fmt.Errorf("recipient %q is not approved", name)
		}
		p, e := ParsePublic(pub)
		if e != nil {
			return nil, e
		}
		fp := ssh.FingerprintSHA256(p)
		if seen[fp] {
			return nil, errors.New("duplicate recipient key")
		}
		seen[fp] = true
		if !present[fp] {
			return nil, fmt.Errorf("recipient %q is not enrolled on this host", name)
		}
		keys = append(keys, p)
	}
	phase(s.ctx, "Encrypting…")
	f, m, err := NewPackage(s.ctx, s.Identity, keys, kind, filename, input)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	defer os.Remove(f.Name())
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	result := []Delivery{}
	failed := false
	for i, p := range keys {
		phase(s.ctx, "Preparing upload…")
		d := Delivery{Recipient: names[i], ID: m.ID}
		dir := path.Join(s.Root, "inbox", DeviceID(p))
		var attempt [8]byte
		_, err = rand.Read(attempt[:])
		if err == nil {
			tmp := path.Join(dir, m.ID+"."+hex.EncodeToString(attempt[:])+".part")
			dest := path.Join(dir, m.ID+".age")
			var remote *sftp.File
			remote, err = s.Client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
			if errors.Is(err, os.ErrNotExist) {
				if err = s.Client.MkdirAll(dir); err == nil {
					remote, err = s.Client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
				}
			}
			if err == nil {
				_, err = f.Seek(0, 0)
				if err == nil {
					writer := newProgressWriter(s.ctx, remote, Progress{Phase: "Uploading", Total: info.Size(), Item: i + 1, Items: len(keys)})
					// Hide os.File.WriteTo so the copy uses our multi-packet buffer.
					_, err = io.CopyBuffer(writer, struct{ io.Reader }{f}, make([]byte, sftpTransferBufferSize(info.Size())))
				}
				if err == nil && s.ctx != nil {
					err = s.ctx.Err()
				}
				if err == nil {
					phase(s.ctx, "Publishing…")
				}
				closeErr := remote.Close()
				if err == nil {
					err = closeErr
				}
				if err == nil {
					err = s.Client.Rename(tmp, dest)
					// A lost acknowledgement cannot prove whether publication happened.
					d.Unconfirmed = connectionFailure(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
				}
				if err != nil {
					s.Client.Remove(tmp)
				}
			}
		}
		if err != nil {
			d.Error = err.Error()
			failed = true
		}
		result = append(result, d)
	}
	if failed {
		return result, errors.New("one or more recipients were not delivered")
	}
	return result, nil
}
func (s *Session) IDs() ([]string, error) {
	files, err := s.Client.ReadDir(s.InboxDir())
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, f := range files {
		base, ok := strings.CutSuffix(f.Name(), ".age")
		if ok && validHex(base, 32) {
			ids = append(ids, base)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
func ResolveID(prefix string, ids []string) (string, error) {
	if len(prefix) < 8 || len(prefix) > 32 || !validHex(prefix, len(prefix)) {
		return "", errors.New("transfer ID must be 8–32 lowercase hexadecimal characters")
	}
	match := ""
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			if match != "" {
				return "", errors.New("ambiguous transfer ID prefix; supply more characters")
			}
			match = id
		}
	}
	if match == "" {
		return "", errors.New("transfer not found")
	}
	return match, nil
}
func ShortID(id string, ids []string) string {
	for n := 8; n <= 32; n++ {
		unique := true
		for _, other := range ids {
			if other != id && strings.HasPrefix(other, id[:n]) {
				unique = false
				break
			}
		}
		if unique {
			return id[:n]
		}
	}
	return id
}

type Item struct {
	Manifest
	From       string    `json:"from"`
	UploadedAt time.Time `json:"uploaded_at"`
}
type Rejected struct {
	ID     string
	Reason string
}

func (s *Session) openPackage(id string) (*sftp.File, time.Time, error) {
	f, info, err := s.openPackageInfo(id)
	if err != nil {
		return nil, time.Time{}, err
	}
	return f, info.ModTime().UTC(), nil
}
func (s *Session) openPackageInfo(id string) (*sftp.File, os.FileInfo, error) {
	p := path.Join(s.InboxDir(), id+".age")
	info, err := s.Client.Lstat(p)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("package is not a regular file")
	}
	f, err := s.Client.Open(p)
	return f, info, err
}
func (s *Session) ListSFTP(c *Config) ([]Item, []Rejected, []string, error) {
	ids, err := s.IDs()
	if err != nil {
		return nil, nil, nil, err
	}
	items := []Item{}
	bad := []Rejected{}
	for _, id := range ids {
		f, uploaded, e := s.openPackage(id)
		// EOF from a control request is a broken transport, not a truncated package.
		if errors.Is(e, io.EOF) || errors.Is(e, io.ErrUnexpectedEOF) {
			return nil, nil, nil, fmt.Errorf("open package %s: %w", id, e)
		}
		var item Item
		if e == nil {
			item, e = s.readInboxItem(c, id, uploaded, io.LimitReader(f, inboxPrefixLimit))
			closeErr := f.Close()
			if closeErr != nil {
				return nil, nil, nil, fmt.Errorf("close package %s: %w", id, closeErr)
			}
		}
		if s.ctx != nil && s.ctx.Err() != nil {
			return nil, nil, nil, s.ctx.Err()
		}
		if connectionFailure(e) {
			return nil, nil, nil, fmt.Errorf("list package %s: %w", id, e)
		}
		if e != nil {
			bad = append(bad, Rejected{id, e.Error()})
			continue
		}
		items = append(items, item)
	}
	sortInbox(items)
	return items, bad, ids, nil
}

func sortInbox(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].UploadedAt.Equal(items[j].UploadedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].UploadedAt.After(items[j].UploadedAt)
	})
}

func (s *Session) readInboxItem(c *Config, id string, uploaded time.Time, r io.Reader) (Item, error) {
	m, _, err := ReadPackage(s.ctx, r, s.Identity, c.Peers, id)
	if err != nil {
		return Item{}, err
	}
	name := ""
	for n, pub := range c.Peers {
		p, _ := ParsePublic(pub)
		if ssh.FingerprintSHA256(p) == m.Sender {
			name = n
			break
		}
	}
	if m.Sender == ssh.FingerprintSHA256(s.Identity.Public) {
		name = "self"
	}
	return Item{Manifest: m, From: name, UploadedAt: uploaded}, nil
}

// Transport failures may be local I/O errors or SFTP status responses.
func connectionFailure(err error) bool {
	var status *sftp.StatusError
	if errors.As(err, &status) {
		err = status.FxCode()
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, sftp.ErrSSHFxNoConnection) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}
func (s *Session) Receive(c *Config, prefix string, deliver func(Manifest, *os.File) error, keep bool) (Manifest, error) {
	return s.receive(c, prefix, deliver, keep, "")
}
func (s *Session) ReceiveSFTP(c *Config, prefix string, deliver func(Manifest, *os.File) error, keep bool) (Manifest, error) {
	return s.receive(c, prefix, deliver, keep, "sftp")
}
func (s *Session) ReceiveAccelerated(c *Config, prefix string, deliver func(Manifest, *os.File) error, keep bool) (Manifest, error) {
	return s.receive(c, prefix, deliver, keep, "ssh")
}
func (s *Session) receive(c *Config, prefix string, deliver func(Manifest, *os.File) error, keep bool, method string) (Manifest, error) {
	var m Manifest
	if len(prefix) < 8 || len(prefix) > 32 || !validHex(prefix, len(prefix)) {
		return m, errors.New("transfer ID must be 8–32 lowercase hexadecimal characters")
	}
	id := prefix
	if len(prefix) < 32 {
		ids, err := s.IDs()
		if err != nil {
			return m, err
		}
		id, err = ResolveID(prefix, ids)
		if err != nil {
			return m, err
		}
	}
	var staging *os.File
	verify := func(input io.Reader) error {
		var plain io.Reader
		var err error
		m, plain, err = ReadPackage(s.ctx, input, s.Identity, c.Peers, id)
		if err != nil {
			return err
		}
		staging, err = VerifyContent(plain, m)
		phase(s.ctx, "Verifying…")
		return err
	}
	defer func() {
		if staging != nil {
			staging.Close()
			os.Remove(staging.Name())
		}
	}()
	var err error
	use := method == "ssh"
	if method == "" {
		use, err = s.commandAvailable(s.ctx, false)
	} else if use {
		_, err = s.commandAvailable(s.ctx, true)
	}
	if err != nil {
		return m, err
	}
	if use {
		err = s.readCommand(s.ctx, path.Join(s.InboxDir(), id+".age"), false, verify)
	} else {
		var f *sftp.File
		var info os.FileInfo
		f, info, err = s.openPackageInfo(id)
		if err == nil {
			input := bufio.NewReaderSize(f, sftpTransferBufferSize(info.Size()))
			err = verify(newProgressReader(s.ctx, input, Progress{Phase: "Downloading", Total: info.Size()}))
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return m, err
	}
	if s.ctx != nil && s.ctx.Err() != nil {
		return m, s.ctx.Err()
	}
	phase(s.ctx, "Delivering…")
	if err = deliver(m, staging); err != nil {
		return m, fmt.Errorf("delivery failed; remote package retained: %w", err)
	}
	if !keep {
		if err = s.Client.Remove(path.Join(s.InboxDir(), id+".age")); err != nil {
			return m, fmt.Errorf("delivery succeeded; remote cleanup failed (removal unconfirmed): %w", err)
		}
	}
	return m, nil
}
func (s *Session) Delete(prefixes []string, all bool) (int, []Rejected, error) {
	list := all || len(prefixes) == 0
	for _, prefix := range prefixes {
		if len(prefix) >= 8 && len(prefix) < 32 && validHex(prefix, len(prefix)) {
			list = true
			break
		}
	}
	var ids []string
	if list {
		// List before changing a mixed batch, preserving listing-failure retention.
		var err error
		ids, err = s.IDs()
		if err != nil {
			return 0, nil, err
		}
	}
	if all {
		prefixes = ids
	}
	count := 0
	failures := []Rejected{}
	seen := map[string]bool{}
	for _, prefix := range prefixes {
		id := prefix
		var e error
		if !validHex(id, 32) {
			id, e = ResolveID(prefix, ids)
		}
		if e != nil {
			failures = append(failures, Rejected{prefix, e.Error()})
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if e = s.Client.Remove(path.Join(s.InboxDir(), id+".age")); e != nil {
			failures = append(failures, Rejected{id, e.Error()})
		} else {
			count++
		}
	}
	if len(failures) > 0 {
		return count, failures, fmt.Errorf("%d deletion error(s)", len(failures))
	}
	return count, failures, nil
}

func SaveContent(dest string, m Manifest, f *os.File, overwrite bool) (string, error) {
	return SaveContentContext(context.Background(), dest, m, f, overwrite)
}

// SaveContentContext cancels staging before publication. Cancellation closes f
// to interrupt a blocked source read; the caller owns f otherwise.
func SaveContentContext(ctx context.Context, dest string, m Manifest, f *os.File, overwrite bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(dest)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("destination %q: %w", dest, err)
	}
	if err == nil && info.IsDir() {
		name := m.ID + ".txt"
		if m.Filename != nil {
			name = *m.Filename
		}
		if !SafeFilename(name) {
			return "", errors.New("invalid filename")
		}
		if !strings.HasSuffix(dest, string(os.PathSeparator)) {
			dest += string(os.PathSeparator)
		}
		dest += name
	} else if err != nil && strings.HasSuffix(dest, string(os.PathSeparator)) {
		return "", fmt.Errorf("destination directory does not exist: %s", dest)
	}
	if err := checkSaveTarget(dest, overwrite); err != nil {
		return "", err
	}
	// Preserve symlink/.. traversal so staging uses the destination's filesystem.
	dir, _ := filepath.Split(dest)
	if dir == "" {
		dir = "."
	}
	info, err = os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("destination directory does not exist: %s", dir)
	}
	if err != nil {
		return "", fmt.Errorf("destination directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("destination parent is not a directory: %s", dir)
	}
	tmp, err := os.CreateTemp(dir, ".keyparcel-save-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	stop := context.AfterFunc(ctx, func() { f.Close(); tmp.Close(); os.Remove(tmp.Name()) })
	defer stop()
	var input io.Reader = f
	if ctx.Done() != nil {
		input = &saveReader{ctx: ctx, file: f}
	}
	if _, err = io.Copy(tmp, input); err != nil {
		tmp.Close()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if overwrite {
		if err = checkSaveTarget(dest, true); err != nil {
			return "", err
		}
		err = os.Rename(tmp.Name(), dest)
	} else {
		// Link publishes without replacing a concurrently created file.
		err = os.Link(tmp.Name(), dest)
	}
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if e := checkSaveTarget(dest, false); e != nil {
				return "", e
			}
		}
		return "", fmt.Errorf("save destination %q: %w", dest, err)
	}
	return dest, nil
}

type saveReader struct {
	ctx  context.Context
	file *os.File
}

func (r *saveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(p)
}

func checkSaveTarget(dest string, overwrite bool) error {
	info, err := os.Lstat(dest)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("destination %q: %w", dest, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("destination is not a regular file: %s", dest)
	}
	if !overwrite {
		return fmt.Errorf("destination already exists: %s\nRerun with --overwrite to replace the existing file", dest)
	}
	return nil
}
