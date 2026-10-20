package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"quartermaster/trials"
)

type fake struct {
	recs map[string]trials.Record
}

func (f *fake) Get(p, fp string) (trials.Record, bool, error) {
	r, ok := f.recs[p+fp]
	return r, ok, nil
}

func (f *fake) Reset(p, fp string) (int64, error) {
	if _, ok := f.recs[p+fp]; !ok {
		return 0, nil
	}
	delete(f.recs, p+fp)
	return 1, nil
}

func (f *fake) List(p string, n int) ([]trials.Record, error) {
	var out []trials.Record
	for _, r := range f.recs {
		if r.Product == p && len(out) < n {
			out = append(out, r)
		}
	}
	return out, nil
}

var fp = strings.Repeat("ab", 32)
var now = time.Unix(1_000_000, 0)

func newFake() *fake {
	return &fake{recs: map[string]trials.Record{
		"POPF" + fp: {Product: "POPF", Fingerprint: fp, StartedAt: 1_000_000 - 100, EndsAt: 1_000_000 + 100, CreatedAt: 1_000_000 - 100},
	}}
}

func do(f *fake, args ...string) (string, error) {
	var b bytes.Buffer
	err := run(f, "POPF", args, now, &b)
	return b.String(), err
}

func TestShowReportsStatus(t *testing.T) {
	out, err := do(newFake(), "show", fp)
	if err != nil || !strings.Contains(out, "running") || !strings.Contains(out, fp) {
		t.Fatalf("%q %v", out, err)
	}
	f := newFake()
	r := f.recs["POPF"+fp]
	r.EndsAt = 1_000_000 - 1
	f.recs["POPF"+fp] = r
	out, _ = do(f, "show", strings.ToUpper(fp))
	if !strings.Contains(out, "ended") {
		t.Fatalf("%q", out)
	}
}

func TestShowMissing(t *testing.T) {
	out, err := do(newFake(), "show", strings.Repeat("cd", 32))
	if err != nil || !strings.Contains(out, "no POPF trial") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestResetDeletesOnce(t *testing.T) {
	f := newFake()
	out, err := do(f, "reset", fp)
	if err != nil || !strings.Contains(out, "deleted") || len(f.recs) != 0 {
		t.Fatalf("%q %v", out, err)
	}
	out, _ = do(f, "reset", fp)
	if !strings.Contains(out, "nothing deleted") {
		t.Fatalf("%q", out)
	}
}

func TestBadInput(t *testing.T) {
	for _, args := range [][]string{{"show", "xyz"}, {"show", fp[:63]}, {"list", "0"}, {"list", "x"}} {
		if _, err := do(newFake(), args...); err == nil || errors.Is(err, errUsage) {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{{}, {"show"}, {"reset", fp, "x"}, {"nope"}, {"list", "1", "2"}} {
		if _, err := do(newFake(), args...); !errors.Is(err, errUsage) {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestListShortensFingerprints(t *testing.T) {
	out, err := do(newFake(), "list")
	if err != nil || !strings.HasPrefix(out, fp[:12]+"  running") || strings.Contains(out, fp[:13]) {
		t.Fatalf("%q %v", out, err)
	}
}
