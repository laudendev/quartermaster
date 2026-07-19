// keygen generates the Ed25519 signing pair for license issuance.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
)

func generateAndWrite(privPath, pubPath string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(privPath, []byte(hex.EncodeToString(priv)), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)), 0o644); err != nil {
		return err
	}
	return nil
}

func main() {
	if err := generateAndWrite("signing.key", "signing.pub"); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote signing.key (0600) and signing.pub")
} // signing.key is the crown jewels: offline storage only, never the VPS.
