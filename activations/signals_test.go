package activations

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func sgn(kind string, n int) Signal {
	return Signal{Kind: kind, Hash: fmt.Sprintf("%064x", n)}
}

func sgs(ns ...int) []Signal {
	out := make([]Signal, 0, len(ns))
	for _, n := range ns {
		out = append(out, sgn("uuid", n))
	}
	return out
}

func signalCount(t *testing.T, s *Store, licenseID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM activation_signals WHERE license_id = ?`, licenseID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func claimT(t *testing.T, s *Store, id, lic, fp string, seats int, now int64, ns ...int) ClaimResult {
	t.Helper()
	res, err := s.ClaimSeat(id, lic, fp, seats, sgs(ns...), now)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func fileStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClaimFirstSeat(t *testing.T) {
	s := testStore(t)
	res := claimT(t, s, "seatA", "lic1", "fp-a", 1, 100, 1, 2, 3)
	if res.Outcome != New || res.SeatID != "seatA" || res.Added != 3 || res.Overlap {
		t.Fatalf("%+v", res)
	}
	if n, _ := s.CountActivations("lic1"); n != 1 {
		t.Fatalf("count %d", n)
	}
	if n := signalCount(t, s, "lic1"); n != 3 {
		t.Fatalf("signals %d", n)
	}
}

func TestClaimSameFingerprintIsExistingAndLearnsSignals(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-a", 1, 100)
	res := claimT(t, s, "seatX", "lic1", "fp-a", 1, 200, 1, 2)
	if res.Outcome != Existing || res.SeatID != "seatA" || res.Added != 2 {
		t.Fatalf("%+v", res)
	}
	if n, _ := s.CountActivations("lic1"); n != 1 {
		t.Fatalf("count %d", n)
	}
}

func TestClaimExhaustedForADifferentMachine(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-a", 1, 100, 1, 2)
	res := claimT(t, s, "seatB", "lic1", "fp-b", 1, 200, 3, 4)
	if res.Outcome != Full || res.SeatID != "" {
		t.Fatalf("%+v", res)
	}
	if n, _ := s.CountActivations("lic1"); n != 1 {
		t.Fatalf("count %d", n)
	}
	if n := signalCount(t, s, "lic1"); n != 2 {
		t.Fatalf("a refused claim must not learn signals, have %d", n)
	}
}

func TestClaimReinstallReseats(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-old", 1, 100, 1, 2, 3)
	res := claimT(t, s, "seatX", "lic1", "fp-new", 1, 200, 1, 2, 9)
	if res.Outcome != Reseated || res.SeatID != "seatA" || res.Added != 1 || res.Overlap {
		t.Fatalf("%+v", res)
	}
	if n, _ := s.CountActivations("lic1"); n != 1 {
		t.Fatalf("count %d", n)
	}
	if ok, _ := s.IsActivated("lic1", "fp-new"); !ok {
		t.Fatal("new fingerprint should hold the seat")
	}
	if ok, _ := s.IsActivated("lic1", "fp-old"); ok {
		t.Fatal("old fingerprint should be gone")
	}
	if n := signalCount(t, s, "lic1"); n != 4 {
		t.Fatalf("signals %d", n)
	}
	other := claimT(t, s, "seatB", "lic1", "fp-other", 1, 300, 7, 8)
	if other.Outcome != Full {
		t.Fatalf("a different machine must not take a taken seat: %+v", other)
	}
}

func TestClaimTwoSeatsKeepMachinesSeparate(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-a", 2, 100, 1, 2)
	claimT(t, s, "seatB", "lic1", "fp-b", 2, 200, 3, 4)
	res := claimT(t, s, "seatX", "lic1", "fp-a2", 2, 300, 1, 2)
	if res.Outcome != Reseated || res.SeatID != "seatA" {
		t.Fatalf("%+v", res)
	}
	if n, _ := s.CountActivations("lic1"); n != 2 {
		t.Fatalf("count %d", n)
	}
}

func TestClaimOverlapIsFlaggedNotBlocked(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-a", 2, 100, 1, 2)
	claimT(t, s, "seatB", "lic1", "fp-b", 2, 200, 3, 4)
	res := claimT(t, s, "seatC", "lic1", "fp-c", 2, 300, 2, 4)
	if res.Outcome != Reseated || res.SeatID != "seatA" || !res.Overlap {
		t.Fatalf("a tie goes to the earliest seat and is flagged: %+v", res)
	}
	if n, _ := s.CountActivations("lic1"); n != 2 {
		t.Fatalf("count %d", n)
	}
	if ok, _ := s.IsActivated("lic1", "fp-b"); !ok {
		t.Fatal("the other seat must be untouched")
	}
}

func TestClaimWithoutSignalsBehavesAsBefore(t *testing.T) {
	s := testStore(t)
	if r := claimT(t, s, "seatA", "lic1", "fp-a", 2, 100); r.Outcome != New {
		t.Fatalf("%+v", r)
	}
	if r := claimT(t, s, "seatB", "lic1", "fp-b", 2, 200); r.Outcome != New {
		t.Fatalf("%+v", r)
	}
	if r := claimT(t, s, "seatC", "lic1", "fp-c", 2, 300); r.Outcome != Full {
		t.Fatalf("%+v", r)
	}
	if r := claimT(t, s, "seatD", "lic1", "fp-a", 2, 400); r.Outcome != Existing {
		t.Fatalf("%+v", r)
	}
}

func TestSignalsAreScopedPerLicence(t *testing.T) {
	s := testStore(t)
	a := claimT(t, s, "seatA", "lic1", "fp-same", 1, 100, 1, 2)
	b := claimT(t, s, "seatB", "lic2", "fp-same", 1, 100, 1, 2)
	if a.Outcome != New || b.Outcome != New || b.Overlap {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

func TestSignalsAreSaltedAtRest(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-a", 1, 100, 1)
	raw := sgn("uuid", 1).Hash
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM activation_signals WHERE signal = ?`, raw).Scan(&n)
	if n != 0 {
		t.Fatal("the raw signal hash must not be stored")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM activation_signals WHERE signal = ?`, saltedSignal("lic1", raw)).Scan(&n)
	if n != 1 {
		t.Fatal("the salted hash should be stored")
	}
}

func TestDeactivateSeatByFingerprintAndBySignal(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-old", 1, 100, 1, 2)
	// The fingerprint changed (reinstall) but hardware did not.
	if err := s.DeactivateSeat("lic1", "fp-new", sgs(1)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountActivations("lic1"); n != 0 {
		t.Fatalf("count %d", n)
	}
	if n := signalCount(t, s, "lic1"); n != 0 {
		t.Fatalf("signals %d", n)
	}
	if r := claimT(t, s, "seatB", "lic1", "fp-b", 1, 200, 5); r.Outcome != New {
		t.Fatalf("%+v", r)
	}
	// Unknown computer: nothing happens.
	if err := s.DeactivateSeat("lic1", "fp-zzz", sgs(99)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountActivations("lic1"); n != 1 {
		t.Fatalf("count %d", n)
	}
}

func TestPlainDeactivateRemovesSignals(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-a", 1, 100, 1, 2)
	if err := s.Deactivate("lic1", "fp-a"); err != nil {
		t.Fatal(err)
	}
	if n := signalCount(t, s, "lic1"); n != 0 {
		t.Fatalf("signals %d", n)
	}
	if n, _ := s.CountActivations("lic1"); n != 0 {
		t.Fatalf("count %d", n)
	}
}

func TestResetClearsSignals(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-a", 2, 100, 1, 2)
	claimT(t, s, "seatB", "lic1", "fp-b", 2, 200, 3)
	claimT(t, s, "seatC", "lic2", "fp-c", 1, 300, 1)
	n, err := s.Reset("lic1")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if c := signalCount(t, s, "lic1"); c != 0 {
		t.Fatalf("lic1 signals %d", c)
	}
	if c := signalCount(t, s, "lic2"); c != 1 {
		t.Fatalf("another licence must keep its signals, have %d", c)
	}
}

func TestSignalCapPerSeat(t *testing.T) {
	s := testStore(t)
	claimT(t, s, "seatA", "lic1", "fp-a", 1, 100, 1, 2, 3, 4, 5, 6, 7, 8)
	claimT(t, s, "seatA", "lic1", "fp-a", 1, 200, 9, 10, 11, 12, 13, 14, 15, 16)
	res := claimT(t, s, "seatA", "lic1", "fp-a", 1, 300, 17, 18)
	if res.Added != 0 {
		t.Fatalf("%+v", res)
	}
	if n := signalCount(t, s, "lic1"); n != MaxSignalsPerSeat {
		t.Fatalf("signals %d", n)
	}
}

func TestConcurrentClaimsNeverExceedTheSeats(t *testing.T) {
	s := fileStore(t)
	var wg sync.WaitGroup
	outcomes := make(chan Outcome, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := s.ClaimSeat(fmt.Sprintf("seat%d", i), "lic1", fmt.Sprintf("fp%d", i), 2,
				[]Signal{sgn("uuid", 100+i)}, int64(i))
			if err != nil {
				t.Error(err)
				return
			}
			outcomes <- res.Outcome
		}(i)
	}
	wg.Wait()
	close(outcomes)
	news, fulls := 0, 0
	for o := range outcomes {
		switch o {
		case New:
			news++
		case Full:
			fulls++
		}
	}
	if news != 2 || fulls != 6 {
		t.Fatalf("new=%d full=%d", news, fulls)
	}
	if n, _ := s.CountActivations("lic1"); n != 2 {
		t.Fatalf("count %d", n)
	}
}
