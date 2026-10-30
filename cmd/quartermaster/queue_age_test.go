package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseMinAge(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 0, true}, {"0", 0, true}, {"120", 120 * time.Second, true},
		{"3600", time.Hour, true}, {"3601", 0, false}, {"-1", 0, false}, {"abc", 0, false},
	} {
		got, ok := parseMinAge(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseMinAge(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestWaitHidesFreshRequestsFromBackupSigner(t *testing.T) {
	s := testQueueStore(t)
	if err := s.Enqueue("fresh#0", "price", "POPF", "a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	api := &queueAPI{st: s, waitTimeout: 100 * time.Millisecond}

	rec := httptest.NewRecorder()
	api.wait(rec, httptest.NewRequest("GET", "/queue/wait?min_age=120", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("backup signer saw a fresh request: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	api.wait(rec, httptest.NewRequest("GET", "/queue/wait", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("primary signer should see it: status %d", rec.Code)
	}
}

func TestWaitRejectsBadMinAge(t *testing.T) {
	api := &queueAPI{st: testQueueStore(t), waitTimeout: 50 * time.Millisecond}
	rec := httptest.NewRecorder()
	api.wait(rec, httptest.NewRequest("GET", "/queue/wait?min_age=-5", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestCompleteOfNonPendingRequestIsConflict(t *testing.T) {
	api := &queueAPI{st: testQueueStore(t)}
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"id":"nope","license_key":"KEY"}`)
	api.complete(rec, httptest.NewRequest("POST", "/queue/complete", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", rec.Code)
	}
}
