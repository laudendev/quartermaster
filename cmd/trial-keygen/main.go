// trial-keygen creates Quartermaster's online trial signing key in the current
// directory: trial.key (hex seed, mode 0600) and trial.pub (hex). It refuses to
// overwrite an existing key. Run it once on the Quartermaster host, in the
// service's working directory, as the service user. The printed Rust array is
// the public key to embed in apps that verify trial tokens.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.OpenFile("trial.key", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		log.Fatal("trial.key: ", err, " (an existing key is never overwritten)")
	}
	if _, err := fmt.Fprintln(f, hex.EncodeToString(priv.Seed())); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("trial.pub", []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("trial.pub:", hex.EncodeToString(pub))
	fmt.Println("Rust:")
	var parts []string
	for _, b := range pub {
		parts = append(parts, fmt.Sprintf("0x%02x", b))
	}
	fmt.Printf("[\n    %s,\n    %s,\n]\n", strings.Join(parts[:16], ", "), strings.Join(parts[16:], ", "))
}
