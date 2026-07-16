package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"testing"
	"time"
	"net/http"
	"net/http/httptest"
	"strings"

	"quartermaster/store"
)

// signPayload replicates Stripe's own signing scheme, so tests can
// construct genuinely valid signatures without needing a real Stripe account.
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
	old := time.Now().Add(-10 * time.Minute).Unix() // older than the 5-minute window
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
		"v1=abc", // missing timestamp
		"t=" + strconv.FormatInt(time.Now().Unix(), 10), // missing signature
	} {
		if api.verifySignature(badHeader, []byte(body)) {
			t.Fatalf("expected malformed header %q to fail", badHeader)
		}
	}
}

func TestWebhookRejectsNonUSCountry(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
				"metadata": {"product": "TRCR", "seats": "1"}
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
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
				"metadata": {"product": "TRCR", "seats": "2"}
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
	if pending.Product != "TRCR" || pending.Seats != 2 || pending.Email != "buyer@example.com" {
		t.Fatalf("unexpected queued request: %+v", pending)
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()

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
