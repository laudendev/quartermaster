package main

import (
	"log"
	"time"

	"quartermaster/queue"
)

// maxEmailAttempts caps how many times we'll retry a failing send before
// giving up automatically — a permanently bad address (typo, closed inbox)
// shouldn't retry forever and burn through Resend's API quota.
const maxEmailAttempts = 5

// runEmailRetryLoop periodically checks for checkout sessions whose
// combined receipt email failed to send (or was never sent — e.g. if
// quartermaster crashed between signing the last item and sending),
// and retries delivery, without silently leaving the customer without
// their license keys.
func runEmailRetryLoop(st *queue.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		ready, err := st.ReadySessions(maxEmailAttempts)
		if err != nil {
			log.Println("email retry: failed to query ready sessions:", err)
			continue
		}
		if len(ready) == 0 {
			continue
		}

		log.Println("email retry: found", len(ready), "session(s) awaiting receipt email")
		for _, rs := range ready {
			if err := sendSessionReceiptEmail(rs.SessionID, rs.Email, rs.Items); err != nil {
				log.Println("email retry: send failed for session", rs.SessionID, "(attempt", rs.Attempts+1, "):", err)
				if rerr := st.RecordSessionEmailAttempt(rs.SessionID); rerr != nil {
					log.Println("email retry: failed to record attempt:", rerr)
				}
				continue
			}
			log.Println("email retry: sent receipt for session", rs.SessionID, "to", rs.Email, "after", rs.Attempts+1, "attempt(s)")
			if merr := st.MarkSessionEmailSent(rs.SessionID); merr != nil {
				log.Println("email retry: failed to mark sent:", merr)
			}
		}
	}
}
