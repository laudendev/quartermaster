package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/resend/resend-go/v3"
)

// sendOpsAlert emails the operator. It is a variable so tests can replace it.
// Alerts are only logged unless ALERT_EMAIL is set in the environment.
var sendOpsAlert = func(subject, body string) error {
	to := strings.TrimSpace(os.Getenv("ALERT_EMAIL"))
	if to == "" {
		log.Println("ops alert not sent (ALERT_EMAIL unset):", subject)
		return nil
	}
	client := resend.NewClient(getResendAPIKey())
	_, err := client.Emails.SendWithContext(context.Background(), &resend.SendEmailRequest{
		From:    "licenses@lauden.dev",
		To:      []string{to},
		Subject: subject,
		Text:    body,
	})
	if err != nil {
		return fmt.Errorf("resend alert failed: %w", err)
	}
	return nil
}

// alertUnfulfilled tells the operator that a completed Stripe checkout did
// not produce a license, so a charged customer can be refunded or fixed.
// A failure to send the alert is logged and never fails the webhook.
func alertUnfulfilled(reason, sessionID, email, detail string) {
	subject := "Quartermaster: checkout not fulfilled (" + reason + ")"
	body := fmt.Sprintf("%s\n\nStripe session: %s\nCustomer email: %s\nDetail: %s\n\nNo license was issued. If the customer was charged, refund or fix it in the Stripe Dashboard.\n",
		reason, sessionID, email, detail)
	if err := sendOpsAlert(subject, body); err != nil {
		log.Println("ops alert failed:", err)
	}
}
