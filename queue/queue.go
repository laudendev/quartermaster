package queue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS sign_requests (
    id           TEXT PRIMARY KEY,
    txn_id   TEXT UNIQUE NOT NULL,
    product      TEXT NOT NULL,
    email        TEXT NOT NULL,
    seats        INTEGER NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    license_key  TEXT,
    reject_note  TEXT,
    created_at   INTEGER NOT NULL,
    signed_at    INTEGER,
	email_sent   INTEGER NOT NULL DEFAULT 0,
	email_attempts INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_status ON sign_requests(status);
CREATE INDEX IF NOT EXISTS idx_email_pending ON sign_requests(status, email_sent);
`

func Open(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Store) Enqueue(txnID, product, email string, seats int) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO sign_requests (id, txn_id, product, email, seats, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, txnID, product, email, seats, time.Now().Unix(),
	)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return nil
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
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) Complete(id, licenseKey string) (string, string, error) {
	res, err := s.db.Exec(
		`UPDATE sign_requests SET status = 'signed', license_key = ?, signed_at = ?
		 WHERE id = ? AND status = 'pending'`,
		licenseKey, time.Now().Unix(), id)
	if err != nil {
		return "", "", err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return "", "", err
	}

	var email, txnID string
	row := s.db.QueryRow(`SELECT email, txn_id FROM sign_requests WHERE id = ?`, id)
	if err := row.Scan(&email, &txnID); err != nil {
		return "", "", err
	}
	return email, txnID, nil
}

// UnsentEmail represents a signed request whose license email hasn't
// been successfully delivered yet.
type UnsentEmail struct {
	ID          string
	TxnID       string
	Email       string
	LicenseKey  string
	Attempts    int
}

// PendingEmails returns signed requests that still need their license
// email sent (or resent, after a prior failed attempt).
func (s *Store) PendingEmails(maxAttempts int) ([]UnsentEmail, error) {
	rows, err := s.db.Query(
		`SELECT id, txn_id, email, license_key, email_attempts
		 FROM sign_requests
		 WHERE status = 'signed' AND email_sent = 0 AND email_attempts < ?
		 ORDER BY signed_at`,
		maxAttempts,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UnsentEmail
	for rows.Next() {
		var u UnsentEmail
		if err := rows.Scan(&u.ID, &u.TxnID, &u.Email, &u.LicenseKey, &u.Attempts); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MarkEmailSent records that the license email for this request was
// successfully delivered.
func (s *Store) MarkEmailSent(id string) error {
	_, err := s.db.Exec(`UPDATE sign_requests SET email_sent = 1 WHERE id = ?`, id)
	return err
}

// RecordEmailAttempt increments the retry counter after a failed send,
// without marking the email as sent.
func (s *Store) RecordEmailAttempt(id string) error {
	_, err := s.db.Exec(`UPDATE sign_requests SET email_attempts = email_attempts + 1 WHERE id = ?`, id)
	return err
}

func (s *Store) Reject(id, note string) error {
	_, err := s.db.Exec(
		`UPDATE sign_requests SET status = 'rejected', reject_note = ?
		 WHERE id = ? AND status = 'pending'`,
		note, id)
	return err
}

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
