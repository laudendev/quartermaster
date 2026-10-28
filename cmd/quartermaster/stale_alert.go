package main

import (
	"fmt"
	"log"
	"time"

	"quartermaster/queue"
)

// staleAfter is how long a paid order may wait for the signer before the
// operator is told. One long-poll timeout plus a retry is well under a minute.
const staleAfter = 5 * time.Minute

// staleSource lists pending sign requests older than a cutoff.
type staleSource interface {
	StalePending(cutoff time.Time) ([]queue.StaleRequest, error)
}

// checkStale emails the operator once per request that has waited longer
// than threshold. alerted remembers requests already reported and is pruned
// of requests that are no longer stale. A failed send is retried next tick.
// Only request IDs and ages are logged or mailed, never customer data.
func checkStale(src staleSource, now time.Time, threshold time.Duration, alerted map[string]bool) {
	stale, err := src.StalePending(now.Add(-threshold))
	if err != nil {
		log.Println("stale check failed:", err)
		return
	}
	current := make(map[string]bool, len(stale))
	for _, r := range stale {
		current[r.ID] = true
		if alerted[r.ID] {
			continue
		}
		subject := "Quartermaster: order waiting for a signature"
		body := fmt.Sprintf("Request %s has been waiting %s for the signer.\n\n"+
			"On the signer machine check: is it awake, is the wg-qmaster tunnel up "+
			"(sudo wg show), is signer.service running (systemctl status signer)?\n",
			r.ID, now.Sub(r.Created).Round(time.Second))
		if err := sendOpsAlert(subject, body); err != nil {
			log.Println("ops alert failed:", err)
			continue
		}
		alerted[r.ID] = true
	}
	for id := range alerted {
		if !current[id] {
			delete(alerted, id)
		}
	}
}

// runStaleAlertLoop checks for stalled orders every interval, forever.
func runStaleAlertLoop(src staleSource, every time.Duration) {
	alerted := map[string]bool{}
	for range time.Tick(every) {
		checkStale(src, time.Now(), staleAfter, alerted)
	}
}
