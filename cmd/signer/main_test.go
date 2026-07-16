package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"quartermaster/license"
)

func TestPollOnceReturnsQueuedRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/queue/wait" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(signRequest{
			ID: "req_1", Product: "TRCR", Email: "buyer@example.com", Seats: 1,
		})
	}))
	defer srv.Close()

	req, err := pollOnce(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if req == nil || req.ID != "req_1" || req.Product != "TRCR" {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestPollOnceEmptyQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	req, err := pollOnce(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if req != nil {
		t.Fatalf("expected nil on empty queue, got %+v", req)
	}
}

func TestPollOnceServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := pollOnce(srv.URL)
	if err == nil {
		t.Fatal("expected an error on 500 response")
	}
}

func TestPollOnceMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := pollOnce(srv.URL)
	if err == nil {
		t.Fatal("expected an error on malformed JSON")
	}
}

func TestPostCompleteSendsCorrectPayload(t *testing.T) {
	var gotID, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotID = body["id"]
		gotKey = body["license_key"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := postComplete(srv.URL, "req_1", "SOME-LICENSE-KEY"); err != nil {
		t.Fatal(err)
	}
	if gotID != "req_1" || gotKey != "SOME-LICENSE-KEY" {
		t.Fatalf("unexpected payload: id=%q key=%q", gotID, gotKey)
	}
}

func TestPostCompleteServerRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := postComplete(srv.URL, "req_1", "KEY")
	if err == nil {
		t.Fatal("expected error on server 500")
	}
}

func TestPostRejectSendsCorrectPayload(t *testing.T) {
	var gotID, gotNote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotID = body["id"]
		gotNote = body["reject_note"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := postReject(srv.URL, "req_2", "bad product code"); err != nil {
		t.Fatal(err)
	}
	if gotID != "req_2" || gotNote != "bad product code" {
		t.Fatalf("unexpected payload: id=%q note=%q", gotID, gotNote)
	}
}

func TestIssueForProducesValidLicense(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	req := &signRequest{ID: "req_1", Product: "TRCR", Email: "buyer@example.com", Seats: 3}
	key, err := issueFor(priv, req)
	if err != nil {
		t.Fatal(err)
	}

	l, err := license.Verify(pub, key)
	if err != nil {
		t.Fatalf("issued license failed verification: %v", err)
	}
	if l.Product != "TRCR" || l.Seats != 3 {
		t.Fatalf("unexpected license contents: %+v", l)
	}
}

func TestIssueForRejectsBadProduct(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	req := &signRequest{ID: "req_1", Product: "", Email: "buyer@example.com", Seats: 1}

	_, err := issueFor(priv, req)
	if err == nil {
		t.Fatal("expected error for empty product code")
	}
}
