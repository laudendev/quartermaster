package trials

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Trials v2 identify a computer by a set of hardware signals instead of one
// fingerprint. Each signal is the SHA-256 (hex) of one hardware identifier, made
// on the computer; the server never sees the identifier itself. A computer
// matches a trial when it shares at least one signal with it, so a reinstall
// (same hardware) and a replaced part (most signals unchanged) keep the same
// trial, while two computers that only share an operating-system image do not.
//
// Rules, all applied in one transaction:
//   - no submitted signal is owned: a new trial starts and owns all of them;
//   - one trial owns some: that trial is returned, and submitted signals nobody
//     owns are added to it (up to MaxSignalsPerTrial), so gradual part
//     replacement keeps matching;
//   - several trials own some (a weak or colliding signal): they are never
//     merged and an owned signal is never reassigned; the trial matching the
//     most submitted signals wins (ties: earliest start, then lowest id).

// MaxSignalsPerTrial bounds how many signals one trial can own.
const MaxSignalsPerTrial = 16

// Signal is one hashed hardware identifier. Hash is 64 lowercase hex characters.
type Signal struct {
	Kind string
	Hash string
}

// RecordV2 is one computer's trial of one product.
type RecordV2 struct {
	ID        int64
	Product   string
	StartedAt int64
	EndsAt    int64
	CreatedAt int64
	Signals   int // signals owned (filled by List and Get)
}

// ResultV2 describes what Start did.
type ResultV2 struct {
	Record   RecordV2
	Existed  bool // an earlier trial matched
	Conflict bool // submitted signals belonged to more than one trial
	Added    int  // signals newly attached to the matched trial
}

// StoreV2 keeps v2 trials in the Quartermaster database.
type StoreV2 struct {
	db *sql.DB
	mu sync.Mutex // one Start at a time: SQLite upgrades read locks badly
}

const schemaV2 = `
CREATE TABLE IF NOT EXISTS trials_v2 (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    product    TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    ends_at    INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS trial_signals (
    product    TEXT NOT NULL,
    signal     TEXT NOT NULL,
    kind       TEXT NOT NULL,
    trial_id   INTEGER NOT NULL,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    PRIMARY KEY (product, signal)
);
CREATE INDEX IF NOT EXISTS trial_signals_trial ON trial_signals (trial_id);
`

// OpenV2 creates the v2 tables if needed.
func OpenV2(db *sql.DB) (*StoreV2, error) {
	if _, err := db.Exec(schemaV2); err != nil {
		return nil, err
	}
	return &StoreV2{db: db}, nil
}

type candidate struct {
	ID        int64
	Matches   int
	StartedAt int64
}

// pickOwner chooses which trial a computer belongs to: most matching signals,
// then earliest start, then lowest id. conflict is true when more than one
// trial matched.
func pickOwner(cands []candidate) (best candidate, conflict bool) {
	if len(cands) == 0 {
		return candidate{}, false
	}
	sorted := append([]candidate(nil), cands...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Matches != b.Matches {
			return a.Matches > b.Matches
		}
		if a.StartedAt != b.StartedAt {
			return a.StartedAt < b.StartedAt
		}
		return a.ID < b.ID
	})
	return sorted[0], len(sorted) > 1
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// Start finds or creates the trial for a computer with these signals. The
// caller validates the signals (see ValidateSignals). days is the trial length.
func (s *StoreV2) Start(product string, sigs []Signal, now, days int64) (ResultV2, error) {
	if len(sigs) == 0 {
		return ResultV2{}, errors.New("no signals")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ResultV2{}, err
	}
	defer tx.Rollback()
	// A write statement first, so this transaction takes SQLite's write lock
	// now instead of failing with "database is locked" when it upgrades later.
	if _, err := tx.Exec(`DELETE FROM trial_signals WHERE 0`); err != nil {
		return ResultV2{}, err
	}

	args := make([]any, 0, len(sigs)+1)
	args = append(args, product)
	for _, sg := range sigs {
		args = append(args, sg.Hash)
	}
	rows, err := tx.Query(
		`SELECT s.signal, t.id, t.started_at, t.ends_at, t.created_at
		   FROM trial_signals s JOIN trials_v2 t ON t.id = s.trial_id
		  WHERE s.product = ? AND s.signal IN (`+placeholders(len(sigs))+`)`, args...)
	if err != nil {
		return ResultV2{}, err
	}
	owned := map[string]int64{} // signal -> owning trial id
	counts := map[int64]*candidate{}
	recs := map[int64]RecordV2{}
	for rows.Next() {
		var sig string
		var r RecordV2
		if err := rows.Scan(&sig, &r.ID, &r.StartedAt, &r.EndsAt, &r.CreatedAt); err != nil {
			rows.Close()
			return ResultV2{}, err
		}
		r.Product = product
		owned[sig] = r.ID
		recs[r.ID] = r
		c := counts[r.ID]
		if c == nil {
			c = &candidate{ID: r.ID, StartedAt: r.StartedAt}
			counts[r.ID] = c
		}
		c.Matches++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ResultV2{}, err
	}
	rows.Close()

	var res ResultV2
	if len(counts) == 0 {
		// Signals left behind by a deleted trial would block the insert below.
		clean := append([]any{product}, args[1:]...)
		if _, err := tx.Exec(
			`DELETE FROM trial_signals WHERE product = ? AND signal IN (`+placeholders(len(sigs))+`)`,
			clean...); err != nil {
			return ResultV2{}, err
		}
		r, err := tx.Exec(
			`INSERT INTO trials_v2 (product, started_at, ends_at, created_at) VALUES (?, ?, ?, ?)`,
			product, now, now+days*86_400, now)
		if err != nil {
			return ResultV2{}, err
		}
		id, err := r.LastInsertId()
		if err != nil {
			return ResultV2{}, err
		}
		for _, sg := range sigs {
			if _, err := tx.Exec(
				`INSERT INTO trial_signals (product, signal, kind, trial_id, first_seen, last_seen)
				 VALUES (?, ?, ?, ?, ?, ?)`, product, sg.Hash, sg.Kind, id, now, now); err != nil {
				return ResultV2{}, err
			}
		}
		res.Record = RecordV2{ID: id, Product: product, StartedAt: now, EndsAt: now + days*86_400, CreatedAt: now, Signals: len(sigs)}
		res.Added = len(sigs)
		return res, tx.Commit()
	}

	cands := make([]candidate, 0, len(counts))
	for _, c := range counts {
		cands = append(cands, *c)
	}
	best, conflict := pickOwner(cands)
	res.Record = recs[best.ID]
	res.Existed = true
	res.Conflict = conflict

	var have int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM trial_signals WHERE trial_id = ?`, best.ID).Scan(&have); err != nil {
		return ResultV2{}, err
	}
	for _, sg := range sigs {
		if _, taken := owned[sg.Hash]; taken {
			continue // never reassign an owned signal
		}
		if have >= MaxSignalsPerTrial {
			break
		}
		r, err := tx.Exec(
			`INSERT OR IGNORE INTO trial_signals (product, signal, kind, trial_id, first_seen, last_seen)
			 VALUES (?, ?, ?, ?, ?, ?)`, product, sg.Hash, sg.Kind, best.ID, now, now)
		if err != nil {
			return ResultV2{}, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			have++
			res.Added++
		}
	}
	mine := []any{now, product, best.ID}
	for _, sg := range sigs {
		if owned[sg.Hash] == best.ID {
			mine = append(mine, sg.Hash)
		}
	}
	if len(mine) > 3 {
		if _, err := tx.Exec(
			`UPDATE trial_signals SET last_seen = ? WHERE product = ? AND trial_id = ? AND signal IN (`+
				placeholders(len(mine)-3)+`)`, mine...); err != nil {
			return ResultV2{}, err
		}
	}
	res.Record.Signals = have
	return res, tx.Commit()
}

// GetV2 returns one trial by id.
func (s *StoreV2) GetV2(id int64) (RecordV2, bool, error) {
	r := RecordV2{ID: id}
	err := s.db.QueryRow(
		`SELECT t.product, t.started_at, t.ends_at, t.created_at,
		        (SELECT COUNT(*) FROM trial_signals WHERE trial_id = t.id)
		   FROM trials_v2 t WHERE t.id = ?`, id).
		Scan(&r.Product, &r.StartedAt, &r.EndsAt, &r.CreatedAt, &r.Signals)
	if errors.Is(err, sql.ErrNoRows) {
		return RecordV2{}, false, nil
	}
	if err != nil {
		return RecordV2{}, false, err
	}
	return r, true, nil
}

// ListV2 returns the newest trials of a product.
func (s *StoreV2) ListV2(product string, limit int) ([]RecordV2, error) {
	rows, err := s.db.Query(
		`SELECT t.id, t.product, t.started_at, t.ends_at, t.created_at,
		        (SELECT COUNT(*) FROM trial_signals WHERE trial_id = t.id)
		   FROM trials_v2 t WHERE t.product = ? ORDER BY t.created_at DESC, t.id DESC LIMIT ?`, product, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordV2
	for rows.Next() {
		var r RecordV2
		if err := rows.Scan(&r.ID, &r.Product, &r.StartedAt, &r.EndsAt, &r.CreatedAt, &r.Signals); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResetV2 deletes a trial and its signals so that computer can start a new one
// (support only). It reports how many trials were deleted.
func (s *StoreV2) ResetV2(product string, id int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM trial_signals WHERE product = ? AND trial_id = ?`, product, id); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM trials_v2 WHERE product = ? AND id = ?`, product, id)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// CountLegacy reports how many v1 trials a product has.
func (s *StoreV2) CountLegacy(product string) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM trials WHERE product = ?`, product).Scan(&n)
	return n, err
}

// PurgeLegacy deletes every v1 trial of a product (the table `trials`). Use it
// once the v2 server and app are live; v1 clients simply start a new trial.
func (s *StoreV2) PurgeLegacy(product string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM trials WHERE product = ?`, product)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Signal kinds the client may send. "os" is a fallback for computers with no
// readable hardware identifier and is only accepted on its own.
var signalKinds = map[string]bool{"uuid": true, "serial": true, "board": true, "disk": true, "os": true}

// MaxSignalsPerRequest bounds one request.
const MaxSignalsPerRequest = 8

// ValidateSignals normalises and checks the signals of one request: 1 to
// MaxSignalsPerRequest, known kinds, 64 hex characters each (lowercased), no
// repeats, and "os" only on its own.
func ValidateSignals(in []Signal) ([]Signal, error) {
	if len(in) == 0 || len(in) > MaxSignalsPerRequest {
		return nil, fmt.Errorf("need 1 to %d signals", MaxSignalsPerRequest)
	}
	out := make([]Signal, 0, len(in))
	seen := map[string]bool{}
	for _, sg := range in {
		if !signalKinds[sg.Kind] {
			return nil, fmt.Errorf("unknown signal kind %q", sg.Kind)
		}
		h := strings.ToLower(sg.Hash)
		if len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
			return nil, errors.New("signal hash must be 64 hex characters")
		}
		if seen[h] {
			return nil, errors.New("repeated signal")
		}
		seen[h] = true
		out = append(out, Signal{Kind: sg.Kind, Hash: h})
	}
	if len(out) > 1 {
		for _, sg := range out {
			if sg.Kind == "os" {
				return nil, errors.New("os signal must be sent alone")
			}
		}
	}
	return out, nil
}
