// Package sshkeys generates the per-lease ed25519 keypair AgentMesh authorizes
// on a rented Tendril machine.
//
// A fresh key per lease rather than one platform key: the private key is
// downloadable by the lease's owner, so a shared key would hand every renter
// access to every machine AgentMesh has ever rented.
package sshkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"

	"golang.org/x/crypto/ssh"
)

func Generate() (publicKey string, privateKeyPEM string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	return line + " agentmesh", string(pem.EncodeToMemory(block)), nil
}
