package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"quartermaster/queue"
)

type stripeAPI struct {
	st     *queue.Store
	secret string // whsec_... from `stripe listen` or the dashboard
}

// stripeEvent is the minimal shape we need from checkout.session.completed.
type stripeEvent struct {
	Type string `json:"type"`
	Data struct {
		Object struct {
			ID              string `json:"id"`
			CustomerDetails struct {
				Email   string `json:"email"`
				Address struct {
					Country string `json:"country"`
				} `json:"address"`
			} `json:"customer_details"`
			Metadata struct {
				Product string `json:"product"`
				Seats   string `json:"seats"`
			} `json:"metadata"`
		} `json:"object"`
	} `json:"data"`
}

func (s *stripeAPI) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Println("stripe webhook: failed to read body:", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !s.verifySignature(r.Header.Get("Stripe-Signature"), body) {
		log.Println("stripe webhook: signature verification failed")
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	var evt stripeEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		log.Println("stripe webhook: failed to parse payload:", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	if evt.Type != "checkout.session.completed" {
		log.Println("stripe webhook: ignoring event type", evt.Type)
		w.WriteHeader(http.StatusOK) // acknowledge, ignore other event types
		return
	}

	obj := evt.Data.Object
	if !strings.EqualFold(obj.CustomerDetails.Address.Country, "US") {
		// Out of the market we're registered to sell in. Acknowledge the
		// webhook so Stripe doesn't retry, but never enqueue.
		log.Println("stripe webhook: rejecting non-US checkout, session",
			obj.ID,
			"country",
			obj.CustomerDetails.Address.Country)
		w.WriteHeader(http.StatusOK)
		return
	}

	seats, _ := strconv.Atoi(obj.Metadata.Seats)
	if seats <= 0 {
		seats = 1
	}
	const maxSeats = 24
	if seats > maxSeats {
		log.Println("stripe webhook: rejecting session", obj.ID,
			"- seats", seats, "exceeds maximum of", maxSeats,
			"metadata likely malformed or tampered")
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := s.st.Enqueue(obj.ID, obj.Metadata.Product, obj.CustomerDetails.Email, seats); err != nil {
		log.Println("stripe webhook: enqueue failed for session", obj.ID, ":", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Println("stripe webhook: enqueued session", obj.ID, "product", obj.Metadata.Product, "email", obj.CustomerDetails.Email)
	w.WriteHeader(http.StatusOK)
}

// verifySignature implements Stripe's webhook signature scheme:
// header is "t=<timestamp>,v1=<hmac>[,v1=<hmac>...]"
// signed payload is "<timestamp>.<raw body>"
// HMAC-SHA256 keyed with the webhook signing secret.
func (s *stripeAPI) verifySignature(header string, body []byte) bool {
	var timestamp string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			timestamp = kv[1]
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if timestamp == "" || len(sigs) == 0 {
		return false
	}

	// Replay defense: reject signatures older than 5 minutes.
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)) > 5*time.Minute {
		return false
	}

	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(timestamp + "." + string(body)))
	expected := hex.EncodeToString(mac.Sum(nil))

	for _, sig := range sigs {
		if hmac.Equal([]byte(sig), []byte(expected)) {
			return true
		}
	}
	return false
}
