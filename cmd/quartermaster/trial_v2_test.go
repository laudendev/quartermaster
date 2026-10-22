package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quartermaster/license"
	"quartermaster/trials"
)

type fakeV2 struct {
	res    trials.ResultV2
	err    error
	gotSig []trials.Signal
	gotNow int64
	gotDay int64
	calls  int
}

func (f *fakeV2) Start(product string, sigs []trials.Signal, now, days int64) (trials.ResultV2, error) {
	f.calls++
	f.gotSig, f.gotNow, f.gotDay = sigs, now, days
	return f.res, f.err
}

func hexV2(c byte) string { return strings.Repeat(string("0123456789abcdef"[c%16]), 64) }

func v2API(f *fakeV2) (*trialV2API, ed25519.PublicKey) {
	priv := ed25519.NewKeyFromSeed(make([]byte, 32))
	return &trialV2API{st: f, key: priv, now: func() time.Time { return time.Unix(5000, 0) }},
		priv.Public().(ed25519.PublicKey)
}

func postV2(api *trialV2API, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	api.start(rec, httptest.NewRequest("POST", "/v2/trial/start", strings.NewReader(body)))
	return rec
}

func TestV2StartIssuesATokenBoundToTheSignalsSent(t *testing.T) {
	f := &fakeV2{res: trials.ResultV2{Record: trials.RecordV2{ID: 7, StartedAt: 1000, EndsAt: 1000 + 14*86400}, Existed: true}}
	api, pub := v2API(f)
	rec := postV2(api, `{"product":"POPF","signals":[{"kind":"serial","hash":"`+hexV2(2)+`"},{"kind":"uuid","hash":"`+strings.ToUpper(hexV2(1))+`"}]}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var resp trialResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Existing || resp.StartedAt != 1000 || resp.EndsAt != 1000+14*86400 {
		t.Fatalf("%+v", resp)
	}
	tr, err := license.VerifyTrialV2(pub, resp.Token)
	if err != nil {
		t.Fatal(err)
	}
	var a, b [32]byte
	x, _ := hex.DecodeString(hexV2(1))
	y, _ := hex.DecodeString(hexV2(2))
	copy(a[:], x)
	copy(b[:], y)
	if tr.Binding != license.TrialBinding("POPF", [][32]byte{a, b}) || tr.Product != "POPF" {
		t.Fatalf("binding %x", tr.Binding)
	}
	if f.gotNow != 5000 || f.gotDay != 14 || f.gotSig[1].Hash != hexV2(1) {
		t.Fatalf("store saw %+v now %d days %d", f.gotSig, f.gotNow, f.gotDay)
	}
}

func TestV2StartIgnoresAClientStartDate(t *testing.T) {
	f := &fakeV2{res: trials.ResultV2{Record: trials.RecordV2{StartedAt: 5000, EndsAt: 5000 + 14*86400}}}
	api, _ := v2API(f)
	rec := postV2(api, `{"product":"POPF","started_at":1,"signals":[{"kind":"uuid","hash":"`+hexV2(1)+`"}]}`)
	if rec.Code != 200 || f.gotNow != 5000 {
		t.Fatalf("%d now %d", rec.Code, f.gotNow)
	}
}

func TestV2StartRejectsBadRequestsWithoutTouchingTheStore(t *testing.T) {
	f := &fakeV2{}
	api, _ := v2API(f)
	for name, body := range map[string]string{
		"not json":     `nope`,
		"no product":   `{"signals":[{"kind":"uuid","hash":"` + hexV2(1) + `"}]}`,
		"bad product":  `{"product":"XXXX","signals":[{"kind":"uuid","hash":"` + hexV2(1) + `"}]}`,
		"no signals":   `{"product":"POPF","signals":[]}`,
		"bad kind":     `{"product":"POPF","signals":[{"kind":"mac","hash":"` + hexV2(1) + `"}]}`,
		"short hash":   `{"product":"POPF","signals":[{"kind":"uuid","hash":"ab"}]}`,
		"repeat":       `{"product":"POPF","signals":[{"kind":"uuid","hash":"` + hexV2(1) + `"},{"kind":"disk","hash":"` + hexV2(1) + `"}]}`,
		"os and other": `{"product":"POPF","signals":[{"kind":"os","hash":"` + hexV2(1) + `"},{"kind":"disk","hash":"` + hexV2(2) + `"}]}`,
		"too large":    `{"product":"POPF","pad":"` + strings.Repeat("a", 5000) + `"}`,
	} {
		if rec := postV2(api, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if f.calls != 0 {
		t.Fatalf("store called %d times", f.calls)
	}
}

func TestV2StartWithoutTheTrialKeyIs503AndStoreErrorsAre500(t *testing.T) {
	f := &fakeV2{}
	api, _ := v2API(f)
	api.key = nil
	if rec := postV2(api, `{}`); rec.Code != 503 {
		t.Fatal(rec.Code)
	}
	f.err = errors.New("disk full")
	api, _ = v2API(f)
	body := `{"product":"POPF","signals":[{"kind":"uuid","hash":"` + hexV2(1) + `"}]}`
	if rec := postV2(api, body); rec.Code != 500 {
		t.Fatal(rec.Code)
	}
}

func TestV2TokenIsNotAcceptedAsV1(t *testing.T) {
	f := &fakeV2{res: trials.ResultV2{Record: trials.RecordV2{StartedAt: 1, EndsAt: 2}}}
	api, pub := v2API(f)
	rec := postV2(api, `{"product":"POPF","signals":[{"kind":"uuid","hash":"`+hexV2(1)+`"}]}`)
	var resp trialResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, err := license.VerifyTrial(pub, resp.Token); err != license.ErrTrialVersion {
		t.Fatal(err)
	}
}
