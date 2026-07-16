package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndWritePermissions(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "signing.key")
	pubPath := filepath.Join(dir, "signing.pub")

	if err := generateAndWrite(privPath, pubPath); err != nil {
		t.Fatal(err)
	}

	privInfo, err := os.Stat(privPath)
	if err != nil {
		t.Fatal(err)
	}
	if privInfo.Mode().Perm() != 0o600 {
		t.Fatalf("signing.key: expected 0600, got %o", privInfo.Mode().Perm())
	}

	pubInfo, err := os.Stat(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	if pubInfo.Mode().Perm() != 0o644 {
		t.Fatalf("signing.pub: expected 0644, got %o", pubInfo.Mode().Perm())
	}
}

func TestGenerateAndWriteValidHexKeys(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "signing.key")
	pubPath := filepath.Join(dir, "signing.pub")

	if err := generateAndWrite(privPath, pubPath); err != nil {
		t.Fatal(err)
	}

	privHex, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatal(err)
	}
	privBytes, err := hex.DecodeString(string(privHex))
	if err != nil {
		t.Fatalf("signing.key is not valid hex: %v", err)
	}
	if len(privBytes) != 64 {
		t.Fatalf("expected 64-byte private key, got %d bytes", len(privBytes))
	}

	pubHex, err := os.ReadFile(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := hex.DecodeString(string(pubHex))
	if err != nil {
		t.Fatalf("signing.pub is not valid hex: %v", err)
	}
	if len(pubBytes) != 32 {
		t.Fatalf("expected 32-byte public key, got %d bytes", len(pubBytes))
	}
}
