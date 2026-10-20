package trials

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStartRecordsOnceAndKeepsTheOriginalDates(t *testing.T) {
	s := openStore(t)
	first, existed, err := s.Start("POPF", "aa", 100, 200, 100)
	if err != nil || existed {
		t.Fatalf("first start: existed=%v err=%v", existed, err)
	}
	again, existed, err := s.Start("POPF", "aa", 900, 1000, 900)
	if err != nil || !existed {
		t.Fatalf("second start: existed=%v err=%v", existed, err)
	}
	if again.StartedAt != first.StartedAt || again.EndsAt != first.EndsAt {
		t.Fatalf("dates moved: %+v then %+v", first, again)
	}
}

func TestTrialsAreSeparatePerProductAndComputer(t *testing.T) {
	s := openStore(t)
	if _, existed, _ := s.Start("POPF", "aa", 1, 2, 1); existed {
		t.Fatal("unexpected existing")
	}
	if _, existed, _ := s.Start("POPF", "bb", 1, 2, 1); existed {
		t.Fatal("another computer shared the trial")
	}
	if _, existed, _ := s.Start("LAMP", "aa", 1, 2, 1); existed {
		t.Fatal("another product shared the trial")
	}
}

func TestResetAllowsANewTrial(t *testing.T) {
	s := openStore(t)
	s.Start("POPF", "aa", 1, 2, 1)
	n, err := s.Reset("POPF", "aa")
	if err != nil || n != 1 {
		t.Fatalf("reset: n=%d err=%v", n, err)
	}
	if _, found, _ := s.Get("POPF", "aa"); found {
		t.Fatal("record still there")
	}
}
