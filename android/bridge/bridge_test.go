package bridge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
)

func TestServerKeysAreIndependentOfPeerKeySupport(t *testing.T) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	public := core.PublicText(key)
	fp, err := ServerFingerprint(public)
	if err != nil || fp != ssh.FingerprintSHA256(key) {
		t.Fatalf("ECDSA server fingerprint: %q %v", fp, err)
	}
	if err := ValidatePeer("server", public); err == nil {
		t.Fatal("ECDSA server key was accepted as an age peer")
	}
	if _, err := ServerFingerprint(public + "\n" + public); err == nil {
		t.Fatal("multiple trusted server keys were accepted")
	}
}

func TestServerKeyProbeValidation(t *testing.T) {
	for _, tc := range []struct {
		address       string
		port, timeout int
	}{
		{"unused", 22, 0}, {"unused", 22, -1}, {"", 22, 100}, {"unused", 0, 100}, {"unused", 65536, 100},
	} {
		if key, err := ProbeServerKey(tc.address, tc.port, tc.timeout); err == nil || key != "" {
			t.Fatalf("invalid probe settings returned a candidate: %+v %q %v", tc, key, err)
		}
	}
}

func TestIdentityAndPeerValidation(t *testing.T) {
	key, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	public, err := PublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := core.ParsePublic(public)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(public)
	if err != nil || fingerprint != ssh.FingerprintSHA256(parsed) {
		t.Fatalf("bridge fingerprint differs from SSH fingerprint: %v", err)
	}
	c := &Client{config: &core.Config{Peers: map[string]string{}}}
	if err := c.Receive("unused-id", nil, false); err == nil {
		t.Fatal("missing delivery callback was accepted")
	}
	for _, approval := range [][2]string{{"self", public}, {"invalid name", public}, {"peer", "invalid key"}} {
		if err := c.Approve(approval[0], approval[1]); err == nil || len(c.config.Peers) != 0 {
			t.Fatalf("invalid approval modified peer trust: %q", approval)
		}
	}
	if err := c.Approve("peer", public); err != nil || c.config.Peers["peer"] != public {
		t.Fatalf("valid peer approval failed: %v", err)
	}
	if _, err := PublicKey([]byte("invalid private key")); err == nil {
		t.Fatal("malformed private identity was accepted")
	}
	if _, err := c.SendFile("peer", t.TempDir()); err == nil {
		t.Fatal("directory file input was accepted")
	}
	for _, name := range []string{"", ".", "..", "../file", "folder/file", "folder\\file", "bad\x00name"} {
		if _, err := c.SendNamedFile("peer", "unused-staging-file", name); err == nil || err.Error() != "invalid filename" {
			t.Fatalf("unsafe document name %q was not rejected before reading: %v", name, err)
		}
	}
}

func TestAndroidTemporaryDirectory(t *testing.T) {
	t.Setenv("TMPDIR", os.TempDir())
	if err := Initialize("relative-directory"); err == nil {
		t.Fatal("relative temporary directory was accepted")
	}
	dir := t.TempDir()
	if err := Initialize(dir); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp("", "android-staging-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())
	if filepath.Dir(f.Name()) != dir {
		t.Fatal("Go staging did not use Android's app-private cache directory")
	}
}

func TestReplacingApprovalSnapshotRemovesRevokedPeers(t *testing.T) {
	private, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	public, err := PublicKey(private)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{config: &core.Config{Peers: map[string]string{"old": public}}}
	if err := c.SetPeers(`{}`); err != nil {
		t.Fatal(err)
	}
	if len(c.config.Peers) != 0 {
		t.Fatal("revoked approval remained on the reused client")
	}
	if err := c.SetPeers(`{"self":"bad"}`); err == nil {
		t.Fatal("invalid snapshot was accepted")
	}
	if len(c.config.Peers) != 0 {
		t.Fatal("failed validation modified approvals")
	}
}
