package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/resend/resend-go/v3"
)

var resendAPIKey string

func getResendAPIKey() string {
	if resendAPIKey == "" {
		resendAPIKey = requireEnv("RESEND_API_KEY")
	}
	return resendAPIKey
}

var stripeSecretKeyForEmail string

func getStripeSecretKeyForEmail() string {
	if stripeSecretKeyForEmail == "" {
		stripeSecretKeyForEmail = requireEnv("STRIPE_SECRET_KEY")
	}
	return stripeSecretKeyForEmail
}

// stripeSessionDetails is the minimal shape we need back from Stripe's
// Checkout Session API to build a receipt.
type stripeSessionDetails struct {
	AmountTotal int64  `json:"amount_total"` // cents
	Currency    string `json:"currency"`
	LineItems   struct {
		Data []struct {
			Description string `json:"description"`
		} `json:"data"`
	} `json:"line_items"`
}

// fetchSessionDetails calls Stripe's API directly (no SDK dependency,
// matching this project's existing style) to retrieve the product name
// and amount paid for a completed Checkout Session, for use in the
// purchase receipt email.
func fetchSessionDetails(sessionID string) (*stripeSessionDetails, error) {
	url := fmt.Sprintf("https://api.stripe.com/v1/checkout/sessions/%s?expand[]=line_items", sessionID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build stripe request: %w", err)
	}
	req.SetBasicAuth(getStripeSecretKeyForEmail(), "")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stripe session fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stripe session fetch: unexpected status %d", resp.StatusCode)
	}

	var details stripeSessionDetails
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		return nil, fmt.Errorf("decode stripe session: %w", err)
	}
	return &details, nil
}

func formatAmount(cents int64, currency string) string {
	dollars := float64(cents) / 100
	switch currency {
	case "usd", "":
		return fmt.Sprintf("$%.2f", dollars)
	default:
		return fmt.Sprintf("%.2f %s", dollars, currency)
	}
}

func sendLicenseEmail(sessionID, to, licenseKey string) error {
	productName := "your purchase"
	amountLine := ""

	if details, err := fetchSessionDetails(sessionID); err != nil {
		// Don't fail the whole email over a receipt-detail lookup issue —
		// the license key is what actually matters to the customer.
		log.Println("fetch stripe session details failed, sending without order details:", err)
	} else {
		if len(details.LineItems.Data) > 0 && details.LineItems.Data[0].Description != "" {
			productName = details.LineItems.Data[0].Description
		}
		if details.AmountTotal > 0 {
			amountLine = formatAmount(details.AmountTotal, details.Currency)
		}
	}

	purchaseDate := time.Now().Format("January 2, 2006")

	client := resend.NewClient(getResendAPIKey())
	params := &resend.SendEmailRequest{
		From:    "licenses@lauden.dev",
		To:      []string{to},
		ReplyTo: "tlauden@duck.com", // your real inbox
		Subject: fmt.Sprintf("Your receipt and license key — %s", productName),
		Html:    buildReceiptHTML(productName, amountLine, purchaseDate, licenseKey),
		Text:    buildReceiptText(productName, amountLine, purchaseDate, licenseKey),
	}

	ctx := context.Background()
	sent, err := client.Emails.SendWithContext(ctx, params)
	if err != nil {
		return fmt.Errorf("resend send failed: %w", err)
	}
	log.Println("resend accepted, id:", sent.Id)
	return nil
}

func buildReceiptText(productName, amountLine, purchaseDate, licenseKey string) string {
	amountPart := ""
	if amountLine != "" {
		amountPart = fmt.Sprintf("Amount paid: %s\n", amountLine)
	}
	return fmt.Sprintf(
		"Thank you for your purchase!\n\n"+
			"Order summary\n"+
			"-------------\n"+
			"Product: %s\n"+
			"%s"+
			"Date: %s\n\n"+
			"Your license key:\n\n%s\n\n"+
			"Keep this key safe — it's tied to your product and you'll need it to activate your software.\n\n"+
			"Questions? Just reply to this email.\n\n"+
			"— Tyler L. Laudenslager, lauden.dev\n",
		productName, amountPart, purchaseDate, licenseKey,
	)
}

func buildReceiptHTML(productName, amountLine, purchaseDate, licenseKey string) string {
	amountRow := ""
	if amountLine != "" {
		amountRow = fmt.Sprintf(`
			<tr>
				<td style="padding:6px 0;color:#6B7280;font-family:'JetBrains Mono',monospace;font-size:14px;">Amount paid</td>
				<td style="padding:6px 0;color:#111827;font-family:'JetBrains Mono',monospace;font-size:14px;text-align:right;">%s</td>
			</tr>`, amountLine)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="margin:0;padding:0;background:#FDF8F0;font-family:-apple-system,BlinkMacSystemFont,'Inter',sans-serif;">
	<table width="100%%" cellpadding="0" cellspacing="0" style="background:#FDF8F0;padding:32px 16px;">
		<tr>
			<td align="center">
				<table width="480" cellpadding="0" cellspacing="0" style="background:#FFFFFF;border-radius:16px;overflow:hidden;border:1px solid #EBE4D6;">
				    <tr>
					   <td style="padding:32px 32px 8px;">
					       <img src="https://shop.lauden.dev/static/img/logo-email.png" alt="lauden.dev" width="180" style="display:block;">
						 </td>
					</tr>
					<tr>
						<td style="padding:8px 32px 24px;">
							<h1 style="font-size:22px;color:#111827;margin:16px 0 4px;">Thank you for your purchase!</h1>
							<p style="color:#6B7280;font-size:14px;margin:0;">Here's your receipt and license key.</p>
						</td>
					</tr>
					<tr>
						<td style="padding:0 32px;">
							<table width="100%%" cellpadding="0" cellspacing="0" style="border-top:1px solid #EBE4D6;border-bottom:1px solid #EBE4D6;padding:16px 0;">
								<tr>
									<td style="padding:6px 0;color:#6B7280;font-family:'JetBrains Mono',monospace;font-size:14px;">Product</td>
									<td style="padding:6px 0;color:#111827;font-family:'JetBrains Mono',monospace;font-size:14px;text-align:right;">%s</td>
								</tr>%s
								<tr>
									<td style="padding:6px 0;color:#6B7280;font-family:'JetBrains Mono',monospace;font-size:14px;">Date</td>
									<td style="padding:6px 0;color:#111827;font-family:'JetBrains Mono',monospace;font-size:14px;text-align:right;">%s</td>
								</tr>
							</table>
						</td>
					</tr>
					<tr>
						<td style="padding:24px 32px 8px;">
							<p style="color:#111827;font-size:14px;margin:0 0 8px;font-weight:600;">Your license key</p>
							<div style="background:#FDF8F0;border:1px solid #EBE4D6;border-radius:10px;padding:14px 16px;font-family:'JetBrains Mono',monospace;font-size:13px;color:#111827;word-break:break-all;line-height:1.6;">
								%s
							</div>
							<p style="color:#6B7280;font-size:13px;margin:12px 0 0;">Keep this safe — it's tied to your product and you'll need it to activate your software.</p>
						</td>
					</tr>
					<tr>
						<td style="padding:24px 32px 32px;">
							<p style="color:#6B7280;font-size:13px;margin:0;">Questions? Just reply to this email — a real person (me) will get it.</p>
							<p style="color:#9CA3AF;font-size:12px;margin:16px 0 0;">&copy 2026 Laudenslager Software, LLC. All rights reserved.</p>
						</td>
					</tr>
				</table>
			</td>
		</tr>
	</table>
</body>
</html>`, productName, amountRow, purchaseDate, licenseKey)
}
