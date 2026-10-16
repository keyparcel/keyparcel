package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/pem"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// SSHSIG framing is defined by OpenSSH's PROTOCOL.sshsig.
type signatureEnvelope struct {
	Version       uint32
	PublicKey     []byte
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Signature     []byte
}

func signatureMessage(data []byte, namespace, reserved, algorithm string) ([]byte, error) {
	var digest []byte
	switch algorithm {
	case "sha512":
		h := sha512.Sum512(data)
		digest = h[:]
	case "sha256":
		h := sha256.Sum256(data)
		digest = h[:]
	default:
		return nil, errors.New("unsupported signature hash")
	}
	return append([]byte("SSHSIG"), ssh.Marshal(struct {
		Namespace, Reserved, HashAlgorithm string
		Hash                               []byte
	}{namespace, reserved, algorithm, digest})...), nil
}

func sign(ctx context.Context, id *Identity, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(id.private)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), id.Public.Marshal()) {
		return nil, errors.New("identity public key mismatch")
	}
	message, _ := signatureMessage(data, Namespace, "", "sha512")
	var signature *ssh.Signature
	if signer.PublicKey().Type() == ssh.KeyAlgoRSA {
		signature, err = signer.(ssh.AlgorithmSigner).SignWithAlgorithm(rand.Reader, message, ssh.KeyAlgoRSASHA512)
	} else {
		signature, err = signer.Sign(rand.Reader, message)
	}
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	envelope := signatureEnvelope{1, signer.PublicKey().Marshal(), Namespace, "", "sha512", ssh.Marshal(signature)}
	return pem.EncodeToMemory(&pem.Block{Type: "SSH SIGNATURE", Bytes: append([]byte("SSHSIG"), ssh.Marshal(envelope)...)}), nil
}

func verify(ctx context.Context, key ssh.PublicKey, data, armored []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	block, rest := pem.Decode(armored)
	if block == nil || block.Type != "SSH SIGNATURE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.HasPrefix(armored, []byte("-----BEGIN SSH SIGNATURE-----")) || !bytes.HasPrefix(block.Bytes, []byte("SSHSIG")) {
		return errors.New("invalid signature armor")
	}
	var envelope signatureEnvelope
	if err := ssh.Unmarshal(block.Bytes[6:], &envelope); err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}
	if envelope.Version != 1 || envelope.Namespace != Namespace || !bytes.Equal(envelope.PublicKey, key.Marshal()) {
		return errors.New("invalid signature version, namespace or public key")
	}
	var signature ssh.Signature
	if err := ssh.Unmarshal(envelope.Signature, &signature); err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}
	if key.Type() == ssh.KeyAlgoRSA && signature.Format != ssh.KeyAlgoRSASHA256 && signature.Format != ssh.KeyAlgoRSASHA512 {
		return errors.New("invalid signature: RSA requires SHA-2")
	}
	message, err := signatureMessage(data, envelope.Namespace, envelope.Reserved, envelope.HashAlgorithm)
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}
	if err := key.Verify(message, &signature); err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}
	return ctx.Err()
}
