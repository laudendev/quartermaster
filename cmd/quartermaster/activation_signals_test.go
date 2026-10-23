package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func seatAPI(t *testing.T) (*activationAPI, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &activationAPI{st: testActivationsStore(t), pubs: []ed25519.PublicKey{pub}}, priv
}

func seatSigs(ns ...int) []map[string]string {
	out := make([]map[string]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, map[string]string{"kind": "uuid", "hash": fmt.Sprintf("%064x", n)})
	}
	return out
}

func seatPost(t *testing.T, h http.HandlerFunc, body any) int {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(b)))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code
}

func seatReq(key, fp string, sigs []map[string]string) map[string]any {
	m := map[string]any{"license_key": key, "fingerprint": fp}
	if sigs != nil {
		m["signals"] = sigs
	}
	return m
}

func TestSeatSignalsReinstallKeepsTheSeat(t *testing.T) {
	a, priv := seatAPI(t)
	key := testLicenseKey(t, priv, 1)
	if c := seatPost(t, a.activate, seatReq(key, "fp-old", seatSigs(1, 2, 3))); c != 200 {
		t.Fatalf("first activation: %d", c)
	}
	if c := seatPost(t, a.activate, seatReq(key, "fp-new", seatSigs(1, 2, 9))); c != 200 {
		t.Fatalf("reinstall on the same hardware must keep the seat: %d", c)
	}
	if c := seatPost(t, a.activate, seatReq(key, "fp-other", seatSigs(7, 8))); c != 409 {
		t.Fatalf("a different computer must not take the seat: %d", c)
	}
}

func TestSeatSignalsLegacyRequestsStillWork(t *testing.T) {
	a, priv := seatAPI(t)
	key := testLicenseKey(t, priv, 1)
	if c := seatPost(t, a.activate, seatReq(key, "fp-a", nil)); c != 200 {
		t.Fatalf("%d", c)
	}
	if c := seatPost(t, a.activate, seatReq(key, "fp-a", nil)); c != 200 {
		t.Fatalf("idempotent: %d", c)
	}
	if c := seatPost(t, a.activate, seatReq(key, "fp-b", nil)); c != 409 {
		t.Fatalf("%d", c)
	}
}

func TestSeatSignalsBadSignalsAreRejected(t *testing.T) {
	a, priv := seatAPI(t)
	key := testLicenseKey(t, priv, 1)
	bad := []map[string]string{{"kind": "nope", "hash": "zz"}}
	if c := seatPost(t, a.activate, seatReq(key, "fp-a", bad)); c != 400 {
		t.Fatalf("%d", c)
	}
	if c := seatPost(t, a.deactivate, seatReq(key, "fp-a", bad)); c != 400 {
		t.Fatalf("%d", c)
	}
}

func TestSeatSignalsDeactivateByHardware(t *testing.T) {
	a, priv := seatAPI(t)
	key := testLicenseKey(t, priv, 1)
	seatPost(t, a.activate, seatReq(key, "fp-old", seatSigs(1, 2)))
	// The fingerprint changed since activation; the hardware did not.
	if c := seatPost(t, a.deactivate, seatReq(key, "fp-new", seatSigs(1))); c != 200 {
		t.Fatalf("%d", c)
	}
	if c := seatPost(t, a.activate, seatReq(key, "fp-other", seatSigs(7))); c != 200 {
		t.Fatalf("the freed seat should be available: %d", c)
	}
}

func TestSeatSignalsTwoKeysWithOneFingerprintDoNotCollide(t *testing.T) {
	// Cloned-image laptops share the old fingerprint; each owner has their own key.
	a, priv := seatAPI(t)
	keyA := testLicenseKey(t, priv, 1)
	keyB := testLicenseKey(t, priv, 1)
	if c := seatPost(t, a.activate, seatReq(keyA, "same-fp", seatSigs(1, 2))); c != 200 {
		t.Fatalf("%d", c)
	}
	if c := seatPost(t, a.activate, seatReq(keyB, "same-fp", seatSigs(3, 4))); c != 200 {
		t.Fatalf("%d", c)
	}
}
