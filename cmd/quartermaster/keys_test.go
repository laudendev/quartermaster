package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writePub(t *testing.T, dir, name string) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(hex.EncodeToString(pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestLoadPublicKeysPrimaryOnly(t *testing.T) {
	dir := t.TempDir()
	p1 := writePub(t, dir, "signing.pub")
	got, err := loadPublicKeys(filepath.Join(dir, "signing.pub"), filepath.Join(dir, "signing2.pub"))
	if err != nil || len(got) != 1 || !got[0].Equal(p1) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestLoadPublicKeysBoth(t *testing.T) {
	dir := t.TempDir()
	p1 := writePub(t, dir, "signing.pub")
	p2 := writePub(t, dir, "signing2.pub")
	got, err := loadPublicKeys(filepath.Join(dir, "signing.pub"), filepath.Join(dir, "signing2.pub"))
	if err != nil || len(got) != 2 || !got[0].Equal(p1) || !got[1].Equal(p2) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestLoadPublicKeysMalformedExtraIsError(t *testing.T) {
	dir := t.TempDir()
	writePub(t, dir, "signing.pub")
	if err := os.WriteFile(filepath.Join(dir, "signing2.pub"), []byte("not hex"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPublicKeys(filepath.Join(dir, "signing.pub"), filepath.Join(dir, "signing2.pub")); err == nil {
		t.Fatal("expected an error for a malformed extra key")
	}
}

func TestLoadPublicKeysMissingPrimaryIsError(t *testing.T) {
	if _, err := loadPublicKeys(filepath.Join(t.TempDir(), "signing.pub")); err == nil {
		t.Fatal("expected an error for a missing primary key")
	}
}
