package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"

	"quartermaster/activations"
	"quartermaster/license"
)

type activationAPI struct {
	st   *activations.Store
	pubs []ed25519.PublicKey
}

func (a *activationAPI) activate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LicenseKey  string `json:"license_key"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LicenseKey == "" || body.Fingerprint == "" {
		log.Println("activate: bad request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	l, err := license.VerifyAny(a.pubs, body.LicenseKey)
	if err != nil {
		log.Println("activate: invalid license key:", err)
		http.Error(w, "invalid license", http.StatusUnauthorized)
		return
	}
	licenseID := licenseIDString(l)

	revoked, err := a.st.IsRevoked(licenseID)
	if err != nil {
		log.Println("activate: revoked check failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if revoked {
		log.Println("activate: rejected — license", licenseID, "is revoked")
		http.Error(w, "license revoked", http.StatusForbidden)
		return
	}

	alreadyActive, err := a.st.IsActivated(licenseID, body.Fingerprint)
	if err != nil {
		log.Println("activate: already-active check failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !alreadyActive {
		count, err := a.st.CountActivations(licenseID)
		if err != nil {
			log.Println("activate: count failed:", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count >= int(l.Seats) {
			log.Println("activate: rejected — license", licenseID, "seats exhausted", count, "/", l.Seats)
			http.Error(w, "no seats available", http.StatusConflict)
			return
		}
	}

	activationID, err := newActivationID()
	if err != nil {
		log.Println("activate: id generation failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := a.st.Activate(activationID, licenseID, body.Fingerprint); err != nil {
		log.Println("activate: store write failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Println("activate: success — license", licenseID, "fingerprint", body.Fingerprint)
	w.WriteHeader(http.StatusOK)
}

func (a *activationAPI) deactivate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LicenseKey  string `json:"license_key"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LicenseKey == "" || body.Fingerprint == "" {
		log.Println("deactivate: bad request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	l, err := license.VerifyAny(a.pubs, body.LicenseKey)
	if err != nil {
		log.Println("deactivate: invalid license key:", err)
		http.Error(w, "invalid license", http.StatusUnauthorized)
		return
	}
	licenseID := licenseIDString(l)

	if err := a.st.Deactivate(licenseID, body.Fingerprint); err != nil {
		log.Println("deactivate: store write failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Println("deactivate: success — license", licenseID, "fingerprint", body.Fingerprint)
	w.WriteHeader(http.StatusOK)
}

func licenseIDString(l license.License) string {
	return hex.EncodeToString(l.ID[:])
}

func newActivationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
