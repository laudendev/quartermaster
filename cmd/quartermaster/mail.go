package main

import (
	"context"
	"fmt"

	"github.com/resend/resend-go/v3"
)

const resendAPIKey = "REDACTED" // dev key

func sendLicenseEmail(to, licenseKey string) error {
	client := resend.NewClient(resendAPIKey)
	params := &resend.SendEmailRequest{
		From:    "licenses@lauden.dev",
		To:      []string{to},
		ReplyTo: "tlauden@duck.com", // your real inbox
		Subject: "Your license key",
		Text:    fmt.Sprintf("Thanks for your purchase. Your license key:\n\n%s\n\nKeep this safe — it's tied to your product.", licenseKey),
	}

	ctx := context.Background()
	sent, err := client.Emails.SendWithContext(ctx, params)
	if err != nil {
		return fmt.Errorf("resend send failed: %w", err)
	}
	_ = sent // sent.Id available if you want to log it
	return nil
}
