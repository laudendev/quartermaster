// Package store owns the SQLite queue: schema and every query
// both daemons run against it.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	_ "modernc.org/sqlite"
	"strings"

	"time"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS sign_requests (
    id           TEXT PRIMARY KEY,
    paddle_txn   TEXT UNIQUE NOT NULL,
    product      TEXT NOT NULL,
    email        TEXT NOT NULL,
    seats        INTEGER NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    license_key  TEXT,
    reject_note  TEXT,
    created_at   INTEGER NOT NULL,
    signed_at    INTEGER
);
CREATE INDEX IF NOT EXISTS idx_status ON sign_requests(status);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Enqueue records a sign request. Safe to call repeatedly with the
// same paddle_txn: duplicates are absorbed (webhook retries).
func (s *Store) Enqueue(paddleTxn, product, email string, seats int) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO sign_requests (id, paddle_txn, product, email, seats, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, paddleTxn, product, email, seats, time.Now().Unix(),
	)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return nil // retry absorbed; row already exists
	}
	return err
}

type SignRequest struct {
	ID      string `json:"id"`
	Product string `json:"product"`
	Email   string `json:"email"`
	Seats   int    `json:"seats"`
}

func (s *Store) NextPending() (*SignRequest, error) {
	row := s.db.QueryRow(
		`SELECT id, product, email, seats FROM sign_requests
		 WHERE status = 'pending' ORDER BY created_at LIMIT 1`)
	var r SignRequest
	if err := row.Scan(&r.ID, &r.Product, &r.Email, &r.Seats); err == sql.ErrNoRows {
		return nil, nil // empty queue is not an error
	} else if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) Complete(id, licenseKey string) error {
	_, err := s.db.Exec(
		`UPDATE sign_requests SET status = 'signed', license_key = ?, signed_at = ?
		 WHERE id = ? AND status = 'pending'`,
		licenseKey, time.Now().Unix(), id)
	return err
}

func (s *Store) Reject(id, note string) error {
	_, err := s.db.Exec(
		`UPDATE sign_requests SET status = 'rejected', reject_note = ?
		 WHERE id = ? AND status = 'pending'`,
		note, id)
	return err
}

// WaitPending polls for pending work until timeout or ctx cancellation.
// Returns (nil, nil) on timeout with an empty queue.
func (s *Store) WaitPending(ctx context.Context, timeout time.Duration) (*SignRequest, error) {
	deadline := time.Now().Add(timeout)
	for {
		r, err := s.NextPending()
		if r != nil || err != nil {
			return r, err
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
