package license

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strings"
)

// Trial tokens v2 bind a free trial to a computer's set of hardware signals
// instead of one fingerprint. The layout is the same length as v1, with the
// version byte 2 and the binding in place of the fingerprint:
//
//	[0]      version (2)
//	[1:5]    product code, 4 ASCII bytes
//	[5:37]   binding: TrialBinding of the signal hashes the app submitted
//	[37:45]  started_at, Unix seconds, big-endian
//	[45:53]  ends_at, Unix seconds, big-endian
//
// followed by a 64-byte Ed25519 signature over TrialDomain + payload, as in v1.
// VerifyTrial still accepts only v1 and VerifyTrialV2 only v2, so neither
// version can be mistaken for the other.
const trialVersionV2 = 2

// TrialBindingDomain is hashed in front of a binding so it can never equal any
// other SHA-256 value in the system.
const TrialBindingDomain = "quartermaster-trial-binding\x00"

// TrialV2 is one computer's free trial of one product.
type TrialV2 struct {
	Product   string
	Binding   [32]byte
	StartedAt int64
	EndsAt    int64
}

// TrialBinding returns SHA-256(TrialBindingDomain || product || signals), the
// signals sorted bytewise and de-duplicated. Order and repeats do not matter.
func TrialBinding(product string, signals [][32]byte) [32]byte {
	sorted := make([][32]byte, len(signals))
	copy(sorted, signals)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i][:], sorted[j][:]) < 0 })
	h := sha256.New()
	h.Write([]byte(TrialBindingDomain))
	h.Write([]byte(product))
	for i := range sorted {
		if i > 0 && sorted[i] == sorted[i-1] {
			continue
		}
		h.Write(sorted[i][:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// SignTrialV2 returns the base32 token for t, signed with priv.
func SignTrialV2(priv ed25519.PrivateKey, t TrialV2) (string, error) {
	if len(t.Product) != 4 {
		return "", ErrTrialProductCode
	}
	for i := 0; i < 4; i++ {
		if t.Product[i] > 0x7f {
			return "", ErrTrialProductCode
		}
	}
	if t.EndsAt < t.StartedAt {
		return "", ErrTrialMalformed
	}
	payload := make([]byte, trialPayloadLen)
	payload[0] = trialVersionV2
	copy(payload[1:5], t.Product)
	copy(payload[5:37], t.Binding[:])
	binary.BigEndian.PutUint64(payload[37:45], uint64(t.StartedAt))
	binary.BigEndian.PutUint64(payload[45:53], uint64(t.EndsAt))
	sig := ed25519.Sign(priv, trialMessage(payload))
	return trialEncoding.EncodeToString(append(payload, sig...)), nil
}

// VerifyTrialV2 checks token against pub and returns the trial it records.
func VerifyTrialV2(pub ed25519.PublicKey, token string) (TrialV2, error) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, strings.ToUpper(token))
	raw, err := trialEncoding.DecodeString(clean)
	if err != nil || len(raw) != trialRawLen {
		return TrialV2{}, ErrTrialMalformed
	}
	payload, sig := raw[:trialPayloadLen], raw[trialPayloadLen:]
	if payload[0] != trialVersionV2 {
		return TrialV2{}, ErrTrialVersion
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, trialMessage(payload), sig) {
		return TrialV2{}, ErrTrialSignature
	}
	var t TrialV2
	t.Product = string(payload[1:5])
	copy(t.Binding[:], payload[5:37])
	t.StartedAt = int64(binary.BigEndian.Uint64(payload[37:45]))
	t.EndsAt = int64(binary.BigEndian.Uint64(payload[45:53]))
	if t.EndsAt < t.StartedAt {
		return TrialV2{}, ErrTrialMalformed
	}
	return t, nil
}
