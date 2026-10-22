package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"quartermaster/license"
	"quartermaster/trials"
)

// trialV2Store is the part of trials.StoreV2 the handler uses.
type trialV2Store interface {
	Start(product string, sigs []trials.Signal, now, days int64) (trials.ResultV2, error)
}

// trialV2API serves POST /v2/trial/start: a free trial per computer, found by
// hardware signals (see package trials). The v1 endpoint keeps working for
// older apps.
type trialV2API struct {
	st  trialV2Store
	key ed25519.PrivateKey // nil when trial.key is missing: requests get 503
	now func() time.Time
}

type trialV2Request struct {
	Product string `json:"product"`
	Signals []struct {
		Kind string `json:"kind"`
		Hash string `json:"hash"`
	} `json:"signals"`
}

// start returns this computer's trial as a token bound to the signals it sent.
// Always 200 for a valid request: whether the trial is still running is for
// the app to show from ends_at. A client-supplied start date is not accepted:
// every computer's first v2 request starts a fresh trial.
func (t *trialV2API) start(w http.ResponseWriter, r *http.Request) {
	if t.key == nil {
		http.Error(w, "trials unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body trialV2Request
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Println("trial2: bad request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	days, ok := trialDays[body.Product]
	if !ok {
		log.Println("trial2: unknown product")
		http.Error(w, "unknown product", http.StatusBadRequest)
		return
	}
	in := make([]trials.Signal, len(body.Signals))
	for i, s := range body.Signals {
		in[i] = trials.Signal{Kind: s.Kind, Hash: s.Hash}
	}
	sigs, err := trials.ValidateSignals(in)
	if err != nil {
		log.Println("trial2: bad signals:", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	raw := make([][32]byte, len(sigs))
	for i, s := range sigs {
		b, err := hex.DecodeString(s.Hash)
		if err != nil || len(b) != 32 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		copy(raw[i][:], b)
	}

	res, err := t.st.Start(body.Product, sigs, t.now().Unix(), days)
	if err != nil {
		log.Println("trial2: store failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	token, err := license.SignTrialV2(t.key, license.TrialV2{
		Product:   body.Product,
		Binding:   license.TrialBinding(body.Product, raw),
		StartedAt: res.Record.StartedAt,
		EndsAt:    res.Record.EndsAt,
	})
	if err != nil {
		log.Println("trial2: signing failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	verb := "started"
	if res.Existed {
		verb = "restored"
	}
	log.Printf("trial2: %s — product %s id %d signals sent %d added %d conflict %v ends %s",
		verb, body.Product, res.Record.ID, len(sigs), res.Added, res.Conflict,
		time.Unix(res.Record.EndsAt, 0).UTC().Format(time.RFC3339))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(trialResponse{
		Token:     token,
		StartedAt: res.Record.StartedAt,
		EndsAt:    res.Record.EndsAt,
		Existing:  res.Existed,
	})
}
