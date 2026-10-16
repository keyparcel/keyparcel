package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
)

const clipboardLimit = 16 * 1024 * 1024
const previewLimit = 65536

type store struct {
	path string
	mu   sync.Mutex
}
type profile struct {
	Config              *core.Config
	Target              string
	Host                core.Host
	Public, Fingerprint string
}

func (s *store) load(target string) (profile, error) {
	cfg, err := core.LoadConfig(s.path)
	if err != nil {
		return profile{}, err
	}
	if target == "" {
		target = cfg.DefaultHost
	}
	if target == "" && len(cfg.Hosts) == 1 {
		for name := range cfg.Hosts {
			target = name
		}
	}
	p := profile{Config: cfg, Target: target}
	if target == "" {
		return p, nil
	}
	h, ok := cfg.Hosts[target]
	if !ok {
		return p, fmt.Errorf("host %q is not configured; save it in Settings first", target)
	}
	p.Host = h
	id, err := core.LoadIdentity(h.Identity)
	if err != nil {
		return p, err
	}
	p.Public, p.Fingerprint = core.PublicText(id.Public), ssh.FingerprintSHA256(id.Public)
	if p.Fingerprint != h.Fingerprint {
		return p, errors.New("selected identity changed; configure it explicitly in Settings")
	}
	return p, nil
}

func (s *store) update(change func(*core.Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reload before each edit so a CLI change is not overwritten by a UI snapshot.
	cfg, err := core.LoadConfig(s.path)
	if err != nil {
		return err
	}
	if err = change(cfg); err != nil {
		return err
	}
	return cfg.Save(s.path)
}

func (s *store) setup(target, identity, root, discovery string) error {
	if err := core.ValidateTarget(target); err != nil {
		return err
	}
	if root == "" || discovery == "" {
		return errors.New("remote storage and discovery paths must not be empty")
	}
	id, err := core.LoadIdentity(identity)
	if err != nil {
		return err
	}
	return s.update(func(c *core.Config) error {
		c.Hosts[target] = core.Host{Identity: id.Path, Fingerprint: ssh.FingerprintSHA256(id.Public), Root: root, Discovery: discovery}
		c.DefaultHost = target
		return nil
	})
}

func (s *store) approve(name, public string) error {
	if err := core.ValidateName(name); err != nil {
		return err
	}
	if _, err := core.ParsePublic(public); err != nil {
		return err
	}
	return s.update(func(c *core.Config) error {
		if _, ok := c.Peers[name]; ok {
			return errors.New("peer name already approved; forget it explicitly before replacement")
		}
		key, _ := core.ParsePublic(public)
		for existing, text := range c.Peers {
			other, err := core.ParsePublic(text)
			if err != nil {
				return err
			}
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(other) {
				return fmt.Errorf("key is already approved as %q", existing)
			}
		}
		c.Peers[name] = public
		return nil
	})
}

func (s *store) forget(name, public string) error {
	return s.update(func(c *core.Config) error {
		if c.Peers[name] != public {
			return errors.New("peer approval changed; refresh Devices before forgetting it")
		}
		delete(c.Peers, name)
		return nil
	})
}

func openInput(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("choose a regular file")
	}
	// Nonblocking open also covers a replacement with a pipe between stat/open.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("choose a regular file")
	}
	return f, nil
}

func clipboardText(text string) error {
	if len(text) > clipboardLimit {
		return errors.New("text exceeds the 16 MiB clipboard limit")
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return errors.New("clipboard requires valid UTF-8 without NUL bytes")
	}
	return nil
}

type received struct {
	ID                                    string
	Profile                               profile
	Preview, Notice, Clipboard, CopyError string
	PreviewNUL                            bool
}

func readText(f *os.File, m core.Manifest, p profile) (*received, error) {
	b, err := io.ReadAll(io.LimitReader(f, 4*previewLimit+4))
	if err != nil {
		return nil, err
	}
	end := 0
	for count := 0; count < previewLimit && end < len(b); count++ {
		_, size := utf8.DecodeRune(b[end:])
		end += size
	}
	raw := string(b[:end])
	if !utf8.ValidString(raw) {
		return nil, errors.New("received text is not valid UTF-8")
	}
	v := &received{ID: m.ID, Profile: p, Preview: raw}
	if int64(end) < m.Size {
		v.Notice = "Preview truncated; the complete remote transfer is retained."
	}
	if strings.ContainsRune(raw, 0) {
		v.PreviewNUL = true
		v.Preview = strings.ReplaceAll(raw, "\x00", "␀")
		v.Notice += " NUL bytes are displayed as ␀; selection copying is unavailable for this preview."
	}
	if m.Size > clipboardLimit {
		v.CopyError = "Complete message exceeds the 16 MiB clipboard limit; its remote copy is retained."
		return v, nil
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	b, err = io.ReadAll(io.LimitReader(f, clipboardLimit+1))
	if err != nil {
		return nil, err
	}
	if err = clipboardText(string(b)); err != nil {
		v.CopyError = err.Error()
	} else {
		v.Clipboard = string(b)
	}
	return v, nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Confirmation belongs to one inbox and approval snapshot, even for identical IDs.
type pendingDelete struct {
	ID      string
	Profile profile
}

func (p pendingDelete) matches(current profile) bool {
	return p.Profile.Target == current.Target && p.Profile.Host == current.Host &&
		p.Profile.Config != nil && current.Config != nil && reflect.DeepEqual(p.Profile.Config.Peers, current.Config.Peers)
}
func containsItem(items []core.Item, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
