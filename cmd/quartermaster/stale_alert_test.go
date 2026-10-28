package main

import (
	"errors"
	"testing"
	"time"

	"quartermaster/queue"
)

type fakeStaleSource struct {
	reqs []queue.StaleRequest
	err  error
}

func (f *fakeStaleSource) StalePending(time.Time) ([]queue.StaleRequest, error) {
	return f.reqs, f.err
}

func captureStaleAlerts(t *testing.T) *[]string {
	t.Helper()
	var subjects []string
	old := sendOpsAlert
	sendOpsAlert = func(subject, body string) error {
		subjects = append(subjects, subject)
		return nil
	}
	t.Cleanup(func() { sendOpsAlert = old })
	return &subjects
}

func staleReq(id string) queue.StaleRequest {
	return queue.StaleRequest{ID: id, Created: time.Unix(0, 0)}
}

func TestStaleAlertFiresOncePerRequest(t *testing.T) {
	subjects := captureStaleAlerts(t)
	src := &fakeStaleSource{reqs: []queue.StaleRequest{staleReq("r1")}}
	alerted := map[string]bool{}
	now := time.Unix(600, 0)
	checkStale(src, now, 5*time.Minute, alerted)
	checkStale(src, now.Add(time.Minute), 5*time.Minute, alerted)
	if len(*subjects) != 1 {
		t.Fatalf("got %d alerts, want 1", len(*subjects))
	}
}

func TestStaleAlertForgetsSignedRequestsAndAlertsForNewOnes(t *testing.T) {
	subjects := captureStaleAlerts(t)
	src := &fakeStaleSource{reqs: []queue.StaleRequest{staleReq("r1")}}
	alerted := map[string]bool{}
	now := time.Unix(600, 0)
	checkStale(src, now, 5*time.Minute, alerted)
	src.reqs = nil // r1 got signed
	checkStale(src, now, 5*time.Minute, alerted)
	if len(alerted) != 0 {
		t.Fatalf("alerted not pruned: %v", alerted)
	}
	src.reqs = []queue.StaleRequest{staleReq("r2")}
	checkStale(src, now, 5*time.Minute, alerted)
	if len(*subjects) != 2 || !alerted["r2"] {
		t.Fatalf("got %d alerts, alerted=%v", len(*subjects), alerted)
	}
}

func TestStaleAlertRetriesAfterSendFailure(t *testing.T) {
	calls := 0
	old := sendOpsAlert
	sendOpsAlert = func(subject, body string) error {
		calls++
		if calls == 1 {
			return errors.New("resend down")
		}
		return nil
	}
	t.Cleanup(func() { sendOpsAlert = old })
	src := &fakeStaleSource{reqs: []queue.StaleRequest{staleReq("r1")}}
	alerted := map[string]bool{}
	now := time.Unix(600, 0)
	checkStale(src, now, 5*time.Minute, alerted)
	if alerted["r1"] {
		t.Fatal("marked alerted although the send failed")
	}
	checkStale(src, now.Add(time.Minute), 5*time.Minute, alerted)
	if calls != 2 || !alerted["r1"] {
		t.Fatalf("calls=%d alerted=%v", calls, alerted)
	}
}

func TestStaleCheckErrorSendsNothing(t *testing.T) {
	subjects := captureStaleAlerts(t)
	src := &fakeStaleSource{err: errors.New("db locked")}
	checkStale(src, time.Unix(600, 0), 5*time.Minute, map[string]bool{})
	if len(*subjects) != 0 {
		t.Fatalf("got %d alerts, want 0", len(*subjects))
	}
}
