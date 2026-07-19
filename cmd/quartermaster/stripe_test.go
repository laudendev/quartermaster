package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"quartermaster/queue"
)

func testQueueStore(t *testing.T) *queue.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := queue.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func signPayload(secret, body string, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, body)))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, sig)
}

func TestVerifySignatureValid(t *testing.T) {
	api := &stripeAPI{secret: "whsec_test_secret"}
	body := `{"type":"checkout.session.completed"}`
	header := signPayload("whsec_test_secret", body, time.Now().Unix())

	if !api.verifySignature(header, []byte(body)) {
		t.Fatal("expected valid signature to pass")
	}
}

func TestVerifySignatureWrongSecret(t *testing.T) {
	api := &stripeAPI{secret: "whsec_test_secret"}
	body := `{"type":"checkout.session.completed"}`
	header := signPayload("whsec_WRONG_secret", body, time.Now().Unix())

	if api.verifySignature(header, []byte(body)) {
		t.Fatal("expected wrong-secret signature to fail")
	}
}

func TestVerifySignatureTamperedBody(t *testing.T) {
	api := &stripeAPI{secret: "whsec_test_secret"}
	body := `{"type":"checkout.session.completed"}`
	header := signPayload("whsec_test_secret", body, time.Now().Unix())

	tamperedBody := `{"type":"checkout.session.completed","amount":999999}`
	if api.verifySignature(header, []byte(tamperedBody)) {
		t.Fatal("expected tampered body to fail verification")
	}
}

func TestVerifySignatureExpiredTimestamp(t *testing.T) {
	api := &stripeAPI{secret: "whsec_test_secret"}
	body := `{"type":"checkout.session.completed"}`
	old := time.Now().Add(-10 * time.Minute).Unix()
	header := signPayload("whsec_test_secret", body, old)

	if api.verifySignature(header, []byte(body)) {
		t.Fatal("expected stale timestamp to fail (replay defense)")
	}
}

func TestVerifySignatureMalformedHeader(t *testing.T) {
	api := &stripeAPI{secret: "whsec_test_secret"}
	body := `{"type":"checkout.session.completed"}`

	for _, badHeader := range []string{
		"",
		"garbage",
		"t=notanumber,v1=abc",
		"v1=abc",
		"t=" + strconv.FormatInt(time.Now().Unix(), 10),
	} {
		if api.verifySignature(badHeader, []byte(body)) {
			t.Fatalf("expected malformed header %q to fail", badHeader)
		}
	}
}

func TestWebhookRejectsNonUSCountry(t *testing.T) {
	s := testQueueStore(t)
	api := &stripeAPI{st: s, secret: "whsec_test"}

	body := `{
		"type": "checkout.session.completed",
		"data": {
			"object": {
				"id": "cs_test_nonus",
				"customer_details": {
					"email": "buyer@example.com",
					"address": {"country": "DE"}
				},
				"metadata": {"product": "BOOK", "seats": "1"}
			}
		}
	}`

	req := httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", signPayload("whsec_test", body, time.Now().Unix()))
	w := httptest.NewRecorder()

	api.webhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (acknowledged, not enqueued), got %d", w.Code)
	}

	pending, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if pending != nil {
		t.Fatalf("non-US checkout should not enqueue, got %+v", pending)
	}
}

func TestWebhookEnqueuesValidUSCheckout(t *testing.T) {
	s := testQueueStore(t)
	api := &stripeAPI{st: s, secret: "whsec_test"}

	body := `{
		"type": "checkout.session.completed",
		"data": {
			"object": {
				"id": "cs_test_us",
				"customer_details": {
					"email": "buyer@example.com",
					"address": {"country": "US"}
				},
				"metadata": {"product": "BOOK", "seats": "2"}
			}
		}
	}`

	req := httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", signPayload("whsec_test", body, time.Now().Unix()))
	w := httptest.NewRecorder()

	api.webhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	pending, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil {
		t.Fatal("expected a queued row, got nil")
	}
	if pending.Product != "BOOK" || pending.Seats != 2 || pending.Email != "buyer@example.com" {
		t.Fatalf("unexpected queued request: %+v", pending)
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	s := testQueueStore(t)
	api := &stripeAPI{st: s, secret: "whsec_test"}
	body := `{"type":"checkout.session.completed"}`

	req := httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", signPayload("whsec_WRONG", body, time.Now().Unix()))
	w := httptest.NewRecorder()

	api.webhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad signature, got %d", w.Code)
	}
}

func TestWebhookRejectsSeatsOverMax(t *testing.T) {
	s := testQueueStore(t)
	api := &stripeAPI{st: s, secret: "whsec_test"}

	body := `{
		"type": "checkout.session.completed",
		"data": {
			"object": {
				"id": "cs_test_toomanyseats",
				"customer_details": {
					"email": "buyer@example.com",
					"address": {"country": "US"}
				},
				"metadata": {"product": "BOOK", "seats": "500"}
			}
		}
	}`

	req := httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", signPayload("whsec_test", body, time.Now().Unix()))
	w := httptest.NewRecorder()

	api.webhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (acknowledged, not enqueued), got %d", w.Code)
	}

	pending, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if pending != nil {
		t.Fatalf("over-max-seats checkout should not enqueue, got %+v", pending)
	}
}
