package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
)

func TestEnrollmentValidatesPrivateIdentityAndSibling(t *testing.T) {
	dir := t.TempDir()
	path := identity(t, dir) // Arbitrary filename, not id_ed25519 or a required extension.
	id, err := core.LoadIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := core.LoadIdentity(identity(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	public := core.PublicText(id.Public)
	cases := []struct{ name, sibling, want string }{
		{"missing", "", public},
		{"matching comment", public + " desktop@example 世界\n", public + " desktop@example 世界"},
		{"matching without comment", public + "\n", public},
		{"mismatched", core.PublicText(other.Public) + " other device\n", public},
		{"invalid", "not a key", public},
		{"authorized options", "command=\"false\" " + public + " misleading\n", public},
		{"multiple", public + " first\n" + public + " second\n", public},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Remove(path + ".pub")
			if tc.sibling != "" {
				if err := os.WriteFile(path+".pub", []byte(tc.sibling), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := readEnrollment(path)
			if err != nil || got.Public != tc.want || got.Fingerprint != ssh.FingerprintSHA256(id.Public) {
				t.Fatalf("enrollment=%+v error=%v", got, err)
			}
		})
	}
	if err := os.WriteFile(path, []byte("invalid private identity"), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path+".pub", []byte(public+" valid sibling"), 0600)
	if got, err := readEnrollment(path); err == nil || got.Public != "" || got.Fingerprint != "" {
		t.Fatal("sibling replaced invalid private identity", got, err)
	}
}

func TestEnrollmentPrivateFormatsAndLocalPaths(t *testing.T) {
	dir := t.TempDir()
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	smallRSA, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	openSSH, err := ssh.MarshalPrivateKey(ed, "")
	if err != nil {
		t.Fatal(err)
	}
	protected, err := ssh.MarshalPrivateKeyWithPassphrase(ed, "", []byte("fixture password"))
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(ed)
	if err != nil {
		t.Fatal(err)
	}
	ecPEM, err := x509.MarshalECPrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		block *pem.Block
		valid bool
	}{
		{"openssh", openSSH, true},
		{"pkcs8", &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}, true},
		{"rsa", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}, true},
		{"weak rsa", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(smallRSA)}, false},
		{"ecdsa", &pem.Block{Type: "EC PRIVATE KEY", Bytes: ecPEM}, false},
		{"protected", protected, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			private := pem.EncodeToMemory(tc.block)
			if err := os.WriteFile(path, private, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := readEnrollment(path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if !tc.valid && got != (enrollmentDetails{}) {
				t.Fatal("invalid private key exposed enrollment data")
			}
			if strings.Contains(got.Public, "PRIVATE KEY") {
				t.Fatal("private bytes exposed")
			}
		})
	}
	for _, path := range []string{"", filepath.Join(dir, "missing"), dir} {
		if _, err := readEnrollment(path); err == nil {
			t.Fatalf("invalid identity %q accepted", path)
		}
	}
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnrollment(pipe); err == nil {
		t.Fatal("identity reader accepted pipe")
	}
	path := filepath.Join(dir, "unreadable")
	os.WriteFile(path, pem.EncodeToMemory(openSSH), 0000)
	if os.Getuid() != 0 {
		if _, err := readEnrollment(path); err == nil {
			t.Fatal("unreadable identity accepted")
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if defaultIdentity() != "" {
		t.Fatal("nonexistent default identity prefilled")
	}
	os.MkdirAll(filepath.Join(home, ".ssh"), 0700)
	os.WriteFile(filepath.Join(home, ".ssh/id_ed25519"), pem.EncodeToMemory(openSSH), 0600)
	if defaultIdentity() != "~/.ssh/id_ed25519" {
		t.Fatal("existing default identity not suggested")
	}
	if _, err := readEnrollment("~/.ssh/id_ed25519"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh/id_rsa")); !os.IsNotExist(err) {
		t.Fatal("default lookup generated or selected another key")
	}
}
