// qm-admin inspects or resets a license's activations on the live database.
//
// Run it on the Quartermaster host, from the service's working directory and
// as the service user, so the default paths resolve:
//
//	cd /opt/quartermaster && sudo -u quartermaster /tmp/qm-admin show
//
// The license key is read from standard input so that it never appears in
// shell history or in the process list.
package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	_ "modernc.org/sqlite"

	"quartermaster/activations"
	"quartermaster/license"
)

func loadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s: wrong public key length", path)
	}
	return ed25519.PublicKey(b), nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func main() {
	dbPath := flag.String("db", "quartermaster.db", "path to the Quartermaster database")
	pubPath := flag.String("pub", "signing.pub", "path to the hex-encoded public key")
	flag.Parse()
	action := flag.Arg(0)
	if flag.NArg() != 1 || (action != "show" && action != "reset") {
		fmt.Fprintln(os.Stderr, "usage: qm-admin [-db path] [-pub path] show|reset   (license key on stdin)")
		os.Exit(2)
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		log.Fatal(err)
	}
	pub, err := loadPublicKey(*pubPath)
	if err != nil {
		log.Fatal(err)
	}
	l, err := license.VerifyAny([]ed25519.PublicKey{pub}, license.Normalize(string(raw)))
	if err != nil {
		log.Fatal("that is not a valid license key: ", err)
	}
	id := hex.EncodeToString(l.ID[:])

	// SQLite silently creates an empty database for a wrong path.
	if _, err := os.Stat(*dbPath); err != nil {
		log.Fatal("database not found (run from the service's working directory): ", err)
	}
	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		log.Fatal(err)
	}
	store, err := activations.Open(db)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("license %s  product %s  seats %d  issued %s\n",
		id, l.Product, l.Seats, l.IssuedAt.UTC().Format("2006-01-02"))

	if action == "reset" {
		n, err := store.Reset(id)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("removed %d activation(s)\n", n)
	}

	recs, err := store.List(id)
	if err != nil {
		log.Fatal(err)
	}
	if len(recs) == 0 {
		fmt.Println("no activations")
		return
	}
	for _, r := range recs {
		state := "active"
		if r.Revoked {
			state = "REVOKED"
		}
		fmt.Printf("  %s  fingerprint %s...  %s\n",
			r.ActivatedAt.UTC().Format("2006-01-02 15:04"), short(r.Fingerprint), state)
	}
}
