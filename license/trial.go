package license

import (
	"crypto/ed25519"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"strings"
)

// Trial tokens record a free trial for one computer. Quartermaster signs them
// with its online trial key (never the license signing key, which stays on
// the offline signer). The app verifies them offline, like license keys.
//
// Layout (v1), then a 64-byte Ed25519 signature over TrialDomain + payload:
//
//	[0]      version (1)
//	[1:5]    product code, 4 ASCII bytes
//	[5:37]   SHA-256 computer fingerprint (raw bytes)
//	[37:45]  started_at, Unix seconds, big-endian
//	[45:53]  ends_at, Unix seconds, big-endian
const (
	trialPayloadLen = 53
	trialRawLen     = trialPayloadLen + ed25519.SignatureSize
	trialVersion    = 1
)

// TrialDomain is signed in front of every trial payload, so a trial signature
// can never stand for any other Quartermaster signature.
const TrialDomain = "quartermaster-trial\x00"

// Trial is one computer's free trial of one product.
type Trial struct {
	Product     string
	Fingerprint [32]byte
	StartedAt   int64
	EndsAt      int64
}

var (
	ErrTrialMalformed   = errors.New("malformed trial token")
	ErrTrialVersion     = errors.New("unsupported trial token version")
	ErrTrialSignature   = errors.New("invalid trial token signature")
	ErrTrialProductCode = errors.New("product code must be 4 ASCII bytes")
)

var trialEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func trialMessage(payload []byte) []byte {
	msg := make([]byte, 0, len(TrialDomain)+len(payload))
	msg = append(msg, TrialDomain...)
	return append(msg, payload...)
}

// SignTrial returns the base32 token for t, signed with priv.
func SignTrial(priv ed25519.PrivateKey, t Trial) (string, error) {
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
	payload[0] = trialVersion
	copy(payload[1:5], t.Product)
	copy(payload[5:37], t.Fingerprint[:])
	binary.BigEndian.PutUint64(payload[37:45], uint64(t.StartedAt))
	binary.BigEndian.PutUint64(payload[45:53], uint64(t.EndsAt))
	sig := ed25519.Sign(priv, trialMessage(payload))
	return trialEncoding.EncodeToString(append(payload, sig...)), nil
}

// VerifyTrial checks token against pub and returns the trial it records.
func VerifyTrial(pub ed25519.PublicKey, token string) (Trial, error) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, strings.ToUpper(token))
	raw, err := trialEncoding.DecodeString(clean)
	if err != nil || len(raw) != trialRawLen {
		return Trial{}, ErrTrialMalformed
	}
	payload, sig := raw[:trialPayloadLen], raw[trialPayloadLen:]
	if payload[0] != trialVersion {
		return Trial{}, ErrTrialVersion
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, trialMessage(payload), sig) {
		return Trial{}, ErrTrialSignature
	}
	var t Trial
	t.Product = string(payload[1:5])
	copy(t.Fingerprint[:], payload[5:37])
	t.StartedAt = int64(binary.BigEndian.Uint64(payload[37:45]))
	t.EndsAt = int64(binary.BigEndian.Uint64(payload[45:53]))
	if t.EndsAt < t.StartedAt {
		return Trial{}, ErrTrialMalformed
	}
	return t, nil
}
