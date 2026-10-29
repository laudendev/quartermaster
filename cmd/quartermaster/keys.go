package main

import (
	"crypto/ed25519"
	"errors"
	"io/fs"
	"os"
)

// loadPublicKeys loads the required primary license public key plus any
// extra trusted keys whose files exist. A missing extra file is skipped; a
// present but malformed one is an error, so a typo cannot silently drop trust.
func loadPublicKeys(primary string, extras ...string) ([]ed25519.PublicKey, error) {
	pub, err := loadPublicKey(primary)
	if err != nil {
		return nil, err
	}
	pubs := []ed25519.PublicKey{pub}
	for _, p := range extras {
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		extra, err := loadPublicKey(p)
		if err != nil {
			return nil, err
		}
		pubs = append(pubs, extra)
	}
	return pubs, nil
}
