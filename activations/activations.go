package activations

import (
	"database/sql"
	"strings"
	"time"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS activations (
    id           TEXT PRIMARY KEY,
    license_id   TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    activated_at INTEGER NOT NULL,
    revoked      INTEGER NOT NULL DEFAULT 0,
    UNIQUE(license_id, fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_activations_license ON activations(license_id);
`

func Open(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) CountActivations(licenseID string) (int, error) {
	var count int
	row := s.db.QueryRow(
		`SELECT COUNT(*) FROM activations WHERE license_id = ? AND revoked = 0`,
		licenseID)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) IsRevoked(licenseID string) (bool, error) {
	var revoked int
	row := s.db.QueryRow(
		`SELECT COUNT(*) FROM activations WHERE license_id = ? AND revoked = 1`,
		licenseID)
	if err := row.Scan(&revoked); err != nil {
		return false, err
	}
	return revoked > 0, nil
}

func (s *Store) Activate(id, licenseID, fingerprint string) error {
	_, err := s.db.Exec(
		`INSERT INTO activations (id, license_id, fingerprint, activated_at)
		 VALUES (?, ?, ?, ?)`,
		id, licenseID, fingerprint, time.Now().Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return nil
	}
	return err
}

func (s *Store) Deactivate(licenseID, fingerprint string) error {
	_, err := s.db.Exec(
		`DELETE FROM activations WHERE license_id = ? AND fingerprint = ?`,
		licenseID, fingerprint)
	return err
}

func (s *Store) IsActivated(licenseID, fingerprint string) (bool, error) {
	var count int
	row := s.db.QueryRow(
		`SELECT COUNT(*) FROM activations WHERE license_id = ? AND fingerprint = ? AND revoked = 0`,
		licenseID, fingerprint)
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
