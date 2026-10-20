// Package trials records free trials server-side: one per product per
// computer fingerprint, so deleting the app's trial file or reinstalling the
// app cannot start a new trial.
package trials

import (
	"database/sql"
	"errors"
)

// Record is one computer's trial of one product.
type Record struct {
	Product     string
	Fingerprint string
	StartedAt   int64
	EndsAt      int64
	CreatedAt   int64
}

// Store keeps trial records in the Quartermaster database.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS trials (
    product     TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    started_at  INTEGER NOT NULL,
    ends_at     INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (product, fingerprint)
);
`

// Open creates the trials table if needed.
func Open(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Start records a trial unless this computer already has one for the product.
// It returns the stored record either way, and whether it already existed. A
// computer's trial never moves: repeat calls get the original dates back.
func (s *Store) Start(product, fingerprint string, startedAt, endsAt, now int64) (Record, bool, error) {
	res, err := s.db.Exec(
		`INSERT INTO trials (product, fingerprint, started_at, ends_at, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(product, fingerprint) DO NOTHING`,
		product, fingerprint, startedAt, endsAt, now)
	if err != nil {
		return Record{}, false, err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return Record{}, false, err
	}
	rec, found, err := s.Get(product, fingerprint)
	if err != nil {
		return Record{}, false, err
	}
	if !found {
		return Record{}, false, errors.New("trial vanished after insert")
	}
	return rec, inserted == 0, nil
}

// Get returns the trial for a product and fingerprint, if there is one.
func (s *Store) Get(product, fingerprint string) (Record, bool, error) {
	rec := Record{Product: product, Fingerprint: fingerprint}
	err := s.db.QueryRow(
		`SELECT started_at, ends_at, created_at FROM trials WHERE product = ? AND fingerprint = ?`,
		product, fingerprint).Scan(&rec.StartedAt, &rec.EndsAt, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	return rec, true, nil
}

// Reset deletes a computer's trial so it can start a new one (support only).
func (s *Store) Reset(product, fingerprint string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM trials WHERE product = ? AND fingerprint = ?`, product, fingerprint)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
