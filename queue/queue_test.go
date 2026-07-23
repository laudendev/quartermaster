package queue

import (
	"context"
	"database/sql"
	"testing"
	"time"

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

func TestEnqueueAndNextPending(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_1", "BOOK", "buyer@example.com", 1); err != nil {
		t.Fatal(err)
	}

	req, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if req == nil {
		t.Fatal("expected a pending request, got nil")
	}
	if req.Product != "BOOK" || req.Email != "buyer@example.com" || req.Seats != 1 {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestEnqueueIdempotent(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_dup", "BOOK", "a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue("txn_dup", "BOOK", "a@example.com", 1); err != nil {
		t.Fatalf("second enqueue with same txn should be absorbed, got: %v", err)
	}

	var count int
	row := s.db.QueryRow(`SELECT COUNT(*) FROM sign_requests WHERE txn_id = ?`, "txn_dup")
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
	s.Enqueue("txn_complete", "BOOK", "buyer@example.com", 1)
	req, _ := s.NextPending()

	email, _, err := s.Complete(req.ID, "FAKE-KEY-123")
	if err != nil {
		t.Fatal(err)
	}
	if email != "buyer@example.com" {
		t.Fatalf("expected email returned, got %q", email)
	}

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
	s.Enqueue("txn_dup_complete", "BOOK", "buyer@example.com", 1)
	req, _ := s.NextPending()

	if _, _, err := s.Complete(req.ID, "FIRST-KEY"); err != nil {
		t.Fatal(err)
	}
	email, _, err := s.Complete(req.ID, "SECOND-KEY")
	if err != nil {
		t.Fatal(err)
	}
	if email != "" {
		t.Fatalf("expected empty email on no-op transition, got %q", email)
	}
}

func TestRejectTransitionsStatus(t *testing.T) {
	s := testStore(t)
	s.Enqueue("txn_reject", "BOOK", "buyer@example.com", 1)
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
	s.Enqueue("txn_wait", "BOOK", "buyer@example.com", 1)

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

func TestPendingEmailsFindsUnsent(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_1", "PROD", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req, err := s.NextPending()
	if err != nil || req == nil {
		t.Fatalf("expected a pending request, got %v, err %v", req, err)
	}
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-123"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	pending, err := s.PendingEmails(5)
	if err != nil {
		t.Fatalf("PendingEmails failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending email, got %d", len(pending))
	}
	if pending[0].Email != "buyer@example.com" {
		t.Errorf("expected buyer@example.com, got %q", pending[0].Email)
	}
	if pending[0].TxnID != "txn_1" {
		t.Errorf("expected txn_1, got %q", pending[0].TxnID)
	}
}

func TestMarkEmailSentExcludesFromPending(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_2", "PROD", "buyer2@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req, _ := s.NextPending()
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-456"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	if err := s.MarkEmailSent(req.ID); err != nil {
		t.Fatalf("MarkEmailSent failed: %v", err)
	}

	pending, err := s.PendingEmails(5)
	if err != nil {
		t.Fatalf("PendingEmails failed: %v", err)
	}
	for _, p := range pending {
		if p.ID == req.ID {
			t.Errorf("expected request %s to be excluded after MarkEmailSent, but it was still pending", req.ID)
		}
	}
}

func TestRecordEmailAttemptIncrementsCounter(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_3", "PROD", "buyer3@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req, _ := s.NextPending()
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-789"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	if err := s.RecordEmailAttempt(req.ID); err != nil {
		t.Fatalf("RecordEmailAttempt failed: %v", err)
	}
	if err := s.RecordEmailAttempt(req.ID); err != nil {
		t.Fatalf("RecordEmailAttempt failed: %v", err)
	}

	pending, err := s.PendingEmails(5)
	if err != nil {
		t.Fatalf("PendingEmails failed: %v", err)
	}
	var found bool
	for _, p := range pending {
		if p.ID == req.ID {
			found = true
			if p.Attempts != 2 {
				t.Errorf("expected 2 attempts recorded, got %d", p.Attempts)
			}
		}
	}
	if !found {
		t.Fatalf("expected request %s to still be pending after 2 failed attempts", req.ID)
	}
}

func TestPendingEmailsRespectsMaxAttempts(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_4", "PROD", "buyer4@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req, _ := s.NextPending()
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-999"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	// Exceed the retry cap.
	for i := 0; i < 5; i++ {
		if err := s.RecordEmailAttempt(req.ID); err != nil {
			t.Fatalf("RecordEmailAttempt failed: %v", err)
		}
	}

	pending, err := s.PendingEmails(5)
	if err != nil {
		t.Fatalf("PendingEmails failed: %v", err)
	}
	for _, p := range pending {
		if p.ID == req.ID {
			t.Errorf("expected request %s to be excluded once attempts reach the cap, but it was still pending", req.ID)
		}
	}
}
