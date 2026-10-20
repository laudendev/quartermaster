package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quartermaster/license"
	"quartermaster/trials"
)

type fakeTrials struct {
	recs map[string]trials.Record
}

func (f *fakeTrials) Start(product, fp string, started, ends, now int64) (trials.Record, bool, error) {
	if f.recs == nil {
		f.recs = map[string]trials.Record{}
	}
	if rec, ok := f.recs[product+fp]; ok {
		return rec, true, nil
	}
	rec := trials.Record{Product: product, Fingerprint: fp, StartedAt: started, EndsAt: ends, CreatedAt: now}
	f.recs[product+fp] = rec
	return rec, false, nil
}

const testFP = "a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebf"

func newTrialAPI(now int64) (*trialAPI, ed25519.PublicKey) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	return &trialAPI{
		st:  &fakeTrials{},
		key: priv,
		now: func() time.Time { return time.Unix(now, 0) },
	}, priv.Public().(ed25519.PublicKey)
}

func post(t *trialAPI, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	t.start(rr, httptest.NewRequest(http.MethodPost, "/trial/start", strings.NewReader(body)))
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) trialResponse {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp trialResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestTrialStartIssuesAVerifiableFourteenDayToken(t *testing.T) {
	api, pub := newTrialAPI(1_000_000)
	resp := decode(t, post(api, `{"product":"POPF","fingerprint":"`+testFP+`"}`))
	if resp.Existing || resp.StartedAt != 1_000_000 || resp.EndsAt != 1_000_000+14*86_400 {
		t.Fatalf("unexpected %+v", resp)
	}
	trial, err := license.VerifyTrial(pub, resp.Token)
	if err != nil {
		t.Fatal(err)
	}
	if trial.Product != "POPF" || trial.StartedAt != resp.StartedAt || trial.EndsAt != resp.EndsAt {
		t.Fatalf("token says %+v", trial)
	}
}

func TestTrialStartTwiceRestoresTheOriginalTrial(t *testing.T) {
	api, _ := newTrialAPI(1_000_000)
	first := decode(t, post(api, `{"product":"POPF","fingerprint":"`+testFP+`"}`))
	api.now = func() time.Time { return time.Unix(9_000_000, 0) }
	again := decode(t, post(api, `{"product":"POPF","fingerprint":"`+strings.ToUpper(testFP)+`"}`))
	if !again.Existing || again.StartedAt != first.StartedAt || again.EndsAt != first.EndsAt {
		t.Fatalf("first %+v, again %+v", first, again)
	}
}

func TestClaimedStartCanOnlyShortenTheTrial(t *testing.T) {
	cases := []struct {
		claim, want int64
	}{
		{99_000_000, 99_000_000},   // a local trial that began earlier: honoured
		{200_000_000, 100_000_000}, // in the future: ignored
		{-5, 100_000_000},          // nonsense: ignored
		{1, 100_000_000},           // older than a year: ignored
	}
	for _, c := range cases {
		api, _ := newTrialAPI(100_000_000)
		resp := decode(t, post(api, `{"product":"POPF","fingerprint":"`+testFP+`","started_at":`+itoa(c.claim)+`}`))
		if resp.StartedAt != c.want {
			t.Fatalf("claim %d: started %d, want %d", c.claim, resp.StartedAt, c.want)
		}
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestTrialStartRejectsBadRequests(t *testing.T) {
	api, _ := newTrialAPI(1_000_000)
	for _, body := range []string{
		`not json`,
		`{"product":"LAMP","fingerprint":"` + testFP + `"}`,
		`{"product":"POPF","fingerprint":"abc"}`,
		`{"product":"POPF","fingerprint":"` + strings.Repeat("zz", 32) + `"}`,
		`{"product":"POPF"}`,
	} {
		if rr := post(api, body); rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", body, rr.Code)
		}
	}
	big := `{"product":"POPF","fingerprint":"` + testFP + `","pad":"` + strings.Repeat("x", 8000) + `"}`
	if rr := post(api, big); rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: status %d", rr.Code)
	}
}

func TestTrialStartWithoutAKeyIsUnavailable(t *testing.T) {
	api, _ := newTrialAPI(1_000_000)
	api.key = nil
	if rr := post(api, `{"product":"POPF","fingerprint":"`+testFP+`"}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestLoadTrialKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trial.key")
	os.WriteFile(path, []byte(strings.Repeat("07", 32)+"\n"), 0o600)
	k, err := loadTrialKey(path)
	if err != nil || !bytes.Equal(k.Seed(), bytes.Repeat([]byte{7}, 32)) {
		t.Fatalf("key %x err %v", k.Seed(), err)
	}
	os.WriteFile(path, []byte("abcd"), 0o600)
	if _, err := loadTrialKey(path); err == nil {
		t.Fatal("short key accepted")
	}
}
