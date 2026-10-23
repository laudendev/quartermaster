package activations

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
)

// Seats are matched to computers by hardware signals as well as by the old
// fingerprint, so a reinstall (new fingerprint, same hardware) keeps its seat.
// Signals are stored salted per licence, so this table cannot be linked to the
// trial tables. A signal belongs to one seat of one licence; an owned signal is
// never reassigned, and a signal that matches several seats is only flagged.

// MaxSignalsPerSeat bounds how many signals one seat can own.
const MaxSignalsPerSeat = 16

const schemaSignals = `
CREATE TABLE IF NOT EXISTS activation_signals (
	license_id	TEXT NOT NULL,
	signal		TEXT NOT NULL,
	kind		  TEXT NOT NULL,
	activation_id TEXT NOT NULL,
	first_seen	INTEGER NOT NULL,
	last_seen	 INTEGER NOT NULL,
	PRIMARY KEY (license_id, signal)
);
CREATE INDEX IF NOT EXISTS idx_activation_signals_seat ON activation_signals(activation_id);
`

// Signal is one hashed hardware identifier as the client sends it.
type Signal struct {
	Kind string
	Hash string
}

// Outcome says what ClaimSeat did.
type Outcome int

const (
	Existing Outcome = iota // the fingerprint already held a seat
	Reseated                // a signal matched a seat; its fingerprint was updated
	New                     // a free seat was taken
	Full                    // no seat available
)

func (o Outcome) String() string {
	switch o {
	case Existing:
		return "existing"
	case Reseated:
		return "reseated"
	case New:
		return "new"
	default:
		return "full"
	}
}

// ClaimResult describes one ClaimSeat call.
type ClaimResult struct {
	Outcome Outcome
	SeatID  string
	Added   int  // signals newly attached to the seat
	Overlap bool // some signal belongs to, or matched, more than one seat
}

var claimMu sync.Mutex // one claim at a time: SQLite upgrades read locks badly

func saltedSignal(licenseID, hash string) string {
	sum := sha256.Sum256([]byte(licenseID + ":" + hash))
	return hex.EncodeToString(sum[:])
}

func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ClaimSeat finds or creates the seat for a computer, atomically. newID is the
// id for a new seat. seats is the licence's seat count.
func (s *Store) ClaimSeat(newID, licenseID, fingerprint string, seats int, sigs []Signal, now int64) (ClaimResult, error) {
	claimMu.Lock()
	defer claimMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ClaimResult{}, err
	}
	defer tx.Rollback()
	// A write statement first, so the write lock is taken now.
	if _, err := tx.Exec(`DELETE FROM activation_signals WHERE 0`); err != nil {
		return ClaimResult{}, err
	}

	var seatID string
	err = tx.QueryRow(
		`SELECT id FROM activations WHERE license_id = ? AND fingerprint = ?`,
		licenseID, fingerprint).Scan(&seatID)
	switch {
	case err == nil:
		added, overlap, err := learnSignals(tx, licenseID, seatID, sigs, now)
		if err != nil {
			return ClaimResult{}, err
		}
		return ClaimResult{Outcome: Existing, SeatID: seatID, Added: added, Overlap: overlap}, tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return ClaimResult{}, err
	}

	if len(sigs) > 0 {
		id, multi, found, err := matchSeat(tx, licenseID, sigs)
		if err != nil {
			return ClaimResult{}, err
		}
		if found {
			if _, err := tx.Exec(`UPDATE activations SET fingerprint = ? WHERE id = ?`, fingerprint, id); err != nil {
				return ClaimResult{}, err
			}
			added, overlap, err := learnSignals(tx, licenseID, id, sigs, now)
			if err != nil {
				return ClaimResult{}, err
			}
			return ClaimResult{Outcome: Reseated, SeatID: id, Added: added, Overlap: multi || overlap}, tx.Commit()
		}
	}

	var count int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM activations WHERE license_id = ? AND revoked = 0`,
		licenseID).Scan(&count); err != nil {
		return ClaimResult{}, err
	}
	if count >= seats {
		return ClaimResult{Outcome: Full}, nil
	}
	if _, err := tx.Exec(
		`INSERT INTO activations (id, license_id, fingerprint, activated_at) VALUES (?, ?, ?, ?)`,
		newID, licenseID, fingerprint, now); err != nil {
		return ClaimResult{}, err
	}
	added, overlap, err := learnSignals(tx, licenseID, newID, sigs, now)
	if err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{Outcome: New, SeatID: newID, Added: added, Overlap: overlap}, tx.Commit()
}

// DeactivateSeat frees a computer's seat: by fingerprint, or, when the
// fingerprint changed since activation, by a matching hardware signal. It does
// nothing when no seat matches.
func (s *Store) DeactivateSeat(licenseID, fingerprint string, sigs []Signal) error {
	claimMu.Lock()
	defer claimMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM activation_signals WHERE 0`); err != nil {
		return err
	}
	var seatID string
	err = tx.QueryRow(
		`SELECT id FROM activations WHERE license_id = ? AND fingerprint = ?`,
		licenseID, fingerprint).Scan(&seatID)
	if errors.Is(err, sql.ErrNoRows) {
		if len(sigs) == 0 {
			return tx.Commit()
		}
		id, _, found, err := matchSeat(tx, licenseID, sigs)
		if err != nil {
			return err
		}
		if !found {
			return tx.Commit()
		}
		seatID = id
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM activation_signals WHERE activation_id = ?`, seatID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM activations WHERE id = ?`, seatID); err != nil {
		return err
	}
	return tx.Commit()
}

// matchSeat finds the live seat of a licence that shares the most signals with
// sigs (ties: earliest activation, then lowest id). multi is true when more
// than one seat matched.
func matchSeat(tx *sql.Tx, licenseID string, sigs []Signal) (id string, multi, found bool, err error) {
	args := make([]any, 0, len(sigs)+1)
	args = append(args, licenseID)
	for _, sg := range sigs {
		args = append(args, saltedSignal(licenseID, sg.Hash))
	}
	rows, err := tx.Query(
		`SELECT s.activation_id
		   FROM activation_signals s JOIN activations a ON a.id = s.activation_id
		  WHERE s.license_id = ? AND a.revoked = 0 AND s.signal IN (`+marks(len(sigs))+`)
		  GROUP BY s.activation_id
		  ORDER BY COUNT(*) DESC, MIN(a.activated_at), s.activation_id`, args...)
	if err != nil {
		return "", false, false, err
	}
	var ids []string
	for rows.Next() {
		var one string
		if err := rows.Scan(&one); err != nil {
			rows.Close()
			return "", false, false, err
		}
		ids = append(ids, one)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", false, false, err
	}
	rows.Close()
	if len(ids) == 0 {
		return "", false, false, nil
	}
	return ids[0], len(ids) > 1, true, nil
}

// learnSignals attaches unowned signals to a seat (up to the cap), refreshes
// the ones it already owns, and reports whether any belongs to another seat.
func learnSignals(tx *sql.Tx, licenseID, seatID string, sigs []Signal, now int64) (added int, overlap bool, err error) {
	var have int
	if err = tx.QueryRow(
		`SELECT COUNT(*) FROM activation_signals WHERE activation_id = ?`, seatID).Scan(&have); err != nil {
		return 0, false, err
	}
	for _, sg := range sigs {
		salted := saltedSignal(licenseID, sg.Hash)
		var owner string
		err = tx.QueryRow(
			`SELECT activation_id FROM activation_signals WHERE license_id = ? AND signal = ?`,
			licenseID, salted).Scan(&owner)
		switch {
		case err == nil:
			if owner == seatID {
				if _, err = tx.Exec(
					`UPDATE activation_signals SET last_seen = ? WHERE license_id = ? AND signal = ?`,
					now, licenseID, salted); err != nil {
					return added, overlap, err
				}
			} else {
				overlap = true
			}
		case errors.Is(err, sql.ErrNoRows):
			err = nil
			if have >= MaxSignalsPerSeat {
				continue
			}
			if _, err = tx.Exec(
				`INSERT INTO activation_signals (license_id, signal, kind, activation_id, first_seen, last_seen)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				licenseID, salted, sg.Kind, seatID, now, now); err != nil {
				return added, overlap, err
			}
			have++
			added++
		default:
			return added, overlap, err
		}
	}
	return added, overlap, nil
}
