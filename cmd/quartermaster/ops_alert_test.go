package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func captureAlerts(t *testing.T) *[]string {
	t.Helper()
	var got []string
	orig := sendOpsAlert
	sendOpsAlert = func(subject, body string) error {
		got = append(got, subject+"\n"+body)
		return nil
	}
	t.Cleanup(func() { sendOpsAlert = orig })
	return &got
}

func completedBody(id, status, country string) string {
	return fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":%q,"payment_status":%q,"customer_details":{"email":"buyer@example.com","address":{"country":%q}}}}}`, id, status, country)
}

func postCompleted(api *stripeAPI, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", signPayload("whsec_test", body, time.Now().Unix()))
	w := httptest.NewRecorder()
	api.webhook(w, req)
	return w
}

func TestWebhookAlertsOnPaidNonUSSession(t *testing.T) {
	alerts := captureAlerts(t)
	s := testQueueStore(t)
	api := &stripeAPI{st: s, secret: "whsec_test", lineItems: &fakeLineItems{}, products: &fakeProducts{}}

	w := postCompleted(api, completedBody("cs_alert_nonus", "paid", "DE"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if pending, _ := s.NextPending(); pending != nil {
		t.Fatalf("non-US session must not enqueue, got %+v", pending)
	}
	if len(*alerts) != 1 || !strings.Contains((*alerts)[0], "cs_alert_nonus") || !strings.Contains((*alerts)[0], "non-US") {
		t.Fatalf("expected one non-US alert naming the session, got %q", *alerts)
	}
}

func TestWebhookRejectsAndAlertsOnUnpaidSession(t *testing.T) {
	alerts := captureAlerts(t)
	s := testQueueStore(t)
	api := &stripeAPI{st: s, secret: "whsec_test", lineItems: &fakeLineItems{}, products: &fakeProducts{}}

	w := postCompleted(api, completedBody("cs_alert_unpaid", "unpaid", "US"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if pending, _ := s.NextPending(); pending != nil {
		t.Fatalf("unpaid session must not enqueue, got %+v", pending)
	}
	if len(*alerts) != 1 || !strings.Contains((*alerts)[0], "unpaid") {
		t.Fatalf("expected one unpaid alert, got %q", *alerts)
	}
}

func TestWebhookAlertsWhenUnitsOverMax(t *testing.T) {
	alerts := captureAlerts(t)
	s := testQueueStore(t)
	api := &stripeAPI{
		st:     s,
		secret: "whsec_test",
		lineItems: &fakeLineItems{items: map[string][]stripeLineItem{
			"cs_alert_units": {{Quantity: 25, Price: struct {
				ID string `json:"id"`
			}{ID: "price_book"}}},
		}},
		products: &fakeProducts{codes: map[string]string{"price_book": "BOOK"}},
	}

	w := postCompleted(api, completedBody("cs_alert_units", "paid", "US"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if pending, _ := s.NextPending(); pending != nil {
		t.Fatalf("over-limit session must not enqueue, got %+v", pending)
	}
	if len(*alerts) != 1 || !strings.Contains((*alerts)[0], "too many units") {
		t.Fatalf("expected one too-many-units alert, got %q", *alerts)
	}
}

func TestWebhookNoAlertForFulfilledSession(t *testing.T) {
	alerts := captureAlerts(t)
	s := testQueueStore(t)
	api := &stripeAPI{
		st:     s,
		secret: "whsec_test",
		lineItems: &fakeLineItems{items: map[string][]stripeLineItem{
			"cs_alert_ok": {{Quantity: 1, Price: struct {
				ID string `json:"id"`
			}{ID: "price_book"}}},
		}},
		products: &fakeProducts{codes: map[string]string{"price_book": "BOOK"}},
	}

	w := postCompleted(api, completedBody("cs_alert_ok", "paid", "US"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if pending, _ := s.NextPending(); pending == nil {
		t.Fatal("expected the paid US session to be enqueued")
	}
	if len(*alerts) != 0 {
		t.Fatalf("fulfilled session must not alert, got %q", *alerts)
	}
}
