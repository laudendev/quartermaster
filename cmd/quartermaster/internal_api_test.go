package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionStatusNotFound(t *testing.T) {
	s := testQueueStore(t)
	api := &sessionStatusAPI{st: s}

	req := httptest.NewRequest(http.MethodGet, "/internal/sessions/no-such/status", nil)
	req.SetPathValue("session_id", "no-such")
	w := httptest.NewRecorder()

	api.status(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp sessionStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Found {
		t.Error("expected found=false")
	}
}

func TestSessionStatusReadyWithItems(t *testing.T) {
	s := testQueueStore(t)
	api := &sessionStatusAPI{st: s}

	if err := s.Enqueue("txn_ready#0", "price_x", "BOOK", "buyer@example.com", 1); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	req0, _ := s.NextPending()
	if _, _, err := s.Complete(req0.ID, "LICENSE-KEY-1"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/internal/sessions/txn_ready/status", nil)
	req.SetPathValue("session_id", "txn_ready")
	w := httptest.NewRecorder()

	api.status(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp sessionStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !resp.Found || !resp.Ready {
		t.Errorf("expected found=true ready=true, got %+v", resp)
	}
	if len(resp.Items) != 1 || resp.Items[0].LicenseKey != "LICENSE-KEY-1" {
		t.Errorf("unexpected items: %+v", resp.Items)
	}
}

func TestRequireInternalSecretBlocksWrongSecret(t *testing.T) {
	called := false
	handler := requireInternalSecret("correct", func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/sessions/x/status", nil)
	req.Header.Set("X-Internal-Secret", "wrong")
	w := httptest.NewRecorder()

	handler(w, req)

	if called {
		t.Error("expected handler not called with wrong secret")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
