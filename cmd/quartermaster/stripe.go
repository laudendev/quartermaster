package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"quartermaster/queue"
)

// lineItemFetcher retrieves the finalized line items for a completed
// Checkout Session. Abstracted so tests can supply a fake instead of
// hitting Stripe's real API.
type lineItemFetcher interface {
	fetchLineItems(sessionID string) ([]stripeLineItem, error)
}

// productResolver translates a Stripe Price ID into softstore's product
// code. Abstracted so tests can supply a fake instead of calling
// softstore's real internal API over the network.
type productResolver interface {
	resolveProductCode(priceID string) (string, error)
}

// cartClearer empties a softstore cart after its purchase has been
// fully fulfilled. Abstracted so tests can supply a fake instead of
// calling softstore's real internal API over the network.
type cartClearer interface {
	clearCart(cartToken string) error
}

type stripeAPI struct {
	st     *queue.Store
	secret string // whsec_... from `stripe listen` or the dashboard

	lineItems lineItemFetcher
	products  productResolver
	carts     cartClearer
}

// realStripeClient is the production implementation of lineItemFetcher,
// calling Stripe's API directly.
type realStripeClient struct {
	stripeSecretKey string
}

// realSoftstoreClient is the production implementation of productResolver,
// calling softstore's internal API directly.
type realSoftstoreClient struct {
	baseURL     string
	internalKey string
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
				Product   string `json:"product"`
				Seats     string `json:"seats"`
				CartToken string `json:"cart_token"`
			} `json:"metadata"`
		} `json:"object"`
	} `json:"data"`
}

// stripeLineItem is one entry from a Checkout Session's line_items list.
type stripeLineItem struct {
	Quantity int64 `json:"quantity"`
	Price    struct {
		ID string `json:"id"`
	} `json:"price"`
}

type stripeLineItemsResponse struct {
	Data []stripeLineItem `json:"data"`
}

// fetchLineItems retrieves the finalized line items (price + quantity)
// for a completed Checkout Session, used to determine exactly what was
// purchased for fulfillment — the webhook payload itself only ever
// carries minimal, non-expandable fields.
func (c *realStripeClient) fetchLineItems(sessionID string) ([]stripeLineItem, error) {
	url := fmt.Sprintf("https://api.stripe.com/v1/checkout/sessions/%s/line_items?limit=100", sessionID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build stripe line items request: %w", err)
	}
	req.SetBasicAuth(c.stripeSecretKey, "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stripe line items fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stripe line items fetch: unexpected status %d", resp.StatusCode)
	}

	var parsed stripeLineItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode stripe line items: %w", err)
	}
	return parsed.Data, nil
}

// productCodeResponse mirrors softstore's internal lookup response shape.
type productCodeResponse struct {
	ProductCode string `json:"product_code"`
}

// resolveProductCode calls softstore's internal API to translate a Stripe
// Price ID (from a completed checkout's line items) into the product
// code Quartermaster uses for licensing and file delivery.
func (c *realSoftstoreClient) resolveProductCode(priceID string) (string, error) {
	url := fmt.Sprintf("%s/internal/products/by-price/%s", c.baseURL, priceID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build softstore lookup request: %w", err)
	}
	req.Header.Set("X-Internal-Secret", c.internalKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("softstore lookup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("softstore lookup: unexpected status %d for price %s", resp.StatusCode, priceID)
	}

	var parsed productCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode softstore lookup response: %w", err)
	}
	return parsed.ProductCode, nil
}

// clearCart calls softstore's internal API to empty a cart after its
// purchase has been fully fulfilled.
func (c *realSoftstoreClient) clearCart(cartToken string) error {
	url := fmt.Sprintf("%s/internal/cart/clear", c.baseURL)
	payload, err := json.Marshal(map[string]string{"cart_token": cartToken})
	if err != nil {
		return fmt.Errorf("marshal clear cart payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build softstore clear cart request: %w", err)
	}
	req.Header.Set("X-Internal-Secret", c.internalKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("softstore clear cart: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("softstore clear cart: unexpected status %d", resp.StatusCode)
	}
	return nil
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

	lineItems, err := s.lineItems.fetchLineItems(obj.ID)
	if err != nil {
		log.Println("stripe webhook: fetch line items failed for session", obj.ID, ":", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	const maxUnitsPerSession = 24
	var totalUnits int64
	for _, li := range lineItems {
		totalUnits += li.Quantity
	}
	if totalUnits > maxUnitsPerSession {
		log.Println("stripe webhook: rejecting session", obj.ID,
			"- total units", totalUnits, "exceeds maximum of", maxUnitsPerSession)
		w.WriteHeader(http.StatusOK)
		return
	}

	unitIndex := 0
	for _, li := range lineItems {
		productCode, err := s.products.resolveProductCode(li.Price.ID)
		if err != nil {
			log.Println("stripe webhook: resolve product code failed for session", obj.ID,
				"price", li.Price.ID, ":", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		for i := int64(0); i < li.Quantity; i++ {
			txnID := fmt.Sprintf("%s#%d", obj.ID, unitIndex)
			if err := s.st.Enqueue(txnID, li.Price.ID, productCode, obj.CustomerDetails.Email, 1); err != nil {
				log.Println("stripe webhook: enqueue failed for", txnID, ":", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			log.Println("stripe webhook: enqueued", txnID, "product", productCode, "email", obj.CustomerDetails.Email)
			unitIndex++
		}
	}

	if obj.Metadata.CartToken != "" {
		if err := s.carts.clearCart(obj.Metadata.CartToken); err != nil {
			// Don't fail the whole webhook over this — fulfillment already
			// succeeded, which is what actually matters to the customer.
			// A cart that lingers a bit longer is a cosmetic issue, not
			// a lost order.
			log.Println("stripe webhook: clear cart failed for token", obj.Metadata.CartToken, ":", err)
		} else {
			log.Println("stripe webhook: cleared cart", obj.Metadata.CartToken)
		}
	}

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
