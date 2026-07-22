package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"quartermaster/queue"
)

type queueAPI struct {
	st *queue.Store
}

func (q *queueAPI) wait(w http.ResponseWriter, r *http.Request) {
	req, err := q.st.WaitPending(r.Context(), 55*time.Second)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if req == nil {
		w.WriteHeader(http.StatusNoContent)
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
		log.Println("queue complete: bad request body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if body.RejectNote != "" {
		if err := q.st.Reject(body.ID, body.RejectNote); err != nil {
			log.Println("queue complete: reject failed for", body.ID, ":", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		log.Println("queue complete: rejected", body.ID, "-", body.RejectNote)
		w.WriteHeader(http.StatusOK)
		return
	}

	if body.LicenseKey == "" {
		log.Println("queue complete: missing license_key and reject_note for", body.ID)
		http.Error(w, "need license_key or reject_note", http.StatusBadRequest)
		return
	}

	email, err := q.st.Complete(body.ID, body.LicenseKey)
	if err != nil {
		log.Println("queue complete: store update failed for", body.ID, ":", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Println("queue complete: signed", body.ID)
	if email != "" {
		if err := sendLicenseEmail(body.ID, email, body.LicenseKey); err != nil {
			log.Println("email send failed:", err)
		} else {
			log.Println("email sent:", email)
		}
	}
	w.WriteHeader(http.StatusOK)
}
