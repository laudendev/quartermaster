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

// sessionStatusResponse is the JSON shape returned by
// GET /internal/sessions/{session_id}/status.
type sessionStatusResponse struct {
	Found bool                `json:"found"`
	Ready bool                `json:"ready"`
	Items []queue.SessionItem `json:"items,omitempty"`
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessionStatusResponse{
		Found: status.Found,
		Ready: status.Ready,
		Items: status.Items,
	})
}
