package main

import (
	"encoding/json"
	"net/http"
	"time"

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
	var opErr error
	if body.RejectNote != "" {
		opErr = q.st.Reject(body.ID, body.RejectNote)
	} else if body.LicenseKey != "" {
		opErr = q.st.Complete(body.ID, body.LicenseKey)
	} else {
		http.Error(w, "need license_key or reject_note", http.StatusBadRequest)
		return
	}
	if opErr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
