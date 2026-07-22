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

// runEmailRetryLoop periodically checks for signed license requests whose
// email failed to send (or was never sent), and retries delivery. This
// covers the case where sendLicenseEmail failed inline at signing time —
// for example, if quartermaster crashed or Resend/Stripe were briefly
// unreachable — without silently leaving the customer without their key.
func runEmailRetryLoop(st *queue.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		pending, err := st.PendingEmails(maxEmailAttempts)
		if err != nil {
			log.Println("email retry: failed to query pending emails:", err)
			continue
		}
		if len(pending) == 0 {
			continue
		}

		log.Println("email retry: found", len(pending), "unsent license email(s)")
		for _, p := range pending {
			if err := sendLicenseEmail(p.TxnID, p.Email, p.LicenseKey); err != nil {
				log.Println("email retry: send failed for", p.Email, "(attempt", p.Attempts+1, "):", err)
				if rerr := st.RecordEmailAttempt(p.ID); rerr != nil {
					log.Println("email retry: failed to record attempt:", rerr)
				}
				continue
			}
			log.Println("email retry: sent to", p.Email, "after", p.Attempts+1, "attempt(s)")
			if merr := st.MarkEmailSent(p.ID); merr != nil {
				log.Println("email retry: failed to mark sent:", merr)
			}
		}
	}
}
