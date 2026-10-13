package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quartermaster/license"
)

func postDownload(api *activationAPI, key, fingerprint, platform string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{
		"license_key": key,
		"fingerprint": fingerprint,
		"platform":    platform,
	})
	req := httptest.NewRequest("POST", "/license/download", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	api.download(w, req)
	return w
}

func withPlatformPaths(t *testing.T, m map[string]map[string]string) {
	t.Helper()
	old := platformPaths
	platformPaths = m
	t.Cleanup(func() { platformPaths = old })
}

func TestDownloadServesTheBuildForThePlatform(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "App-setup.exe")
	deb := filepath.Join(dir, "app_amd64.deb")
	if err := os.WriteFile(exe, []byte("windows build"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deb, []byte("linux build"), 0o600); err != nil {
		t.Fatal(err)
	}
	withPlatformPaths(t, map[string]map[string]string{
		"BOOK": {"windows": exe, "linux": deb},
	})

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	api := &activationAPI{st: testActivationsStore(t), pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 1)

	w := postDownload(api, key, "machine-a", "windows")
	if w.Code != http.StatusOK || w.Body.String() != "windows build" {
		t.Fatalf("windows: got %d %q", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "App-setup.exe") {
		t.Fatalf("Content-Disposition = %q, want the installer's file name", cd)
	}

	w = postDownload(api, key, "machine-a", "linux")
	if w.Code != http.StatusOK || w.Body.String() != "linux build" {
		t.Fatalf("linux: got %d %q", w.Code, w.Body.String())
	}
}

func TestDownloadFallsBackToTheDefaultFile(t *testing.T) {
	withPlatformPaths(t, map[string]map[string]string{
		"BOOK": {"windows": "/nonexistent/App-setup.exe"},
	})
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	api := &activationAPI{st: testActivationsStore(t), pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 1)

	for _, platform := range []string{"", "freebsd"} {
		w := postDownload(api, key, "machine-a", platform)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "placeholder product contents") {
			t.Fatalf("platform %q: got %d %q", platform, w.Code, w.Body.String())
		}
	}
}

func TestDownloadMissingFileDoesNotConsumeASeat(t *testing.T) {
	withPlatformPaths(t, map[string]map[string]string{
		"BOOK": {"windows": filepath.Join(t.TempDir(), "gone.exe")},
	})
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := testActivationsStore(t)
	api := &activationAPI{st: s, pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 1)
	l, err := license.VerifyAny([]ed25519.PublicKey{pub}, key)
	if err != nil {
		t.Fatal(err)
	}

	w := postDownload(api, key, "machine-a", "windows")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for a missing file, got %d", w.Code)
	}
	n, err := s.CountActivations(licenseIDString(l))
	if err != nil || n != 0 {
		t.Fatalf("a failed download consumed a seat: count=%d err=%v", n, err)
	}
	if w := postDownload(api, key, "machine-a", ""); w.Code != http.StatusOK {
		t.Fatalf("the seat should still be free, got %d", w.Code)
	}
}

func TestDownloadUnmappedProductDoesNotConsumeASeat(t *testing.T) {
	old := productPaths
	productPaths = map[string]string{}
	t.Cleanup(func() { productPaths = old })

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := testActivationsStore(t)
	api := &activationAPI{st: s, pubs: []ed25519.PublicKey{pub}}
	key := testLicenseKey(t, priv, 1)
	l, err := license.VerifyAny([]ed25519.PublicKey{pub}, key)
	if err != nil {
		t.Fatal(err)
	}

	w := postDownload(api, key, "machine-a", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for an unmapped product, got %d", w.Code)
	}
	n, err := s.CountActivations(licenseIDString(l))
	if err != nil || n != 0 {
		t.Fatalf("a failed download consumed a seat: count=%d err=%v", n, err)
	}
}
