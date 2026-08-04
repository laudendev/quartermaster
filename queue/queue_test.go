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

	if err := s.Enqueue("txn_1", "price_x", "BOOK", "buyer@example.com", 1); err != nil {
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

	if err := s.Enqueue("txn_dup", "price_x", "BOOK", "a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue("txn_dup", "price_x", "BOOK", "a@example.com", 1); err != nil {
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
	s.Enqueue("txn_complete", "price_x", "BOOK", "buyer@example.com", 1)
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
	s.Enqueue("txn_dup_complete", "price_x", "BOOK", "buyer@example.com", 1)
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
	s.Enqueue("txn_reject", "price_x", "BOOK", "buyer@example.com", 1)
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
	s.Enqueue("txn_wait", "price_x", "BOOK", "buyer@example.com", 1)

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

func TestReadySessionsFindsFullySignedSession(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_a#0", "price_x", "PROD", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req, err := s.NextPending()
	if err != nil || req == nil {
		t.Fatalf("expected a pending request, got %v, err %v", req, err)
	}
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-123"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	ready, err := s.ReadySessions(5)
	if err != nil {
		t.Fatalf("ReadySessions failed: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready session, got %d", len(ready))
	}
	if ready[0].SessionID != "txn_a" {
		t.Errorf("expected session id 'txn_a', got %q", ready[0].SessionID)
	}
	if ready[0].Email != "buyer@example.com" {
		t.Errorf("expected buyer@example.com, got %q", ready[0].Email)
	}
	if len(ready[0].Items) != 1 || ready[0].Items[0].LicenseKey != "LICENSE-KEY-123" {
		t.Errorf("unexpected items: %+v", ready[0].Items)
	}
}

func TestReadySessionsExcludesPartiallySignedSession(t *testing.T) {
	s := testStore(t)

	// Two units in the same session; only sign one.
	if err := s.Enqueue("txn_b#0", "price_x", "PROD_A", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue item 0 failed: %v", err)
	}
	if err := s.Enqueue("txn_b#1", "price_y", "PROD_B", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue item 1 failed: %v", err)
	}

	req, _ := s.NextPending()
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-FIRST"); err != nil {
		t.Fatalf("complete first item failed: %v", err)
	}

	// One of the two units is still pending — session should NOT be ready.
	ready, err := s.ReadySessions(5)
	if err != nil {
		t.Fatalf("ReadySessions failed: %v", err)
	}
	for _, rs := range ready {
		if rs.SessionID == "txn_b" {
			t.Errorf("expected partially-signed session 'txn_b' to be excluded, got %+v", rs)
		}
	}
}

func TestReadySessionsIncludesAllItemsOnceFullySigned(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_c#0", "price_x", "PROD_A", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue item 0 failed: %v", err)
	}
	if err := s.Enqueue("txn_c#1", "price_y", "PROD_B", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue item 1 failed: %v", err)
	}

	for i := 0; i < 2; i++ {
		req, err := s.NextPending()
		if err != nil || req == nil {
			t.Fatalf("expected pending item %d, got %v, err %v", i, req, err)
		}
		if _, _, err := s.Complete(req.ID, "LICENSE-KEY-"+req.Product); err != nil {
			t.Fatalf("complete item %d failed: %v", i, err)
		}
	}

	ready, err := s.ReadySessions(5)
	if err != nil {
		t.Fatalf("ReadySessions failed: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready session, got %d", len(ready))
	}
	if len(ready[0].Items) != 2 {
		t.Fatalf("expected 2 items in ready session, got %d: %+v", len(ready[0].Items), ready[0].Items)
	}
}

func TestMarkSessionEmailSentExcludesFromReady(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_d#0", "price_x", "PROD", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req, _ := s.NextPending()
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-456"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	if err := s.MarkSessionEmailSent("txn_d"); err != nil {
		t.Fatalf("MarkSessionEmailSent failed: %v", err)
	}

	ready, err := s.ReadySessions(5)
	if err != nil {
		t.Fatalf("ReadySessions failed: %v", err)
	}
	for _, rs := range ready {
		if rs.SessionID == "txn_d" {
			t.Errorf("expected session 'txn_d' to be excluded after MarkSessionEmailSent, but it was still ready")
		}
	}
}

func TestRecordSessionEmailAttemptIncrementsCounter(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_e#0", "price_x", "PROD", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req, _ := s.NextPending()
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-789"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	if err := s.RecordSessionEmailAttempt("txn_e"); err != nil {
		t.Fatalf("RecordSessionEmailAttempt failed: %v", err)
	}
	if err := s.RecordSessionEmailAttempt("txn_e"); err != nil {
		t.Fatalf("RecordSessionEmailAttempt failed: %v", err)
	}

	ready, err := s.ReadySessions(5)
	if err != nil {
		t.Fatalf("ReadySessions failed: %v", err)
	}
	var found bool
	for _, rs := range ready {
		if rs.SessionID == "txn_e" {
			found = true
			if rs.Attempts != 2 {
				t.Errorf("expected 2 attempts recorded, got %d", rs.Attempts)
			}
		}
	}
	if !found {
		t.Fatalf("expected session 'txn_e' to still be ready after 2 failed attempts")
	}
}

func TestReadySessionsRespectsMaxAttempts(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_f#0", "price_x", "PROD", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req, _ := s.NextPending()
	if _, _, err := s.Complete(req.ID, "LICENSE-KEY-999"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := s.RecordSessionEmailAttempt("txn_f"); err != nil {
			t.Fatalf("RecordSessionEmailAttempt failed: %v", err)
		}
	}

	ready, err := s.ReadySessions(5)
	if err != nil {
		t.Fatalf("ReadySessions failed: %v", err)
	}
	for _, rs := range ready {
		if rs.SessionID == "txn_f" {
			t.Errorf("expected session 'txn_f' to be excluded once attempts reach the cap, but it was still ready")
		}
	}
}

func TestGetSessionStatusNotFound(t *testing.T) {
	s := testStore(t)

	status, err := s.GetSessionStatus("no-such-session")
	if err != nil {
		t.Fatalf("GetSessionStatus failed: %v", err)
	}
	if status.Found {
		t.Error("expected Found=false for unknown session")
	}
	if status.Ready {
		t.Error("expected Ready=false for unknown session")
	}
}

func TestGetSessionStatusPartiallySigned(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_g#0", "price_x", "PROD_A", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue item 0 failed: %v", err)
	}
	if err := s.Enqueue("txn_g#1", "price_y", "PROD_B", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue item 1 failed: %v", err)
	}
	req, _ := s.NextPending()
	if _, _, err := s.Complete(req.ID, "KEY-1"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	status, err := s.GetSessionStatus("txn_g")
	if err != nil {
		t.Fatalf("GetSessionStatus failed: %v", err)
	}
	if !status.Found {
		t.Error("expected Found=true")
	}
	if status.Ready {
		t.Error("expected Ready=false while one item still pending")
	}
	if len(status.Items) != 0 {
		t.Errorf("expected no items exposed while not ready, got %+v", status.Items)
	}
}

func TestGetSessionStatusFullySigned(t *testing.T) {
	s := testStore(t)

	if err := s.Enqueue("txn_h#0", "price_x", "PROD_A", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue item 0 failed: %v", err)
	}
	if err := s.Enqueue("txn_h#1", "price_y", "PROD_B", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue item 1 failed: %v", err)
	}

	for i := 0; i < 2; i++ {
		req, err := s.NextPending()
		if err != nil || req == nil {
			t.Fatalf("expected pending item %d, got %v, err %v", i, req, err)
		}
		if _, _, err := s.Complete(req.ID, "KEY-"+req.Product); err != nil {
			t.Fatalf("complete item %d failed: %v", i, err)
		}
	}

	status, err := s.GetSessionStatus("txn_h")
	if err != nil {
		t.Fatalf("GetSessionStatus failed: %v", err)
	}
	if !status.Ready {
		t.Error("expected Ready=true once all items signed")
	}
	if len(status.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(status.Items))
	}
}
