package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
	"strings"
)

func TestRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	want := License{
		Product:  "TRCR",
		ID:       [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		MajorVer: 1,
		Seats:    3,
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	}

	key, err := Issue(priv, want)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Verify(pub, key)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestTamperDetection(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := Issue(priv, License{
		Product: "TRCR", MajorVer: 1, Seats: 1,
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < len(key); i++ {
		c := byte('A')
		if key[i] == 'A' {
			c = 'B'
		}
		tampered := key[:i] + string(c) + key[i+1:]
		if _, err := Verify(pub, tampered); err == nil {
			t.Fatalf("tampered key accepted at position %d", i)
		}
	}
	_ = strings.ToUpper // placeholder; remove when Format test lands
}


func TestFormatVerifies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := Issue(priv, License{Product: "TRCR", MajorVer: 1, Seats: 1,
		IssuedAt: time.Now().UTC().Truncate(time.Second)})
	if _, err := Verify(pub, Format(key)); err != nil {
		t.Fatalf("formatted key rejected: %v", err)
	}
}
