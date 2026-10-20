package license

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"
)

// Fixed key and trial shared with the Rust crate's tests, so both sides prove
// they agree on the token format byte for byte.
var (
	vectorSeed, _ = hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	vectorTrial   = Trial{
		Product:     "POPF",
		Fingerprint: [32]byte{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf, 0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf},
		StartedAt:   1_760_000_000,
		EndsAt:      1_760_000_000 + 14*86_400,
	}
)

const vectorToken = "AFIE6UCGUCQ2FI5EUWTKPKFJVKV2ZLNOV6YLDMVTWS23NN5YXG5LXPF5X27QAAAAABUOO6AAAAAAAADI7HWQBHOPQMDONAAQ7IDEXGHM44DJEO2BVH47CWI4QL4KR5T5R6PMQ2IEPJKZ7EOVFLLWEK526GNVXODOU4TEEXAD4ACCD4REHXCPA3GWEUDQ"

func vectorKey() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(vectorSeed) }

func TestTrialRoundTrip(t *testing.T) {
	priv := vectorKey()
	tok, err := SignTrial(priv, vectorTrial)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyTrial(priv.Public().(ed25519.PublicKey), tok)
	if err != nil {
		t.Fatal(err)
	}
	if got != vectorTrial {
		t.Fatalf("got %+v, want %+v", got, vectorTrial)
	}
}

func TestTrialMatchesSharedVector(t *testing.T) {
	tok, err := SignTrial(vectorKey(), vectorTrial)
	if err != nil {
		t.Fatal(err)
	}
	if tok != vectorToken {
		t.Fatalf("token changed; the Rust crate's vector must change too:\n%s", tok)
	}
}

func TestTrialTamperDetected(t *testing.T) {
	priv := vectorKey()
	pub := priv.Public().(ed25519.PublicKey)
	tok, _ := SignTrial(priv, vectorTrial)
	raw, _ := trialEncoding.DecodeString(tok)
	for i := range raw {
		bad := append([]byte(nil), raw...)
		bad[i] ^= 0xff
		if _, err := VerifyTrial(pub, trialEncoding.EncodeToString(bad)); err == nil {
			t.Fatalf("tampered byte %d accepted", i)
		}
	}
}

func TestTrialSignatureNeedsTheDomain(t *testing.T) {
	priv := vectorKey()
	tok, _ := SignTrial(priv, vectorTrial)
	raw, _ := trialEncoding.DecodeString(tok)
	payload := raw[:trialPayloadLen]
	// A signature over the bare payload, as a license key is signed, fails.
	plain := append(append([]byte(nil), payload...), ed25519.Sign(priv, payload)...)
	if _, err := VerifyTrial(priv.Public().(ed25519.PublicKey), trialEncoding.EncodeToString(plain)); !errors.Is(err, ErrTrialSignature) {
		t.Fatalf("got %v, want ErrTrialSignature", err)
	}
}

func TestTrialWrongKeyAndJunk(t *testing.T) {
	tok, _ := SignTrial(vectorKey(), vectorTrial)
	other := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	if _, err := VerifyTrial(other, tok); !errors.Is(err, ErrTrialSignature) {
		t.Fatalf("wrong key: got %v", err)
	}
	for _, junk := range []string{"", "AAAA", "not base32!"} {
		if _, err := VerifyTrial(other, junk); !errors.Is(err, ErrTrialMalformed) {
			t.Fatalf("%q: got %v", junk, err)
		}
	}
}

func TestSignTrialRejectsBadInput(t *testing.T) {
	bad := vectorTrial
	bad.Product = "POPFX"
	if _, err := SignTrial(vectorKey(), bad); err == nil {
		t.Fatal("5-byte product accepted")
	}
	bad = vectorTrial
	bad.EndsAt = bad.StartedAt - 1
	if _, err := SignTrial(vectorKey(), bad); err == nil {
		t.Fatal("end before start accepted")
	}
}
