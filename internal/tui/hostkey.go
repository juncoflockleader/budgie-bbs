package tui

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// GenerateHostKey creates an Ed25519 SSH host key at the given path if it
// doesn't already exist.
func GenerateHostKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return err
	}
	_ = signer // validate the key is usable

	marshaled, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return err
	}

	return os.WriteFile(path, pem.EncodeToMemory(marshaled), 0600)
}
