package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"

	"quartermaster/queue"
)

// requireInternalSecret protects service-to-service endpoints (called by
// softstore) with a static shared secret, sent as the X-Internal-Secret
// header, compared in constant time.
func requireInternalSecret(secret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Internal-Secret")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// receiptItemResponse is one product's row in the JSON receipt returned
// by the status endpoint, mirroring receiptLineItem's shape.
type receiptItemResponse struct {
	ProductName string `json:"product_name"`
	AmountLine  string `json:"amount_line"`
	LicenseKey  string `json:"license_key"`
}

// sessionStatusResponse is the JSON shape returned by
// GET /internal/sessions/{session_id}/status.
type sessionStatusResponse struct {
	Found     bool                  `json:"found"`
	Ready     bool                  `json:"ready"`
	Items     []receiptItemResponse `json:"items,omitempty"`
	TaxLine   string                `json:"tax_line,omitempty"`
	TotalLine string                `json:"total_line,omitempty"`
}

// sessionStatusAPI exposes fulfillment status for a checkout session,
// polled by softstore's thank-you page.
type sessionStatusAPI struct {
	st *queue.Store
}

func (a *sessionStatusAPI) status(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	status, err := a.st.GetSessionStatus(sessionID)
	if err != nil {
		log.Println("session status lookup failed for", sessionID, ":", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := sessionStatusResponse{Found: status.Found, Ready: status.Ready}
	if status.Ready {
		receiptItems, taxLine, totalLine := buildReceiptItems(sessionID, status.Items)
		resp.TaxLine = taxLine
		resp.TotalLine = totalLine
		resp.Items = make([]receiptItemResponse, 0, len(receiptItems))
		for _, item := range receiptItems {
			resp.Items = append(resp.Items, receiptItemResponse{
				ProductName: item.ProductName,
				AmountLine:  item.AmountLine,
				LicenseKey:  item.LicenseKey,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
