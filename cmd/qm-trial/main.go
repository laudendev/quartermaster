// qm-trial inspects or resets free-trial records on the live database.
//
// Run it on the Quartermaster host, from the service's working directory and
// as the service user, so the default database path resolves:
//
//	cd /opt/quartermaster && sudo -u quartermaster /tmp/qm-trial show <fingerprint>
//
// A fingerprint is the hash Pop Focus sends (not a secret). On Linux:
//
//	printf '%s%s' "$(cat /etc/machine-id)" POPF | sha256sum
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"quartermaster/trials"
)

type dbStore struct {
	*trials.Store
	db *sql.DB
}

func (d dbStore) List(product string, limit int) ([]trials.Record, error) {
	rows, err := d.db.Query(
		`SELECT product, fingerprint, started_at, ends_at, created_at FROM trials
		 WHERE product = ? ORDER BY created_at DESC LIMIT ?`, product, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []trials.Record
	for rows.Next() {
		var r trials.Record
		if err := rows.Scan(&r.Product, &r.Fingerprint, &r.StartedAt, &r.EndsAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func main() {
	dbPath := flag.String("db", "quartermaster.db", "path to the Quartermaster database")
	product := flag.String("product", "POPF", "product code")
	flag.Parse()
	// SQLite silently creates an empty database for a wrong path.
	if _, err := os.Stat(*dbPath); err != nil {
		log.Fatal("database not found (run from the service's working directory): ", err)
	}
	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	st, err := trials.Open(db)
	if err != nil {
		log.Fatal(err)
	}
	st2, err := trials.OpenV2(db)
	if err != nil {
		log.Fatal(err)
	}
	var runErr error
	if isV2Command(flag.Args()) {
		runErr = runV2(st2, *product, flag.Args(), time.Now(), os.Stdout)
	} else {
		runErr = run(dbStore{st, db}, *product, flag.Args(), time.Now(), os.Stdout)
	}
	if err := runErr; err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
