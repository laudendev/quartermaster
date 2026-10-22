package license

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

// Shared with the quartermaster-license crate (src/trial_v2.rs): the same key,
// signals and dates must give this exact token on both sides.
func v2Seed() []byte {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	return seed
}

func v2Signals() [][32]byte {
	var out [][32]byte
	for _, base := range []byte{0x10, 0x40, 0x70} {
		var s [32]byte
		for i := range s {
			s[i] = base + byte(i)
		}
		out = append(out, s)
	}
	return out
}

const (
	v2Start = int64(1_760_000_000)
	v2End   = v2Start + 14*86_400
)

func v2Trial() TrialV2 {
	return TrialV2{
		Product:   "POPF",
		Binding:   TrialBinding("POPF", v2Signals()),
		StartedAt: v2Start,
		EndsAt:    v2End,
	}
}

func v2Key() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(v2Seed()) }

func TestTrialV2RoundTrip(t *testing.T) {
	priv := v2Key()
	token, err := SignTrialV2(priv, v2Trial())
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyTrialV2(priv.Public().(ed25519.PublicKey), token)
	if err != nil {
		t.Fatal(err)
	}
	if got != v2Trial() {
		t.Fatalf("got %+v", got)
	}
}

func TestTrialBindingIgnoresOrderAndRepeats(t *testing.T) {
	s := v2Signals()
	want := TrialBinding("POPF", s)
	if TrialBinding("POPF", [][32]byte{s[2], s[0], s[1]}) != want {
		t.Fatal("order changed the binding")
	}
	if TrialBinding("POPF", [][32]byte{s[0], s[1], s[2], s[1], s[0]}) != want {
		t.Fatal("repeats changed the binding")
	}
	if TrialBinding("POPF", s[:2]) == want {
		t.Fatal("a different set gave the same binding")
	}
	if TrialBinding("LAMP", s) == want {
		t.Fatal("a different product gave the same binding")
	}
}

func TestTrialVersionsAreNotInterchangeable(t *testing.T) {
	priv := v2Key()
	pub := priv.Public().(ed25519.PublicKey)
	v2, err := SignTrialV2(priv, v2Trial())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTrial(pub, v2); err != ErrTrialVersion {
		t.Fatalf("v1 verifier on a v2 token: %v", err)
	}
	v1, err := SignTrial(priv, Trial{Product: "POPF", StartedAt: v2Start, EndsAt: v2End})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTrialV2(pub, v1); err != ErrTrialVersion {
		t.Fatalf("v2 verifier on a v1 token: %v", err)
	}
}

func TestTrialV2RejectsTamperingAndJunk(t *testing.T) {
	priv := v2Key()
	pub := priv.Public().(ed25519.PublicKey)
	token, _ := SignTrialV2(priv, v2Trial())
	raw, _ := trialEncoding.DecodeString(token)
	for i := range raw {
		bad := append([]byte(nil), raw...)
		bad[i] ^= 0xff
		if _, err := VerifyTrialV2(pub, trialEncoding.EncodeToString(bad)); err == nil {
			t.Fatalf("byte %d accepted", i)
		}
	}
	if _, err := VerifyTrialV2(pub, "AAAA"); err != ErrTrialMalformed {
		t.Fatal(err)
	}
	other := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	if _, err := VerifyTrialV2(other, token); err != ErrTrialSignature {
		t.Fatal(err)
	}
	if _, err := SignTrialV2(priv, TrialV2{Product: "POP", StartedAt: 1, EndsAt: 2}); err != ErrTrialProductCode {
		t.Fatal(err)
	}
	if _, err := SignTrialV2(priv, TrialV2{Product: "POPF", StartedAt: 5, EndsAt: 2}); err != ErrTrialMalformed {
		t.Fatal(err)
	}
}

// TestPrintTrialV2Vector prints the shared vector; run with -run Vector -v.
func TestTrialV2Vector(t *testing.T) {
	token, _ := SignTrialV2(v2Key(), v2Trial())
	b := v2Trial().Binding
	t.Logf("BINDING=%s", hex.EncodeToString(b[:]))
	t.Logf("TOKEN=%s", token)
	if want := vectorV2Token; want != "" && token != want {
		t.Fatalf("token changed:\n got %s\nwant %s", token, want)
	}
	if want := vectorV2Binding; want != "" && hex.EncodeToString(b[:]) != want {
		t.Fatalf("binding changed: %x", b)
	}
}
