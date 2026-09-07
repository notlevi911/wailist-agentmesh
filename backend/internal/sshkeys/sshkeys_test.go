package sshkeys

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateProducesUsableEd25519Pair(t *testing.T) {
	pub, privPEM, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Errorf("public key = %q, want an ssh-ed25519 prefix", pub)
	}
	if !strings.Contains(privPEM, "OPENSSH PRIVATE KEY") {
		t.Errorf("private key is not an OpenSSH PEM:\n%s", privPEM)
	}

	signer, err := ssh.ParsePrivateKey([]byte(privPEM))
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	// The authorized_keys line we hand Tendril must be the same key the
	// terminal bridge later authenticates with, or the box accepts a key we
	// cannot use.
	want := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if got := strings.TrimSpace(strings.TrimSuffix(pub, " agentmesh")); got != want {
		t.Errorf("public key mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestGenerateIsUnique(t *testing.T) {
	a, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if a == b {
		t.Error("two Generate calls produced the same public key")
	}
}
