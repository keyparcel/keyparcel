package core

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestRSAAndEd25519Packages(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := ssh.MarshalPrivateKeyWithPassphrase(key, "test", []byte("passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	protectedFile := filepath.Join(t.TempDir(), "protected")
	if err := os.WriteFile(protectedFile, pem.EncodeToMemory(protected), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(protectedFile); err == nil || !strings.Contains(err.Error(), "passphrase-protected") {
		t.Fatalf("protected RSA identity rejection: %v", err)
	}
	rsaID, edID := writeTestIdentity(t, key), testIdentity(t)
	if _, err := ParsePublic(PublicText(rsaID.Public)); err != nil {
		t.Fatal("RSA peer rejected:", err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherRSA := writeTestIdentity(t, otherKey)
	data := []byte{0, 255, 10, 32, 65}
	message, err := signatureMessage(data, Namespace, "", "sha512")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(rsaID.private)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := signer.Sign(rand.Reader, message)
	if err != nil {
		t.Fatal(err)
	}
	envelope := signatureEnvelope{1, rsaID.Public.Marshal(), Namespace, "", "sha512", ssh.Marshal(legacy)}
	legacyArmor := pem.EncodeToMemory(&pem.Block{Type: "SSH SIGNATURE", Bytes: append([]byte("SSHSIG"), ssh.Marshal(envelope)...)})
	if err := verify(context.Background(), rsaID.Public, data, legacyArmor); err == nil || !strings.Contains(err.Error(), "RSA requires SHA-2") {
		t.Fatalf("legacy RSA-SHA1 signature accepted: %v", err)
	}
	for _, tc := range []struct {
		name              string
		sender, recipient *Identity
	}{{"RSA-to-Ed25519", rsaID, edID}, {"Ed25519-to-RSA", edID, rsaID}, {"RSA-to-RSA", rsaID, otherRSA}} {
		t.Run(tc.name, func(t *testing.T) {
			name := "bytes.bin"
			f, m, err := NewPackage(context.Background(), tc.sender, []ssh.PublicKey{tc.recipient.Public}, "file", &name, bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())
			defer f.Close()
			_, plain, err := ReadPackage(context.Background(), f, tc.recipient, map[string]string{"sender": PublicText(tc.sender.Public)}, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := VerifyContent(plain, m)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(verified.Name())
			defer verified.Close()
			got, err := io.ReadAll(verified)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("content mismatch: %x, %v", got, err)
			}
		})
	}
}

func TestUnsupportedSSHKeys(t *testing.T) {
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		key    any
		reason string
	}{
		{"small-RSA", small, "RSA key size is too small"},
		{"ECDSA", ecdsaKey, "unknown SSH recipient type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, err := ssh.MarshalPrivateKey(tc.key, "test")
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "identity")
			if err := os.WriteFile(file, pem.EncodeToMemory(block), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadIdentity(file); err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("identity rejection: %v", err)
			}
			signer, err := ssh.NewSignerFromKey(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParsePublic(PublicText(signer.PublicKey())); err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("peer rejection: %v", err)
			}
		})
	}
}
