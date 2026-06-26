package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

func EnsureKeypair(vabbeDir string) (privPath, pubPath string, err error) {
	privPath = filepath.Join(vabbeDir, "id_ed25519")
	pubPath = filepath.Join(vabbeDir, "id_ed25519.pub")
	if _, err := os.Stat(privPath); err == nil {
		if _, err := os.Stat(pubPath); err == nil {
			return privPath, pubPath, nil
		}
	}
	if err := os.MkdirAll(vabbeDir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir %s: %w", vabbeDir, err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate key: %w", err)
	}
	if err := writePrivateKey(privPath, priv); err != nil {
		return "", "", err
	}
	if err := writePublicKey(pubPath, pub); err != nil {
		return "", "", err
	}
	return privPath, pubPath, nil
}

func writePrivateKey(path string, key ed25519.PrivateKey) error {
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

func writePublicKey(path string, pub ed25519.PublicKey) error {
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	return os.WriteFile(path, ssh.MarshalAuthorizedKey(sshPub), 0o644)
}

func RegenerateKeypair(vabbeDir string) (privPath, pubPath string, err error) {
	if err := os.RemoveAll(vabbeDir); err != nil && !os.IsNotExist(err) {
		return "", "", fmt.Errorf("remove %s: %w", vabbeDir, err)
	}
	return EnsureKeypair(vabbeDir)
}
