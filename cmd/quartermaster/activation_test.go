package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"quartermaster/activations"
	"quartermaster/license"
)

func testActivationsStore(t *testing.T) *activations.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := activations.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testLicenseKey(t *testing.T, priv ed25519.PrivateKey, seats int) string {
	t.Helper()
	id, err := license.NewID()
	if err != nil {
		t.Fatal(err)
	}
	key, err := license.Issue(priv, license.License{
		Product:  "BOOK",
		ID:       id,
		MajorVer: 1,
		Seats:    uint16(seats),
		IssuedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestActivateFirstSeat(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := testActivationsStore(t)

	api := &activationAPI{st: s, pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 1)

	body, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": "machine-a"})
	req := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body)))
	w := httptest.NewRecorder()

	api.activate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestActivateSeatsExhausted(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := testActivationsStore(t)

	api := &activationAPI{st: s, pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 1)

	body1, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": "machine-a"})
	req1 := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body1)))
	w1 := httptest.NewRecorder()
	api.activate(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first activation should succeed, got %d", w1.Code)
	}

	body2, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": "machine-b"})
	req2 := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body2)))
	w2 := httptest.NewRecorder()
	api.activate(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 (no seats), got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestActivateSameMachineTwiceIsIdempotent(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := testActivationsStore(t)

	api := &activationAPI{st: s, pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 1)

	for i := 0; i < 2; i++ {
		body, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": "machine-a"})
		req := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		api.activate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("activation %d: expected 200, got %d", i, w.Code)
		}
	}
}

func TestActivateMultiSeatAllowsMultipleMachines(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := testActivationsStore(t)

	api := &activationAPI{st: s, pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 3)

	for _, fp := range []string{"machine-a", "machine-b", "machine-c"} {
		body, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": fp})
		req := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		api.activate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("machine %s: expected 200, got %d", fp, w.Code)
		}
	}

	body, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": "machine-d"})
	req := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	api.activate(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on 4th machine, got %d", w.Code)
	}
}

func TestActivateRejectsInvalidLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	s := testActivationsStore(t)

	api := &activationAPI{st: s, pubs: []ed25519.PublicKey{pub}}

	body, _ := json.Marshal(map[string]string{"license_key": "TOTALLY-FAKE-KEY", "fingerprint": "machine-a"})
	req := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	api.activate(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid license, got %d", w.Code)
	}
}

func TestDeactivateFreesSeatForReactivation(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := testActivationsStore(t)

	api := &activationAPI{st: s, pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 1)

	activate := func(fp string) int {
		body, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": fp})
		req := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		api.activate(w, req)
		return w.Code
	}

	if code := activate("machine-a"); code != http.StatusOK {
		t.Fatalf("initial activation: expected 200, got %d", code)
	}

	body, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": "machine-a"})
	req := httptest.NewRequest("POST", "/license/deactivate", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	api.deactivate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("deactivate: expected 200, got %d", w.Code)
	}

	if code := activate("machine-b"); code != http.StatusOK {
		t.Fatalf("reactivation on new machine: expected 200, got %d", code)
	}
}
