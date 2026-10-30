package main

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

type sent struct{ subjects []string }

func (s *sent) send(subject, body string) error {
	s.subjects = append(s.subjects, subject)
	return nil
}

func TestHeartbeatQuietWithinGraceAfterStart(t *testing.T) {
	start := time.Now()
	var s sent
	st := &heartbeatState{}
	checkHeartbeat(&pollTracker{}, start.Add(9*time.Minute), start, 10*time.Minute, st, s.send)
	if len(s.subjects) != 0 {
		t.Fatalf("alerted too early: %v", s.subjects)
	}
}

func TestHeartbeatAlertsOnceThenRecovers(t *testing.T) {
	start := time.Now()
	tr := &pollTracker{}
	var s sent
	st := &heartbeatState{}
	checkHeartbeat(tr, start.Add(11*time.Minute), start, 10*time.Minute, st, s.send)
	checkHeartbeat(tr, start.Add(12*time.Minute), start, 10*time.Minute, st, s.send)
	if len(s.subjects) != 1 {
		t.Fatalf("want exactly one alert, got %v", s.subjects)
	}
	tr.touch(start.Add(13 * time.Minute))
	checkHeartbeat(tr, start.Add(13*time.Minute), start, 10*time.Minute, st, s.send)
	if len(s.subjects) != 2 || s.subjects[1] != "Quartermaster: signer is polling again" {
		t.Fatalf("want a recovery notice, got %v", s.subjects)
	}
	checkHeartbeat(tr, start.Add(30*time.Minute), start, 10*time.Minute, st, s.send)
	if len(s.subjects) != 3 {
		t.Fatalf("want a new alert for a new outage, got %v", s.subjects)
	}
}

func TestHeartbeatRetriesAFailedSend(t *testing.T) {
	start := time.Now()
	calls := 0
	st := &heartbeatState{}
	failing := func(string, string) error { calls++; return errors.New("smtp down") }
	checkHeartbeat(&pollTracker{}, start.Add(11*time.Minute), start, 10*time.Minute, st, failing)
	checkHeartbeat(&pollTracker{}, start.Add(12*time.Minute), start, 10*time.Minute, st, failing)
	if calls != 2 || st.alerted {
		t.Fatalf("calls=%d alerted=%v; want a retry and no alerted flag", calls, st.alerted)
	}
}

func TestWaitRecordsASignerPoll(t *testing.T) {
	tr := &pollTracker{}
	api := &queueAPI{st: testQueueStore(t), waitTimeout: 50 * time.Millisecond, polls: tr}
	api.wait(httptest.NewRecorder(), httptest.NewRequest("GET", "/queue/wait", nil))
	if _, ok := tr.lastPoll(); !ok {
		t.Fatal("wait did not record the poll")
	}
}
