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
	session_id   TEXT NOT NULL DEFAULT '',
	price_id     TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS session_emails (
	session_id    TEXT PRIMARY KEY,
	email_sent    INTEGER NOT NULL DEFAULT 0,
	email_attempts INTEGER NOT NULL DEFAULT 0
);
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

// sessionIDFromTxnID derives the parent checkout session ID from a
// per-unit txn_id of the form "{session_id}#{index}". Falls back to the
// full txnID unchanged if no "#" is present (e.g. legacy single-item
// txn_ids that were the raw session ID).
func SessionIDFromTxnID(txnID string) string {
	if i := strings.LastIndex(txnID, "#"); i != -1 {
		return txnID[:i]
	}
	return txnID
}

func (s *Store) Enqueue(txnID, priceID, product, email string, seats int) error {
	id, err := newID()
	if err != nil {
		return err
	}
	sessionID := SessionIDFromTxnID(txnID)
	_, err = s.db.Exec(
		`INSERT INTO sign_requests (id, txn_id, session_id, price_id, product, email, seats, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, txnID, sessionID, priceID, product, email, seats, time.Now().Unix(),
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

// SessionItem is one signed license within a checkout session, for
// building a combined receipt email.
type SessionItem struct {
	Product    string
	PriceID    string
	LicenseKey string
}

// ReadySession represents a checkout session whose every line item has
// finished signing and whose receipt email hasn't been sent yet.
type ReadySession struct {
	SessionID string
	Email     string
	Items     []SessionItem
	Attempts  int
}

func (s *Store) ReadySessions(maxAttempts int) ([]ReadySession, error) {
	rows, err := s.db.Query(
		`SELECT sr.session_id, sr.email, sr.product, sr.price_id, sr.license_key,
		        COALESCE(se.email_attempts, 0)
		 FROM sign_requests sr
		 LEFT JOIN session_emails se ON se.session_id = sr.session_id
		 WHERE sr.session_id IN (
		     SELECT session_id FROM sign_requests
		     GROUP BY session_id
		     HAVING COUNT(*) = SUM(CASE WHEN status = 'signed' THEN 1 ELSE 0 END)
		 )
		 AND (se.email_sent IS NULL OR se.email_sent = 0)
		 AND COALESCE(se.email_attempts, 0) < ?
		 ORDER BY sr.session_id, sr.created_at`,
		maxAttempts,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bySession := make(map[string]*ReadySession)
	var order []string
	for rows.Next() {
		var sessionID, email, product, priceID, licenseKey string
		var attempts int
		if err := rows.Scan(&sessionID, &email, &product, &priceID, &licenseKey, &attempts); err != nil {
			return nil, err
		}
		rs, ok := bySession[sessionID]
		if !ok {
			rs = &ReadySession{SessionID: sessionID, Email: email, Attempts: attempts}
			bySession[sessionID] = rs
			order = append(order, sessionID)
		}
		rs.Items = append(rs.Items, SessionItem{Product: product, PriceID: priceID, LicenseKey: licenseKey})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ReadySession, 0, len(order))
	for _, id := range order {
		out = append(out, *bySession[id])
	}
	return out, nil
}

// MarkSessionEmailSent records that the combined receipt email for this
// checkout session was successfully delivered.
func (s *Store) MarkSessionEmailSent(sessionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO session_emails (session_id, email_sent)
		 VALUES (?, 1)
		 ON CONFLICT(session_id) DO UPDATE SET email_sent = 1`,
		sessionID,
	)
	return err
}

// RecordSessionEmailAttempt increments the retry counter after a failed
// send, without marking the email as sent.
func (s *Store) RecordSessionEmailAttempt(sessionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO session_emails (session_id, email_attempts)
		 VALUES (?, 1)
		 ON CONFLICT(session_id) DO UPDATE SET email_attempts = email_attempts + 1`,
		sessionID,
	)
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

// SessionStatus describes the current state of a checkout session's
// fulfillment, for polling from the storefront's thank-you page.
type SessionStatus struct {
	Found bool          // false if no sign_requests exist for this session at all
	Ready bool          // true once every item has been signed
	Items []SessionItem // populated only once Ready is true
}

// GetSessionStatus reports whether a checkout session's fulfillment is
// complete, and its items if so. Unlike ReadySessions (a bulk sweep for
// the email retry loop), this looks up exactly one session on demand.
func (s *Store) GetSessionStatus(sessionID string) (SessionStatus, error) {
	rows, err := s.db.Query(
		`SELECT status, product, price_id, license_key FROM sign_requests WHERE session_id = ? ORDER BY created_at`,
		sessionID,
	)
	if err != nil {
		return SessionStatus{}, err
	}
	defer rows.Close()

	var out SessionStatus
	allSigned := true
	for rows.Next() {
		var status, product, priceID string
		var licenseKey sql.NullString
		if err := rows.Scan(&status, &product, &priceID, &licenseKey); err != nil {
			return SessionStatus{}, err
		}
		out.Found = true
		if status != "signed" {
			allSigned = false
			continue
		}
		out.Items = append(out.Items, SessionItem{Product: product, PriceID: priceID, LicenseKey: licenseKey.String})
	}
	if err := rows.Err(); err != nil {
		return SessionStatus{}, err
	}

	out.Ready = out.Found && allSigned
	if !out.Ready {
		out.Items = nil
	}
	return out, nil
}
