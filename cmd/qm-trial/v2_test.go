package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"quartermaster/trials"
)

type fakeStoreV2 struct {
	recs   map[int64]trials.RecordV2
	legacy int64
	err    error
}

func (f *fakeStoreV2) GetV2(id int64) (trials.RecordV2, bool, error) {
	r, ok := f.recs[id]
	return r, ok, f.err
}
func (f *fakeStoreV2) ListV2(product string, limit int) ([]trials.RecordV2, error) {
	var out []trials.RecordV2
	for _, r := range f.recs {
		out = append(out, r)
	}
	return out, f.err
}
func (f *fakeStoreV2) ResetV2(product string, id int64) (int64, error) {
	if _, ok := f.recs[id]; !ok {
		return 0, f.err
	}
	delete(f.recs, id)
	return 1, f.err
}
func (f *fakeStoreV2) CountLegacy(string) (int64, error) { return f.legacy, f.err }
func (f *fakeStoreV2) PurgeLegacy(string) (int64, error) {
	n := f.legacy
	f.legacy = 0
	return n, f.err
}

func newFakeV2() *fakeStoreV2 {
	return &fakeStoreV2{recs: map[int64]trials.RecordV2{
		3: {ID: 3, Product: "POPF", StartedAt: 1_000, EndsAt: 1_000 + 14*86_400, CreatedAt: 1_000, Signals: 3},
	}, legacy: 7}
}

func doV2(st storeV2, args ...string) (string, error) {
	var out bytes.Buffer
	err := runV2(st, "POPF", args, time.Unix(2_000, 0), &out)
	return out.String(), err
}

func TestIsV2Command(t *testing.T) {
	for _, a := range []string{"list2", "show2", "reset2", "purge-v1", "help"} {
		if !isV2Command([]string{a}) {
			t.Errorf("%s not routed", a)
		}
	}
	for _, a := range [][]string{nil, {"list"}, {"show", "x"}, {"reset", "x"}} {
		if isV2Command(a) {
			t.Errorf("%v routed to v2", a)
		}
	}
}

func TestList2ShowAndReset(t *testing.T) {
	st := newFakeV2()
	out, err := doV2(st, "list2")
	if err != nil || !strings.Contains(out, "running") || !strings.Contains(out, "3 signals") {
		t.Fatalf("%q %v", out, err)
	}
	out, _ = doV2(st, "show2", "3")
	if !strings.Contains(out, "id:        3") || !strings.Contains(out, "signals:   3") {
		t.Fatal(out)
	}
	out, _ = doV2(st, "show2", "9")
	if !strings.Contains(out, "no POPF v2 trial 9") {
		t.Fatal(out)
	}
	out, _ = doV2(st, "reset2", "3")
	if !strings.Contains(out, "deleted POPF v2 trial 3") {
		t.Fatal(out)
	}
	out, _ = doV2(st, "reset2", "3")
	if !strings.Contains(out, "nothing deleted") {
		t.Fatal(out)
	}
}

func TestPurgeV1NeedsConfirmation(t *testing.T) {
	st := newFakeV2()
	out, err := doV2(st, "purge-v1")
	if err != nil || st.legacy != 7 || !strings.Contains(out, "7 v1") || !strings.Contains(out, "--yes") {
		t.Fatalf("%q %d %v", out, st.legacy, err)
	}
	if _, err := doV2(st, "purge-v1", "yes"); err == nil || st.legacy != 7 {
		t.Fatal("a typo in the flag must not delete")
	}
	out, _ = doV2(st, "purge-v1", "--yes")
	if !strings.Contains(out, "deleted 7") || st.legacy != 0 {
		t.Fatalf("%q %d", out, st.legacy)
	}
}

func TestBadArgumentsAndErrors(t *testing.T) {
	st := newFakeV2()
	for _, args := range [][]string{{"show2"}, {"show2", "x"}, {"show2", "0"}, {"reset2", "-1"}, {"list2", "0"}, {"list2", "1", "2"}, {"reset2", "1", "2"}} {
		if _, err := doV2(st, args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	st.err = errors.New("boom")
	if _, err := doV2(st, "list2"); err == nil {
		t.Error("store error swallowed")
	}
	out, err := doV2(newFakeV2(), "help")
	if err != nil || !strings.Contains(out, "list2") || !strings.Contains(out, "usage: qm-trial") {
		t.Fatalf("%q %v", out, err)
	}
}
