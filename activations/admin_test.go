package activations

import "testing"

func TestResetFreesSeatsButKeepsRevokedRows(t *testing.T) {
	s := testStore(t)
	for _, a := range [][3]string{
		{"a1", "lic1", "fp-a"},
		{"a2", "lic1", "fp-b"},
		{"a3", "lic2", "fp-c"},
	} {
		if err := s.Activate(a[0], a[1], a[2]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE activations SET revoked = 1 WHERE id = 'a2'`); err != nil {
		t.Fatal(err)
	}

	n, err := s.Reset("lic1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row removed, got %d", n)
	}
	left, err := s.List("lic1")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != "a2" || !left[0].Revoked {
		t.Fatalf("expected only the revoked row to remain, got %+v", left)
	}
	if revoked, _ := s.IsRevoked("lic1"); !revoked {
		t.Fatal("a revoked license must stay revoked after a reset")
	}
	other, err := s.CountActivations("lic2")
	if err != nil || other != 1 {
		t.Fatalf("other licenses must be untouched: count=%d err=%v", other, err)
	}
}

func TestListReportsActivationsInOrder(t *testing.T) {
	s := testStore(t)
	if got, err := s.List("none"); err != nil || len(got) != 0 {
		t.Fatalf("expected no rows, got %+v err=%v", got, err)
	}
	if err := s.Activate("a1", "lic1", "fp-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Activate("a2", "lic1", "fp-b"); err != nil {
		t.Fatal(err)
	}
	got, err := s.List("lic1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Fingerprint != "fp-a" || got[1].Fingerprint != "fp-b" {
		t.Fatalf("unexpected list: %+v", got)
	}
}
