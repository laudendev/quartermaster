package main

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// heartbeatAfter is how long the signer may stay silent before the operator
// is told. The signer long-polls every ~55s, so ten minutes is generous.
const heartbeatAfter = 10 * time.Minute

// pollTracker records when the signer last called /queue/wait.
type pollTracker struct{ last atomic.Int64 } // unix nanoseconds, 0 = never

func (p *pollTracker) touch(now time.Time) { p.last.Store(now.UnixNano()) }

func (p *pollTracker) lastPoll() (time.Time, bool) {
	n := p.last.Load()
	if n == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}

type heartbeatState struct{ alerted bool }

// checkHeartbeat alerts once when the signer has been silent longer than
// threshold, and once more when polling resumes. Silence is measured from
// the later of the last poll and started, so a server restart gets a full
// threshold of grace. A failed send is retried on the next tick.
func checkHeartbeat(tr *pollTracker, now, started time.Time, threshold time.Duration,
	st *heartbeatState, send func(subject, body string) error) {
	ref := started
	if last, ok := tr.lastPoll(); ok && last.After(ref) {
		ref = last
	}
	silence := now.Sub(ref)
	switch {
	case silence > threshold && !st.alerted:
		body := fmt.Sprintf("The signer has not polled the queue for %s. Orders will wait until it is back.\n\n"+
			"On the signer droplet (ssh qm-signer) check: systemctl status qm-signer, "+
			"and sudo wg show for the wg-qmaster tunnel.\n", silence.Round(time.Second))
		if err := send("Quartermaster: signer has stopped polling", body); err != nil {
			log.Println("heartbeat alert failed:", err)
			return
		}
		st.alerted = true
	case silence <= threshold && st.alerted:
		if err := send("Quartermaster: signer is polling again", "The signer is polling the queue again.\n"); err != nil {
			log.Println("heartbeat recovery notice failed:", err)
			return
		}
		st.alerted = false
	}
}

// runHeartbeatLoop checks the signer's heartbeat every interval, forever.
func runHeartbeatLoop(tr *pollTracker, every time.Duration) {
	started := time.Now()
	st := &heartbeatState{}
	for range time.Tick(every) {
		checkHeartbeat(tr, time.Now(), started, heartbeatAfter, st, sendOpsAlert)
	}
}
