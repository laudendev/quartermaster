package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"quartermaster/activations"
	"quartermaster/license"
)

type activationAPI struct {
	st   *activations.Store
	pubs []ed25519.PublicKey
}

var productPaths = map[string]string{
	"BOOK": "/opt/quartermaster/products/travelers-guide-to-computing.zip",
	"TEST": "/opt/quartermaster/products/test-widget-cli.zip",
	"OFTL": "/opt/quartermaster/products/test-office-tool.zip",
	"RAYY": "/opt/quartermaster/products/shrink-ray-3000.zip",
	"OVEN": "/opt/quartermaster/products/quantum-oven-manual.zip",
	"YETI": "/opt/quartermaster/products/arctic-yeti-care-guide.zip",
	"LAMP": "/opt/quartermaster/products/lava-lamp.zip",
	"POPF": "/opt/quartermaster/products/popfocus/popfocus.zip",
}

func productPath(product string) (string, error) {
	path, ok := productPaths[product]
	if !ok {
		return "", fmt.Errorf("unknown product %q", product)
	}
	return path, nil
}

// platformPaths holds per-OS builds for products that ship installers. The
// launcher sends its OS ("linux", "macos", "windows"); older launchers send
// nothing and get the product's default file from productPaths. The base name
// of each path is the filename the buyer receives.
var platformPaths = map[string]map[string]string{
	"POPF": {
		"linux":   "/opt/quartermaster/products/popfocus/popfocus_amd64.deb",
		"macos":   "/opt/quartermaster/products/popfocus/PopFocus.dmg",
		"windows": "/opt/quartermaster/products/popfocus/PopFocus-setup.exe",
	},
}

// productFile picks the file to serve for a product on a platform, falling
// back to the product's default file when the platform is empty or unknown.
func productFile(product, platform string) (string, error) {
	if byOS, ok := platformPaths[product]; ok {
		if path, ok := byOS[platform]; ok {
			return path, nil
		}
	}
	return productPath(product)
}

// distroPaths holds distro-family builds, keyed "<platform>/<distro>". The
// launcher sends a distro only when it recognises one (currently "fedora",
// which covers Fedora and derivatives such as Nobara). Anything else on that
// platform keeps the platformPaths file.
var distroPaths = map[string]map[string]string{
	"POPF": {
		"linux/fedora": "/opt/quartermaster/products/popfocus/popfocus_x86_64.rpm",
	},
}

// productFileFor is productFile plus an optional distro refinement. An empty
// or unrecognised distro (older launchers) falls through to productFile.
func productFileFor(product, platform, distro string) (string, error) {
	if distro != "" {
		if path, ok := distroPaths[product][platform+"/"+distro]; ok {
			return path, nil
		}
	}
	return productFile(product, platform)
}

func (a *activationAPI) download(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LicenseKey  string `json:"license_key"`
		Fingerprint string `json:"fingerprint"`
		Platform    string `json:"platform"`
		Distro      string `json:"distro"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LicenseKey == "" || body.Fingerprint == "" {
		log.Println("download: bad request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	l, err := license.VerifyAny(a.pubs, body.LicenseKey)
	if err != nil {
		log.Println("download: invalid license key:", err)
		http.Error(w, "invalid license", http.StatusUnauthorized)
		return
	}
	licenseID := licenseIDString(l)

	revoked, err := a.st.IsRevoked(licenseID)
	if err != nil {
		log.Println("download: revoked check failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if revoked {
		log.Println("download: rejected — license", licenseID, "is revoked")
		http.Error(w, "license revoked", http.StatusForbidden)
		return
	}

	alreadyActive, err := a.st.IsActivated(licenseID, body.Fingerprint)
	if err != nil {
		log.Println("download: already-active check failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !alreadyActive {
		count, err := a.st.CountActivations(licenseID)
		if err != nil {
			log.Println("download: count failed:", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count >= int(l.Seats) {
			log.Println("download: rejected — license", licenseID, "seats exhausted", count, "/", l.Seats)
			http.Error(w, "no seats available", http.StatusConflict)
			return
		}
	}

	path, err := productFileFor(l.Product, body.Platform, body.Distro)
	if err != nil {
		log.Println("download: no product file for", l.Product, body.Platform, ":", err)
		http.Error(w, "product unavailable", http.StatusInternalServerError)
		return
	}
	if _, statErr := os.Stat(path); statErr != nil {
		log.Println("download: product file missing for", l.Product, body.Platform, ":", statErr)
		http.Error(w, "product unavailable", http.StatusInternalServerError)
		return
	}

	activationID, err := newActivationID()
	if err != nil {
		log.Println("download: id generation failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := a.st.Activate(activationID, licenseID, body.Fingerprint); err != nil {
		log.Println("download: activation store write failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Println("download: activated and serving — license", licenseID, "fingerprint", body.Fingerprint, "product", l.Product)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	http.ServeFile(w, r, path)
}

func (a *activationAPI) activate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LicenseKey  string       `json:"license_key"`
		Fingerprint string       `json:"fingerprint"`
		Signals     []signalWire `json:"signals"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LicenseKey == "" || body.Fingerprint == "" {
		log.Println("activate: bad request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sigs, err := parseSignals(body.Signals)
	if err != nil {
		log.Println("activate: bad signals:", err)
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

	activationID, err := newActivationID()
	if err != nil {
		log.Println("activate: id generation failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// One atomic step: the same computer (fingerprint, or a shared hardware
	// signal after a reinstall) keeps its seat; otherwise a free seat is taken.
	res, err := a.st.ClaimSeat(activationID, licenseID, body.Fingerprint, int(l.Seats), sigs, seatClock())
	if err != nil {
		log.Println("activate: store write failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if res.Outcome == activations.Full {
		log.Println("activate: rejected — license", licenseID, "seats exhausted /", l.Seats)
		http.Error(w, "no seats available", http.StatusConflict)
		return
	}

	log.Printf("activate: %s — license %s fingerprint %s product %s signals sent %d added %d overlap %v",
		res.Outcome, licenseID, body.Fingerprint, l.Product, len(sigs), res.Added, res.Overlap)
	w.WriteHeader(http.StatusOK)
}

func (a *activationAPI) deactivate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LicenseKey  string       `json:"license_key"`
		Fingerprint string       `json:"fingerprint"`
		Signals     []signalWire `json:"signals"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LicenseKey == "" || body.Fingerprint == "" {
		log.Println("deactivate: bad request")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sigs, err := parseSignals(body.Signals)
	if err != nil {
		log.Println("deactivate: bad signals:", err)
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

	if err := a.st.DeactivateSeat(licenseID, body.Fingerprint, sigs); err != nil {
		log.Println("deactivate: store write failed:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Println("deactivate: success — license", licenseID, "fingerprint", body.Fingerprint, "signals sent", len(sigs))
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
