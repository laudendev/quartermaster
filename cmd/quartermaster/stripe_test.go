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

// fakeLineItems is a test double for lineItemFetcher.
type fakeLineItems struct {
	items map[string][]stripeLineItem // sessionID -> items
	err   error
}

func (f *fakeLineItems) fetchLineItems(sessionID string) ([]stripeLineItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items[sessionID], nil
}

// fakeProducts is a test double for productResolver.
type fakeProducts struct {
	codes map[string]string // priceID -> productCode
	err   error
}

func (f *fakeProducts) resolveProductCode(priceID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	code, ok := f.codes[priceID]
	if !ok {
		return "", fmt.Errorf("no product code mapped for price %s", priceID)
	}
	return code, nil
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
	api := &stripeAPI{st: s, secret: "whsec_test", lineItems: &fakeLineItems{}, products: &fakeProducts{}}
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
	api := &stripeAPI{
		st:     s,
		secret: "whsec_test",
		lineItems: &fakeLineItems{
			items: map[string][]stripeLineItem{
				"cs_test_us": {
					{Quantity: 2, Price: struct {
						ID string `json:"id"`
					}{ID: "price_book"}},
				},
			},
		},
		products: &fakeProducts{
			codes: map[string]string{"price_book": "BOOK"},
		},
	}

	body := `{
		"type": "checkout.session.completed",
		"data": {
			"object": {
				"id": "cs_test_us",
				"customer_details": {
					"email": "buyer@example.com",
					"address": {"country": "US"}
				}
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

	// Quantity 2 of one product should enqueue 2 separate single-seat requests.
	first, err := s.NextPending()
	if err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("expected a first queued row, got nil")
	}
	if first.Product != "BOOK" || first.Seats != 1 || first.Email != "buyer@example.com" {
		t.Fatalf("unexpected first queued request: %+v", first)
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {

	s := testQueueStore(t)
	api := &stripeAPI{st: s, secret: "whsec_test", lineItems: &fakeLineItems{}, products: &fakeProducts{}}

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
	api := &stripeAPI{
		st:     s,
		secret: "whsec_test",
		lineItems: &fakeLineItems{
			items: map[string][]stripeLineItem{
				"cs_test_toomanyseats": {
					{Quantity: 500, Price: struct {
						ID string `json:"id"`
					}{ID: "price_book"}},
				},
			},
		},
		products: &fakeProducts{
			codes: map[string]string{"price_book": "BOOK"},
		},
	}

	body := `{
		"type": "checkout.session.completed",
		"data": {
			"object": {
				"id": "cs_test_toomanyseats",
				"customer_details": {
					"email": "buyer@example.com",
					"address": {"country": "US"}
				}
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
		t.Fatalf("over-max-units checkout should not enqueue, got %+v", pending)
	}
}

func TestWebhookEnqueuesMultipleDistinctProducts(t *testing.T) {
	s := testQueueStore(t)
	api := &stripeAPI{
		st:     s,
		secret: "whsec_test",
		lineItems: &fakeLineItems{
			items: map[string][]stripeLineItem{
				"cs_test_cart": {
					{Quantity: 1, Price: struct {
						ID string `json:"id"`
					}{ID: "price_book"}},
					{Quantity: 1, Price: struct {
						ID string `json:"id"`
					}{ID: "price_widget"}},
				},
			},
		},
		products: &fakeProducts{
			codes: map[string]string{
				"price_book":   "BOOK",
				"price_widget": "TWDG",
			},
		},
	}

	body := `{
		"type": "checkout.session.completed",
		"data": {
			"object": {
				"id": "cs_test_cart",
				"customer_details": {
					"email": "buyer@example.com",
					"address": {"country": "US"}
				}
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

	var products []string
	for {
		pending, err := s.NextPending()
		if err != nil {
			t.Fatal(err)
		}
		if pending == nil {
			break
		}
		products = append(products, pending.Product)

		if _, _, err := s.Complete(pending.ID, "dummy-key-"+pending.Product); err != nil {
			t.Fatalf("failed to drain queue via Complete: %v", err)
		}
	}

	if len(products) != 2 {
		t.Fatalf("expected 2 queued requests for 2 distinct products, got %d: %v", len(products), products)
	}
	seen := map[string]bool{products[0]: true, products[1]: true}
	if !seen["BOOK"] || !seen["TWDG"] {
		t.Errorf("expected products BOOK and TWDG, got %v", products)
	}
}
