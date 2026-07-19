package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	input := License{
		Product:  "TRCR",
		ID:       [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		MajorVer: 1,
		Seats:    3,
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	}

	key, err := Issue(priv, input)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Verify(pub, key)
	if err != nil {
		t.Fatal(err)
	}

	// want is the input plus what Issue is expected to stamp on the wire —
	// Version is Issue's output, never the caller's input, so it's added
	// here rather than set on `input` above.
	want := input
	want.Version = currentVersion

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

	raw, err := b32.DecodeString(Normalize(key))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < len(raw); i++ {
		tampered := make([]byte, len(raw))
		copy(tampered, raw)
		tampered[i] ^= 0xFF // flip every bit in this byte
		tamperedKey := b32.EncodeToString(tampered)
		if _, err := Verify(pub, tamperedKey); err == nil {
			t.Fatalf("tampered raw byte at position %d accepted", i)
		}
	}
}

func TestFormatVerifies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := Issue(priv, License{Product: "TRCR", MajorVer: 1, Seats: 1,
		IssuedAt: time.Now().UTC().Truncate(time.Second)})
	if _, err := Verify(pub, Format(key)); err != nil {
		t.Fatalf("formatted key rejected: %v", err)
	}
}

func TestZeroSeatsCoercedToOne(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key, err := Issue(priv, License{
		Product: "TRCR", MajorVer: 1, Seats: 0, // explicit zero
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(pub, key)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seats != 1 {
		t.Fatalf("expected zero seats coerced to 1, got %d", got.Seats)
	}
}

func TestUnknownVersionRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key, err := Issue(priv, License{
		Product: "TRCR", MajorVer: 1, Seats: 1,
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := b32.DecodeString(Normalize(key))
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the version byte, then re-sign so this is a version failure,
	// not a signature failure — otherwise the test can't tell which check fired.
	tampered := make([]byte, len(raw))
	copy(tampered, raw)
	tampered[0] = 2
	sig := ed25519.Sign(priv, tampered[:payloadLen])
	copy(tampered[payloadLen:], sig)

	tamperedKey := b32.EncodeToString(tampered)
	if _, err := Verify(pub, tamperedKey); err != ErrVersion {
		t.Fatalf("expected ErrVersion for unknown version byte, got %v", err)
	}
}

func TestProductCodeMustBeExactlyFour(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)

	cases := []struct {
		name    string
		product string
	}{
		{"empty", ""},
		{"too short", "AB"},
		{"too long", "TOOLONG"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Issue(priv, License{
				Product: tc.product, MajorVer: 1, Seats: 1,
				IssuedAt: time.Now().UTC().Truncate(time.Second),
			})
			if err != ErrProduct {
				t.Fatalf("product %q: expected ErrProduct, got %v", tc.product, err)
			}
		})
	}
}

func TestVerifyAnyTriesEachKey(t *testing.T) {
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)

	// Issued under the "old" key (priv1).
	key, err := Issue(priv1, License{
		Product: "BOOK", MajorVer: 1, Seats: 1,
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Client embeds both keys, new (pub2) first — mirrors a real
	// rotation where the newest key is checked first.
	l, err := VerifyAny([]ed25519.PublicKey{pub2, pub1}, key)
	if err != nil {
		t.Fatalf("expected verification against the old key to succeed, got %v", err)
	}
	if l.Product != "BOOK" {
		t.Fatalf("unexpected license: %+v", l)
	}
}
func TestVerifyAnyFailsWhenNoKeyMatches(t *testing.T) {
	_, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)

	key, err := Issue(priv1, License{
		Product: "BOOK", MajorVer: 1, Seats: 1,
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = VerifyAny([]ed25519.PublicKey{pub2}, key)
	if err != ErrSignature {
		t.Fatalf("expected ErrSignature when no key matches, got %v", err)
	}
}

func TestVerifyAnyShortCircuitsOnMalformed(t *testing.T) {
	pub1, _, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)

	_, err := VerifyAny([]ed25519.PublicKey{pub1, pub2}, "not-a-real-license-key")
	if err != ErrMalformed {
		t.Fatalf("expected ErrMalformed, got %v", err)
	}
}

func TestVerifyAnyEmptyKeyListIsMalformed(t *testing.T) {
	_, err := VerifyAny(nil, "anything")
	if err != ErrMalformed {
		t.Fatalf("expected ErrMalformed for empty key list, got %v", err)
	}
}
