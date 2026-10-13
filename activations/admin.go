package activations

import "time"

// Record is one activation row, as shown by the admin tool.
type Record struct {
	ID          string
	Fingerprint string
	ActivatedAt time.Time
	Revoked     bool
}

// List returns every activation row of a license, oldest first.
func (s *Store) List(licenseID string) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT id, fingerprint, activated_at, revoked FROM activations
		 WHERE license_id = ? ORDER BY activated_at, id`,
		licenseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var at int64
		var revoked int
		if err := rows.Scan(&r.ID, &r.Fingerprint, &at, &revoked); err != nil {
			return nil, err
		}
		r.ActivatedAt = time.Unix(at, 0)
		r.Revoked = revoked != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reset frees every seat of a license by deleting its non-revoked
// activations and returns how many it removed. Revoked rows are kept, so a
// revoked license cannot be reset into working again.
func (s *Store) Reset(licenseID string) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM activations WHERE license_id = ? AND revoked = 0`,
		licenseID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
