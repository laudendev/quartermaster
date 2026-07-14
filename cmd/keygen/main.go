// keygen generates the Ed25519 signing pair for license issuance.
// signing.key is the crown jewels: offline storage only, never the VPS.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("signing.key", []byte(hex.EncodeToString(priv)), 0o600); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("signing.pub", []byte(hex.EncodeToString(pub)), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote signing.key (0600) and signing.pub")
}
