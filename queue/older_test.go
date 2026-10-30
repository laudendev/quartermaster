package queue

import (
	"context"
	"testing"
	"time"
)

func TestNextPendingOlderThanOnlyReturnsAgedRequests(t *testing.T) {
	s := newStaleTestStore(t)
	if err := s.Enqueue("fresh#0", "price", "POPF", "a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	backdate(t, s, "fresh#0", 10*time.Second)

	got, err := s.NextPendingOlderThan(2 * time.Minute)
	if err != nil || got != nil {
		t.Fatalf("fresh request must be hidden, got %v, %v", got, err)
	}
	got, err = s.NextPending()
	if err != nil || got == nil {
		t.Fatalf("NextPending must still see it, got %v, %v", got, err)
	}

	if err := s.Enqueue("old#0", "price", "POPF", "b@example.com", 1); err != nil {
		t.Fatal(err)
	}
	oldID := backdate(t, s, "old#0", 5*time.Minute)
	got, err = s.NextPendingOlderThan(2 * time.Minute)
	if err != nil || got == nil || got.ID != oldID {
		t.Fatalf("want the aged request %s, got %v, %v", oldID, got, err)
	}
}

func TestWaitPendingOlderThanPicksUpRequestOnceItAges(t *testing.T) {
	s := newStaleTestStore(t)
	if err := s.Enqueue("soon#0", "price", "POPF", "a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	id := backdate(t, s, "soon#0", time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := s.WaitPendingOlderThan(ctx, 6*time.Second, 2*time.Second)
	if err != nil || got == nil || got.ID != id {
		t.Fatalf("want %s once it aged, got %v, %v", id, got, err)
	}
}
