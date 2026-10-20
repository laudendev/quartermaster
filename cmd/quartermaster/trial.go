package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"quartermaster/license"
	"quartermaster/trials"
)

// trialDays is how long each product's free trial runs. Products not listed
// have no trial.
var trialDays = map[string]int64{"POPF": 14}

// maxClaimAge bounds how far back an app may date a trial it ran locally
// before trials were recorded here (one-time migration). An older or future
// date is ignored and the trial starts now. A claimed date can only shorten a
// trial, never extend it.
const maxClaimAge = 365 * 24 * 3600

type trialStore interface {
	Start(product, fingerprint string, startedAt, endsAt, now int64) (trials.Record, bool, error)
}

type trialAPI struct {
	st  trialStore
	key ed25519.PrivateKey // nil when trial.key is missing: requests get 503
	now func() time.Time
}

type trialResponse struct {
	Token     string `json:"token"`
	StartedAt int64  `json:"started_at"`
	EndsAt    int64  `json:"ends_at"`
	Existing  bool   `json:"existing"`
}

// start records this computer's trial, or returns the one it already has, as
// a signed token. Always 200 for a valid request: whether the trial is still
// running is for the app to show from ends_at.
func (t *trialAPI) start(w http.ResponseWriter, r *http.Request) {
	if t.key == nil {
		http.Error(w, "trials unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Product     string `json:"product"`
		Fingerprint string `json:"fingerprint"`
		StartedAt   int64  `json:"started_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Println("trial: bad request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	days, ok := trialDays[body.Product]
	if !ok {
		log.Println("trial: unknown product")
		http.Error(w, "unknown product", http.StatusBadRequest)
		return
	}
	fp, err := hex.DecodeString(body.Fingerprint)
	if err != nil || len(fp) != 32 {
		log.Println("trial: bad fingerprint")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	fingerprint := hex.EncodeToString(fp)

	now := t.now().Unix()
	started := now
	if body.StartedAt > 0 && body.StartedAt <= now && now-body.StartedAt <= maxClaimAge {
		started = body.StartedAt
	}
	rec, existed, err := t.st.Start(body.Product, fingerprint, started, started+days*86_400, now)
	if err != nil {
		log.Println("trial: store failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var fp32 [32]byte
	copy(fp32[:], fp)
	token, err := license.SignTrial(t.key, license.Trial{
		Product:     body.Product,
		Fingerprint: fp32,
		StartedAt:   rec.StartedAt,
		EndsAt:      rec.EndsAt,
	})
	if err != nil {
		log.Println("trial: signing failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	verb := "started"
	if existed {
		verb = "restored"
	}
	log.Println("trial:", verb, "— product", body.Product, "fingerprint", fingerprint[:12], "ends", time.Unix(rec.EndsAt, 0).UTC().Format(time.RFC3339))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(trialResponse{
		Token:     token,
		StartedAt: rec.StartedAt,
		EndsAt:    rec.EndsAt,
		Existing:  existed,
	})
}

// loadTrialKey reads the hex Ed25519 seed written by cmd/trial-keygen.
func loadTrialKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, err
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New(path + ": wrong key length")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
