package core

import (
	"bytes"
	"context"
	"encoding/pem"
	"os"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestMemoryIdentityAndSignatureValidation(t *testing.T) {
	id := testIdentity(t)
	data, err := os.ReadFile(id.Path)
	if err != nil {
		t.Fatal(err)
	}
	id, err = ParseIdentity(data)
	if err != nil {
		t.Fatal(err)
	}
	clear(data)
	t.Setenv("PATH", t.TempDir())
	message := []byte("signed bytes\x00\xff")
	signature, err := sign(context.Background(), id, message)
	if err != nil {
		t.Fatal("memory-only signing:", err)
	}
	if err := verify(context.Background(), id.Public, message, signature); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(signature)
	var original signatureEnvelope
	if err := ssh.Unmarshal(block.Bytes[6:], &original); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*signatureEnvelope)
	}{
		{"version", func(s *signatureEnvelope) { s.Version = 2 }},
		{"namespace", func(s *signatureEnvelope) { s.Namespace = "other" }},
		{"public-key", func(s *signatureEnvelope) { s.PublicKey = nil }},
		{"reserved", func(s *signatureEnvelope) { s.Reserved = "changed" }},
		{"hash", func(s *signatureEnvelope) { s.HashAlgorithm = "sha1" }},
		{"signature", func(s *signatureEnvelope) { s.Signature = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := original
			tc.mutate(&envelope)
			bad := pem.EncodeToMemory(&pem.Block{Type: "SSH SIGNATURE", Bytes: append([]byte("SSHSIG"), ssh.Marshal(envelope)...)})
			if err := verify(context.Background(), id.Public, message, bad); err == nil {
				t.Fatal("invalid signature accepted")
			}
		})
	}
	if err := verify(context.Background(), id.Public, []byte("changed"), signature); err == nil {
		t.Fatal("changed message accepted")
	}
	for _, bad := range [][]byte{append([]byte("prefix"), signature...), append(bytes.Clone(signature), signature...), []byte("bad signature")} {
		if err := verify(context.Background(), id.Public, message, bad); err == nil {
			t.Fatal("invalid armor accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sign(ctx, id, message); err == nil {
		t.Fatal("canceled signing accepted")
	}
	if err := verify(ctx, id.Public, message, signature); err == nil {
		t.Fatal("canceled verification accepted")
	}
}
