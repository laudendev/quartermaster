package queue

import (
	"database/sql"
	"testing"
	"time"
)

func newStaleTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // each connection would get its own empty in-memory database
	t.Cleanup(func() { db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func backdate(t *testing.T, s *Store, txn string, age time.Duration) string {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE sign_requests SET created_at = ? WHERE txn_id = ?`,
		time.Now().Add(-age).Unix(), txn); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.db.QueryRow(`SELECT id FROM sign_requests WHERE txn_id = ?`, txn).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStalePendingListsOnlyOldPendingRequests(t *testing.T) {
	s := newStaleTestStore(t)
	for _, txn := range []string{"old#0", "new#0", "done#0"} {
		if err := s.Enqueue(txn, "price", "POPF", "a@example.com", 1); err != nil {
			t.Fatal(err)
		}
	}
	oldID := backdate(t, s, "old#0", 10*time.Minute)
	backdate(t, s, "new#0", time.Minute)
	doneID := backdate(t, s, "done#0", 10*time.Minute)
	if _, _, err := s.Complete(doneID, "KEY"); err != nil {
		t.Fatal(err)
	}

	got, err := s.StalePending(time.Now().Add(-5 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != oldID {
		t.Fatalf("got %+v, want only %s", got, oldID)
	}
}
