package store

import (
	"context"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEnqueueAndNextPending(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_1", "TRCR", "buyer@example.com", 1); err != nil {
		t.Fatal(err)
	}

	req, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if req == nil {
		t.Fatal("expected a pending request, got nil")
	}
	if req.Product != "TRCR" || req.Email != "buyer@example.com" || req.Seats != 1 {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestEnqueueIdempotent(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_dup", "TRCR", "a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	// Same paddle_txn again — must not error, must not create a second row.
	if err := s.Enqueue("txn_dup", "TRCR", "a@example.com", 1); err != nil {
		t.Fatalf("second enqueue with same txn should be absorbed, got: %v", err)
	}

	var count int
	row := s.db.QueryRow(`SELECT COUNT(*) FROM sign_requests WHERE paddle_txn = ?`, "txn_dup")
	if err := row.Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row, got %d", count)
	}
}

func TestNextPendingEmptyQueue(t *testing.T) {
	s := testStore(t)
	req, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if req != nil {
		t.Fatalf("expected nil on empty queue, got %+v", req)
	}
}

func TestCompleteTransitionsStatus(t *testing.T) {
	s := testStore(t)
	s.Enqueue("txn_complete", "TRCR", "buyer@example.com", 1)
	req, _ := s.NextPending()

	email, err := s.Complete(req.ID, "FAKE-KEY-123")
	if err != nil {
		t.Fatal(err)
	}
	if email != "buyer@example.com" {
		t.Fatalf("expected email returned, got %q", email)
	}

	// Signed rows must not be returned by NextPending again.
	again, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("signed row should not be pending anymore, got %+v", again)
	}
}

func TestCompleteIsIdempotent(t *testing.T) {
	s := testStore(t)
	s.Enqueue("txn_dup_complete", "TRCR", "buyer@example.com", 1)
	req, _ := s.NextPending()

	if _, err := s.Complete(req.ID, "FIRST-KEY"); err != nil {
		t.Fatal(err)
	}
	// Second Complete call on an already-signed row: guarded by the
	// WHERE status='pending' clause — should succeed but do nothing.
	email, err := s.Complete(req.ID, "SECOND-KEY")
	if err != nil {
		t.Fatal(err)
	}
	if email != "" {
		t.Fatalf("expected empty email on no-op transition, got %q", email)
	}
}

func TestRejectTransitionsStatus(t *testing.T) {
	s := testStore(t)
	s.Enqueue("txn_reject", "TRCR", "buyer@example.com", 1)
	req, _ := s.NextPending()

	if err := s.Reject(req.ID, "invalid product code"); err != nil {
		t.Fatal(err)
	}

	again, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("rejected row should not be pending anymore, got %+v", again)
	}
}

func TestWaitPendingReturnsExistingWork(t *testing.T) {
	s := testStore(t)
	s.Enqueue("txn_wait", "TRCR", "buyer@example.com", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := s.WaitPending(ctx, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if req == nil {
		t.Fatal("expected work immediately, got nil")
	}
}

func TestWaitPendingTimesOutOnEmptyQueue(t *testing.T) {
	s := testStore(t)

	ctx := context.Background()
	start := time.Now()
	req, err := s.WaitPending(ctx, 500*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if req != nil {
		t.Fatalf("expected nil on timeout, got %+v", req)
	}
	if elapsed < 500*time.Millisecond {
		t.Fatalf("returned too early: %v", elapsed)
	}
}

func TestWaitPendingRespectsCancellation(t *testing.T) {
	s := testStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := s.WaitPending(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context.Canceled error")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("took too long to respect cancellation: %v", elapsed)
	}
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
