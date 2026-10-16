package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

func testIdentity(t *testing.T) *Identity {
	t.Helper()
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("SSH_AGENT_PID", "")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return writeTestIdentity(t, key)
}
func writeTestIdentity(t *testing.T, key any) *Identity {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(key, "test")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "identity")
	if err = os.WriteFile(file, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadIdentity(file)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func TestPackageExactBytes(t *testing.T) {
	alice, bob := testIdentity(t), testIdentity(t)
	for _, data := range [][]byte{{}, []byte("  hello 世界\n\n"), bytes.Repeat([]byte{0, 255, 31}, 65537)} {
		kind := "file"
		name := "fixture.bin"
		f, m, err := NewPackage(context.Background(), alice, []ssh.PublicKey{bob.Public}, kind, &name, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		_, r, err := ReadPackage(context.Background(), f, bob, map[string]string{"alice": PublicText(alice.Public)}, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		verified, err := VerifyContent(r, m)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := io.ReadAll(verified)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, actual) {
			t.Fatal("received bytes differ from fixed input")
		}
		verified.Close()
		os.Remove(verified.Name())
		f.Close()
		os.Remove(f.Name())
	}
}
func TestPackageRejection(t *testing.T) {
	alice, bob, wrong := testIdentity(t), testIdentity(t), testIdentity(t)
	f, m, err := NewPackage(context.Background(), alice, []ssh.PublicKey{bob.Public}, "text", nil, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	defer os.Remove(f.Name())
	cipher, _ := io.ReadAll(f)
	peers := map[string]string{"alice": PublicText(alice.Public)}
	for _, tc := range []struct {
		name     string
		id       *Identity
		peers    map[string]string
		expected string
	}{{"wrong key", wrong, peers, m.ID}, {"unapproved sender", bob, map[string]string{}, m.ID}, {"ID mismatch", bob, peers, strings.Repeat("0", 32)}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ReadPackage(context.Background(), bytes.NewReader(cipher), tc.id, tc.peers, tc.expected); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	for _, mutated := range [][]byte{cipher[:len(cipher)-1], append([]byte(nil), cipher...)} {
		mutated[len(mutated)-1] ^= 1
		_, r, err := ReadPackage(context.Background(), bytes.NewReader(mutated), bob, peers, m.ID)
		if err == nil {
			verified, e := VerifyContent(r, m)
			if e == nil {
				verified.Close()
				os.Remove(verified.Name())
				t.Fatal("tampered/truncated package accepted")
			}
		}
	}
}
func TestMetadataLimit(t *testing.T) {
	alice, bob := testIdentity(t), testIdentity(t)
	recipient, _ := agessh.NewEd25519Recipient(bob.Public)
	var ciphertext bytes.Buffer
	enc, err := age.Encrypt(&ciphertext, recipient)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(enc, Marker)
	binary.Write(enc, binary.BigEndian, uint32(MaxMetadata+1))
	enc.Close()
	_, _, err = ReadPackage(context.Background(), &ciphertext, bob, map[string]string{"alice": PublicText(alice.Public)}, strings.Repeat("a", 32))
	if err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatalf("expected metadata length rejection, got %v", err)
	}
}
func TestResolvePrefixAndFilename(t *testing.T) {
	ids := []string{"12345678" + strings.Repeat("a", 24), "12345678" + strings.Repeat("b", 24)}
	if _, err := ResolveID("12345678", ids); err == nil {
		t.Fatal("ambiguous prefix accepted")
	}
	if ShortID(ids[0], ids) != "12345678a" {
		t.Fatal("colliding prefix was not extended")
	}
	if id, err := ResolveID("12345678a", ids); err != nil || id != ids[0] {
		t.Fatalf("unique prefix failed: %s %v", id, err)
	}
	for _, p := range []string{"../12345678", "1234", "12345678/", strings.Repeat("F", 32)} {
		if _, err := ResolveID(p, ids); err == nil {
			t.Fatalf("invalid ID accepted: %q", p)
		}
	}
	for _, n := range []string{"../file", "/file", "a\\b", "..", "a\x00b"} {
		if SafeFilename(n) {
			t.Fatalf("unsafe name accepted: %q", n)
		}
	}
}
func TestSaveDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	name := "file.bin"
	dest := filepath.Join(dir, name)
	os.WriteFile(dest, []byte("original"), 0600)
	f, err := os.CreateTemp(t.TempDir(), "content")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString("replacement")
	f.Seek(0, 0)
	if _, err = SaveContent(dir, Manifest{Filename: &name}, f, false); err == nil {
		t.Fatal("overwrite succeeded")
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "original" {
		t.Fatal("existing content changed")
	}
}
func TestConfigInvalidAndAtomicPermissions(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	c := &Config{Hosts: map[string]Host{}, Peers: map[string]string{}}
	if err := c.Save(file); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0600 {
		t.Fatal("configuration is not owner-only")
	}
	os.WriteFile(file, []byte(`{"hosts":{},"peers":{},"default_host":"missing"}`), 0600)
	if _, err := LoadConfig(file); err == nil {
		t.Fatal("invalid default accepted")
	}
	os.WriteFile(file, []byte("bad JSON"), 0600)
	if _, err := LoadConfig(file); err == nil {
		t.Fatal("corrupted configuration reset silently")
	}
}
func TestManifestValidation(t *testing.T) {
	var m Manifest
	if err := json.Unmarshal([]byte(`{"version":1,"transfer_id":"00000000000000000000000000000000","type":"text","filename":"oops","size":0,"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`), &m); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err == nil {
		t.Fatal("invalid manifest accepted")
	}
}
func FuzzManifestAndPrefix(f *testing.F) {
	f.Add([]byte(`{}`), "deadbeef")
	f.Fuzz(func(t *testing.T, b []byte, prefix string) {
		if len(b) > MaxMetadata {
			return
		}
		var m Manifest
		if decodeJSON(b, &m) == nil {
			_ = m.Validate()
		}
		_, _ = ResolveID(prefix, []string{strings.Repeat("a", 32)})
	})
}

func TestSelfSignatureStillVerifiedWithoutApprovals(t *testing.T) {
	id, other := testIdentity(t), testIdentity(t)
	for _, sender := range []*Identity{id, other} {
		file, m, err := NewPackage(context.Background(), sender, []ssh.PublicKey{id.Public}, "text", nil, strings.NewReader("self test"))
		if err != nil {
			t.Fatal(err)
		}
		_, plain, err := ReadPackage(context.Background(), file, id, nil, m.ID)
		if sender == other {
			if err == nil || !strings.Contains(err.Error(), "unapproved sender") {
				t.Fatalf("other sender accepted: %v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			verified, e := VerifyContent(plain, m)
			if e != nil {
				t.Fatal(e)
			}
			verified.Close()
			os.Remove(verified.Name())
		}
		file.Close()
		os.Remove(file.Name())
	}
	if err := ValidateName("self"); err == nil {
		t.Fatal("reserved peer name accepted")
	}
}

func TestSaveDestinationForms(t *testing.T) {
	dir := t.TempDir()
	content := []byte("  preserved 世界\n\n")
	f, err := os.CreateTemp(t.TempDir(), "content")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.Write(content); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	name := "original.log"
	for _, tc := range []struct {
		path     string
		filename *string
		want     string
		failure  string
	}{
		{dir, nil, filepath.Join(dir, id+".txt"), ""},
		{dir + "/", &name, filepath.Join(dir, name), ""},
		{filepath.Join(dir, "text.log"), nil, filepath.Join(dir, "text.log"), ""},
		{filepath.Join(dir, "renamed.log"), &name, filepath.Join(dir, "renamed.log"), ""},
		{filepath.Join(dir, "text.log"), nil, "", "destination already exists"},
		{filepath.Join(dir, "missing") + "/", nil, "", "directory does not exist"},
		{filepath.Join(dir, "missing", "text.log"), nil, "", "directory does not exist"},
	} {
		if _, err = f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		got, err := SaveContent(tc.path, Manifest{ID: id, Filename: tc.filename}, f, false)
		if tc.failure != "" {
			if err == nil || !strings.Contains(err.Error(), tc.failure) {
				t.Fatalf("%s: %v", tc.path, err)
			}
		} else {
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			data, e := os.ReadFile(got)
			if e != nil || !bytes.Equal(data, content) {
				t.Fatalf("saved content: %q %v", data, e)
			}
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".keyparcel-save-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files retained: %v", leftovers)
	}
}

// Edge case: lexical cleanup of symlink/.. selects a different parent.
func TestSaveSymlinkParentTraversal(t *testing.T) {
	base, target := t.TempDir(), t.TempDir()
	for _, dir := range []string{filepath.Join(target, "subdir"), filepath.Join(target, "out")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(target, "subdir"), link); err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(t.TempDir(), "content")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	content := []byte("symlink traversal\n")
	if _, err = input.Write(content); err != nil {
		t.Fatal(err)
	}
	// Concatenation intentionally retains ..; filepath.Join would clean it away.
	parent := link + "/../out/"
	for _, tc := range []struct{ name, path string }{
		{"exact.txt", parent + "exact.txt"},
		{"directory.txt", parent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := input.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			name := tc.name
			if _, err := SaveContent(tc.path, Manifest{Filename: &name}, input, false); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(target, "out", tc.name))
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("actual destination content: %q, %v", got, err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(base, "out")); !os.IsNotExist(err) {
		t.Fatal("created lexically cleaned parent")
	}
}

func TestSaveOverwriteRegularFilesOnly(t *testing.T) {
	dir := t.TempDir()
	name := "output.txt"
	dest := filepath.Join(dir, name)
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err = input.WriteString("replacement\n"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{dest, dir} {
		if err := os.WriteFile(dest, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		input.Seek(0, 0)
		if _, err := SaveContent(target, Manifest{Filename: &name}, input, false); err == nil || !strings.Contains(err.Error(), "Rerun with --overwrite") {
			t.Fatalf("missing overwrite hint: %v", err)
		}
		input.Seek(0, 0)
		if _, err := SaveContent(target, Manifest{Filename: &name}, input, true); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dest)
		if err != nil || string(got) != "replacement\n" {
			t.Fatalf("replacement: %q %v", got, err)
		}
	}
	protected := filepath.Join(dir, "protected")
	if err := os.WriteFile(protected, []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(protected, link); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "folder")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{link, folder} {
		input.Seek(0, 0)
		// A directory at the resolved filename is not a destination directory.
		path := target
		manifest := Manifest{}
		if target == folder {
			path = dir
			folderName := "folder"
			manifest.Filename = &folderName
		}
		if _, err := SaveContent(path, manifest, input, true); err == nil || !strings.Contains(err.Error(), "not a regular file") || strings.Contains(err.Error(), "Rerun") {
			t.Fatalf("nonregular target accepted: %v", err)
		}
	}
	got, err := os.ReadFile(protected)
	if err != nil || string(got) != "protected" {
		t.Fatal("symlink target changed")
	}
	// A read failure must preserve the old file and remove temporary staging.
	unreadable, err := os.OpenFile(protected, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unreadable.Close()
	if _, err := SaveContent(protected, Manifest{}, unreadable, true); err == nil {
		t.Fatal("failed read accepted")
	}
	got, _ = os.ReadFile(protected)
	if string(got) != "protected" {
		t.Fatal("failed save changed old file")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".keyparcel-save-*"))
	if len(leftovers) != 0 {
		t.Fatal("staging files retained", leftovers)
	}
}
