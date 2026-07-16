package main

import (
	"context"
	"fmt"
	"log"

	"github.com/resend/resend-go/v3"
)

var resendAPIKey = requireEnv("RESEND_API_KEY") 

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
	log.Println("resend accepted, id:", sent.Id)
	return nil
}
