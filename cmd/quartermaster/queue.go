package main

import (
	"encoding/json"
	"net/http"
	"time"
	"log"

	"quartermaster/store"
)

type queueAPI struct {
	st *store.Store
}

func (q *queueAPI) wait(w http.ResponseWriter, r *http.Request) {
	req, err := q.st.WaitPending(r.Context(), 55*time.Second)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if req == nil {
		w.WriteHeader(http.StatusNoContent) // 204: empty queue, poll again
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}
func (q *queueAPI) complete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID         string `json:"id"`
		LicenseKey string `json:"license_key,omitempty"`
		RejectNote string `json:"reject_note,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if body.RejectNote != "" {
		if err := q.st.Reject(body.ID, body.RejectNote); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if body.LicenseKey == "" {
		http.Error(w, "need license_key or reject_note", http.StatusBadRequest)
		return
	}

	email, err := q.st.Complete(body.ID, body.LicenseKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if email != "" {
		if err := sendLicenseEmail(email, body.LicenseKey); err != nil {
			log.Println("email send failed:", err) // logged, not fatal to the request
		}
	}
	w.WriteHeader(http.StatusOK)
}

