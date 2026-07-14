package license

import (
  "encoding/base32"
  "crypto/ed25519"
  "time"
  "errors"
  "encoding/binary"
  "strings"
  "crypto/rand"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

const (
    payloadLen = 32
    sigLen     = ed25519.SignatureSize // 64
    rawLen     = payloadLen + sigLen   // 96
)

// Layout (big-endian):
//
//	[0:4)   product code, zero-padded ASCII
//	[4:20)  license id
//	[20:22) major version ceiling
//	[22:24) seats
//	[24:32) issued-at, unix seconds
type License struct {
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
)

func (l License) payload() ([]byte, error) {
	if len(l.Product) == 0 || len(l.Product) > 4 {
		return nil, ErrProduct
	}
	buf := make([]byte, payloadLen)
	copy(buf[0:4], l.Product)
	copy(buf[4:20], l.ID[:])
	binary.BigEndian.PutUint16(buf[20:22], l.MajorVer)
	binary.BigEndian.PutUint16(buf[22:24], l.Seats)
	binary.BigEndian.PutUint64(buf[24:32], uint64(l.IssuedAt.Unix()))
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
	if !ed25519.Verify(pub, p, sig) {
	   return License{}, ErrSignature
        }
	var l License
	l.Product = strings.TrimRight(string(p[0:4]), "\x00")
	copy(l.ID[:], p[4:20])
	l.MajorVer = binary.BigEndian.Uint16(p[20:22])
	l.Seats = binary.BigEndian.Uint16(p[22:24])
	l.IssuedAt = time.Unix(int64(binary.BigEndian.Uint64(p[24:32])), 0).UTC()
	return l, nil
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
