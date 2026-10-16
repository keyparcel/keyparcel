package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

type Host struct {
	Identity    string `json:"identity"`
	Fingerprint string `json:"fingerprint"`
	Root        string `json:"root"`
	Discovery   string `json:"discovery"`
}
type Config struct {
	Hosts       map[string]Host   `json:"hosts"`
	Peers       map[string]string `json:"peers"`
	DefaultHost string            `json:"default_host,omitempty"`
}
type Identity struct {
	Path    string
	Public  ssh.PublicKey
	private []byte
}

// GenerateIdentity returns an ordinary Ed25519 SSH private key for caller-managed storage.
func GenerateIdentity() ([]byte, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(key, "keyparcel")
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
}

func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "keyparcel", "config.json"), nil
}
func LoadConfig(file string) (*Config, error) {
	c := &Config{Hosts: map[string]Host{}, Peers: map[string]string{}}
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err = decodeJSON(b, c); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if c.Hosts == nil || c.Peers == nil {
		return nil, errors.New("invalid configuration: hosts and peers must be objects")
	}
	for target, h := range c.Hosts {
		if err = ValidateTarget(target); err != nil {
			return nil, err
		}
		if h.Identity == "" || h.Fingerprint == "" || h.Root == "" || h.Discovery == "" {
			return nil, fmt.Errorf("incomplete configuration for host %q", target)
		}
	}
	for name, key := range c.Peers {
		if err = ValidateName(name); err != nil {
			return nil, err
		}
		if _, err = ParsePublic(key); err != nil {
			return nil, fmt.Errorf("invalid peer %q: %w", name, err)
		}
	}
	if c.DefaultHost != "" {
		if _, ok := c.Hosts[c.DefaultHost]; !ok {
			return nil, errors.New("default host is not configured")
		}
	}
	return c, nil
}
func (c *Config) Save(file string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), file)
}
func ResolvePath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(h, strings.TrimPrefix(p, "~/"))
	}
	return filepath.Abs(p)
}
func LoadIdentity(p string) (*Identity, error) {
	p, err := ResolvePath(p)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	id, err := ParseIdentity(b)
	if err != nil {
		return nil, err
	}
	id.Path = p
	return id, nil
}

// ParseIdentity loads an app-supplied, unencrypted SSH identity into memory.
func ParseIdentity(b []byte) (*Identity, error) {
	key, err := ssh.ParsePrivateKey(b)
	if err != nil {
		var protected *ssh.PassphraseMissingError
		if errors.As(err, &protected) {
			return nil, errors.New("passphrase-protected identities are not supported yet")
		}
		return nil, fmt.Errorf("identity: %w", err)
	}
	if _, err = ParsePublic(PublicText(key.PublicKey())); err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	return &Identity{Public: key.PublicKey(), private: bytes.Clone(b)}, nil
}

func ParsePublic(s string) (ssh.PublicKey, error) {
	p, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(rest))) > 0 {
		return nil, errors.New("expected one public key")
	}
	if _, err = agessh.ParseRecipient(PublicText(p)); err != nil {
		return nil, err
	}
	return p, nil
}
func PublicText(p ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(p)))
}
func DeviceID(p ssh.PublicKey) string {
	h := sha256.Sum256(p.Marshal())
	return hex.EncodeToString(h[:])
}
func ValidateTarget(t string) error {
	if t == "" || strings.HasPrefix(t, "-") || strings.ContainsAny(t, " \t\r\n\x00") {
		return errors.New("host must be an SSH alias or user@host")
	}
	return nil
}
func ValidateName(n string) error {
	if n == "self" {
		return errors.New("peer name self is reserved for the selected device")
	}
	if n == "" || strings.ContainsAny(n, " \t\r\n\x00") {
		return errors.New("peer name must be nonempty and contain no whitespace or NUL")
	}
	return nil
}

// SuggestedPeerName returns an unchanged key comment only when it is an unused peer name.
func SuggestedPeerName(comment string, peers map[string]string) string {
	if ValidateName(comment) != nil {
		return ""
	}
	if _, exists := peers[comment]; exists {
		return ""
	}
	return comment
}

func (c *Config) Select(target string) (string, Host, error) {
	if target == "" {
		target = c.DefaultHost
	}
	h, ok := c.Hosts[target]
	if !ok {
		return "", Host{}, fmt.Errorf("host %q is not configured; use setup --host or select a saved --host", target)
	}
	return target, h, nil
}
func decodeJSON(b []byte, v any) error {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
