package core

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGeneratedIdentityRoundTrip(t *testing.T) {
	first, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("generated identities reused private-key bytes")
	}
	id, err := ParseIdentity(first)
	if err != nil {
		t.Fatal(err)
	}
	if id.Public.Type() != ssh.KeyAlgoED25519 || id.Path != "" {
		t.Fatal("generated identity must be an in-memory ordinary Ed25519 key")
	}
	content := []byte("generated identity 世界\n")
	f, m, err := NewPackage(context.Background(), id, []ssh.PublicKey{id.Public}, "text", nil, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	defer os.Remove(f.Name())
	reloaded, err := ParseIdentity(first)
	if err != nil {
		t.Fatal(err)
	}
	_, plain, err := ReadPackage(context.Background(), f, reloaded, nil, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyContent(plain, m)
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	defer os.Remove(verified.Name())
	got, err := io.ReadAll(verified)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("generated identity could not sign and decrypt exact content: %v", err)
	}
}
