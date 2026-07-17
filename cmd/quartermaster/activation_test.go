package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quartermaster/license"
	"quartermaster/store"
)

func testLicenseKey(t *testing.T, priv ed25519.PrivateKey, seats int) string {
	t.Helper()
	id, err := license.NewID()
	if err != nil {
		t.Fatal(err)
	}
	key, err := license.Issue(priv, license.License{
		Product:  "TRCR",
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
	s, _ := store.Open(":memory:")
	defer s.Close()

	api := &activationAPI{st: s, pub: pub}
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
	s, _ := store.Open(":memory:")
	defer s.Close()

	api := &activationAPI{st: s, pub: pub}
	key := testLicenseKey(t, priv, 1) // only 1 seat

	// First activation succeeds.
	body1, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": "machine-a"})
	req1 := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body1)))
	w1 := httptest.NewRecorder()
	api.activate(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first activation should succeed, got %d", w1.Code)
	}

	// Second activation, different machine, same 1-seat license — must fail.
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
	s, _ := store.Open(":memory:")
	defer s.Close()

	api := &activationAPI{st: s, pub: pub}
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
	s, _ := store.Open(":memory:")
	defer s.Close()

	api := &activationAPI{st: s, pub: pub}
	key := testLicenseKey(t, priv, 3) // 3 seats

	for _, fp := range []string{"machine-a", "machine-b", "machine-c"} {
		body, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": fp})
		req := httptest.NewRequest("POST", "/license/activate", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		api.activate(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("machine %s: expected 200, got %d", fp, w.Code)
		}
	}

	// 4th machine on a 3-seat license must fail.
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
	s, _ := store.Open(":memory:")
	defer s.Close()

	api := &activationAPI{st: s, pub: pub}

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
	s, _ := store.Open(":memory:")
	defer s.Close()

	api := &activationAPI{st: s, pub: pub}
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

	// Deactivate machine-a.
	body, _ := json.Marshal(map[string]string{"license_key": key, "fingerprint": "machine-a"})
	req := httptest.NewRequest("POST", "/license/deactivate", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	api.deactivate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("deactivate: expected 200, got %d", w.Code)
	}

	// Now machine-b should be able to activate the freed seat.
	if code := activate("machine-b"); code != http.StatusOK {
		t.Fatalf("reactivation on new machine: expected 200, got %d", code)
	}
}
