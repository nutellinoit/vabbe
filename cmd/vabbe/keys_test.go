package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestEnsureKeypairCreates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".vabbe", "lab1")
	priv, pub, err := EnsureKeypair(dir)
	if err != nil {
		t.Fatalf("EnsureKeypair: %v", err)
	}
	if _, err := os.Stat(priv); err != nil {
		t.Errorf("priv missing: %v", err)
	}
	if _, err := os.Stat(pub); err != nil {
		t.Errorf("pub missing: %v", err)
	}
}

func TestEnsureKeypairIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".vabbe", "lab2")
	p1, pub1, err := EnsureKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	p2, pub2, err := EnsureKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 || pub1 != pub2 {
		t.Errorf("paths changed across calls")
	}
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p1)
	if string(b1) != string(b2) {
		t.Errorf("private key changed across calls (should reuse)")
	}
}

func TestRegenerateKeypair(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".vabbe", "lab3")
	_, _, err := EnsureKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := os.ReadFile(filepath.Join(dir, "id_ed25519"))
	if _, _, err := RegenerateKeypair(dir); err != nil {
		t.Fatalf("RegenerateKeypair: %v", err)
	}
	b2, _ := os.ReadFile(filepath.Join(dir, "id_ed25519"))
	if string(b1) == string(b2) {
		t.Errorf("keypair not regenerated")
	}
}

func TestPublicKeyIsParseable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".vabbe", "lab4")
	_, pub, err := EnsureKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pub)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = ssh.ParseAuthorizedKey(data)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
}

func TestPrivateKeyIsEd25519(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".vabbe", "lab5")
	privPath, _, err := EnsureKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "OPENSSH PRIVATE KEY") {
		t.Fatalf("not an OpenSSH private key:\n%s", s)
	}

	if _, err := ssh.ParsePrivateKey(data); err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	_ = ed25519.PublicKey{}
}
