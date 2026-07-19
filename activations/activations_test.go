package activations

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
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

func TestActivateAndCount(t *testing.T) {
	s := testStore(t)

	if err := s.Activate("act1", "lic1", "fingerprint-a"); err != nil {
		t.Fatal(err)
	}
	count, err := s.CountActivations("lic1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 activation, got %d", count)
	}
}

func TestActivateSameFingerprintIdempotent(t *testing.T) {
	s := testStore(t)

	s.Activate("act1", "lic1", "fingerprint-a")
	if err := s.Activate("act2", "lic1", "fingerprint-a"); err != nil {
		t.Fatalf("re-activating same fingerprint should be idempotent, got: %v", err)
	}

	count, _ := s.CountActivations("lic1")
	if count != 1 {
		t.Fatalf("expected still 1 activation, got %d", count)
	}
}

func TestActivateDifferentFingerprintsCountSeparately(t *testing.T) {
	s := testStore(t)

	s.Activate("act1", "lic1", "fingerprint-a")
	s.Activate("act2", "lic1", "fingerprint-b")

	count, _ := s.CountActivations("lic1")
	if count != 2 {
		t.Fatalf("expected 2 activations, got %d", count)
	}
}

func TestDeactivateFreesSlot(t *testing.T) {
	s := testStore(t)

	s.Activate("act1", "lic1", "fingerprint-a")
	if err := s.Deactivate("lic1", "fingerprint-a"); err != nil {
		t.Fatal(err)
	}

	count, _ := s.CountActivations("lic1")
	if count != 0 {
		t.Fatalf("expected 0 activations after deactivate, got %d", count)
	}
}

func TestIsRevokedFalseByDefault(t *testing.T) {
	s := testStore(t)
	revoked, err := s.IsRevoked("lic_never_seen")
	if err != nil {
		t.Fatal(err)
	}
	if revoked {
		t.Fatal("expected false for a license with no revocation record")
	}
}
