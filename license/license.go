package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

const (
	currentVersion = 1
	payloadLen     = 33                    // was 32
	sigLen         = ed25519.SignatureSize // 64
	rawLen         = payloadLen + sigLen   // 97
)

// Layout (big-endian):
//
//	[0]     version, must equal currentVersion or Verify refuses
//	[1:17)  license id
//	[17:21) product code, zero-padded ASCII
//	[21:23) major version ceiling
//	[23:25) seats (0 is coerced to 1 at Issue time; never stored as 0)
//	[25:33) issued-at, unix seconds
type License struct {
	Version  uint8
	Product  string
	ID       [16]byte
	MajorVer uint16
	Seats    uint16
	IssuedAt time.Time
}

var (
	ErrMalformed = errors.New("license: malformed key")
	ErrSignature = errors.New("license: invalid signature")
	ErrProduct   = errors.New("license: product code must be 1-4 ASCII bytes")
	ErrVersion   = errors.New("license: unsupported payload version")
)

func (l License) payload() ([]byte, error) {
	if len(l.Product) != 4 {
		return nil, ErrProduct
	}
	buf := make([]byte, payloadLen)
	buf[0] = currentVersion
	copy(buf[17:21], l.Product)
	copy(buf[1:17], l.ID[:])
	binary.BigEndian.PutUint16(buf[21:23], l.MajorVer)
	seats := l.Seats
	if seats == 0 {
		seats = 1
	}
	binary.BigEndian.PutUint16(buf[23:25], seats)
	binary.BigEndian.PutUint64(buf[25:33], uint64(l.IssuedAt.Unix()))
	return buf, nil
}

func Issue(priv ed25519.PrivateKey, l License) (string, error) {
	p, err := l.payload()
	if err != nil {
		return "", err
	}
	raw := make([]byte, 0, rawLen)
	raw = append(raw, p...)
	raw = append(raw, ed25519.Sign(priv, p)...)
	return b32.EncodeToString(raw), nil
}

func Normalize(s string) string {
	s = strings.ToUpper(s)
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, s)
}

func Verify(pub ed25519.PublicKey, key string) (License, error) {
	raw, err := b32.DecodeString(Normalize(key))
	if err != nil || len(raw) != rawLen {
		return License{}, ErrMalformed
	}
	p, sig := raw[:payloadLen], raw[payloadLen:]
	if p[0] != currentVersion {
		return License{}, ErrVersion
	}
	if !ed25519.Verify(pub, p, sig) {
		return License{}, ErrSignature
	}
	var l License
	l.Version = p[0]
	copy(l.ID[:], p[1:17])
	l.Product = string(p[17:21])
	l.MajorVer = binary.BigEndian.Uint16(p[21:23])
	l.Seats = binary.BigEndian.Uint16(p[23:25])
	l.IssuedAt = time.Unix(int64(binary.BigEndian.Uint64(p[25:33])), 0).UTC()
	return l, nil
}

// VerifyAny tries each candidate public key in order and returns the
// result of the first one that successfully verifies key. This exists
// to support signing-key rotation: a license issued under an old key
// must keep verifying forever, so callers should pass every public
// key that has ever been live, oldest first or newest first — order
// doesn't affect correctness, only which key gets tried first on the
// common case.
//
// Returns ErrSignature if every candidate key fails to verify, and
// returns immediately (without trying remaining keys) on ErrMalformed
// or ErrVersion, since those failures don't depend on which key was
// used — retrying with a different key can't fix a malformed payload
// or an unrecognized version.
func VerifyAny(pubs []ed25519.PublicKey, key string) (License, error) {
	if len(pubs) == 0 {
		return License{}, ErrMalformed
	}
	var lastErr error
	for _, pub := range pubs {
		l, err := Verify(pub, key)
		if err == nil {
			return l, nil
		}
		if err == ErrMalformed || err == ErrVersion {
			return License{}, err
		}
		lastErr = err
	}
	return License{}, lastErr
}

// NewID returns a random 16-byte license id.
func NewID() ([16]byte, error) {
	var id [16]byte
	_, err := rand.Read(id[:])
	return id, err
}

// Format inserts a dash every 5 characters for display.
func Format(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/5)
	for i := 0; i < len(s); i++ {
		if i > 0 && i%5 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
