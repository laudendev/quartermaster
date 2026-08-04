package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/resend/resend-go/v3"
	"quartermaster/queue"
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
			AmountTotal int64  `json:"amount_total"`
			Price       struct {
				ID string `json:"id"`
			} `json:"price"`
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

// receiptLineItem is one product's row in the combined receipt email.
type receiptLineItem struct {
	ProductName string
	AmountLine  string
	LicenseKey  string
}

// sendSessionReceiptEmail sends one combined receipt for an entire
// checkout session, listing every purchased product with its own price
// and license key, plus the session total.
func sendSessionReceiptEmail(sessionID, to string, items []queue.SessionItem) error {
	details, err := fetchSessionDetails(sessionID)
	if err != nil {
		// Don't fail the whole email over a receipt-detail lookup issue —
		// the license keys are what actually matter to the customer.
		log.Println("fetch stripe session details failed, sending without per-item pricing:", err)
		details = &stripeSessionDetails{}
	}

	// Index Stripe's line items by exact Price ID for precise per-item
	// matching — avoids any ambiguity from similar/duplicate descriptions.
	amountByPriceID := make(map[string]string)
	for _, li := range details.LineItems.Data {
		amountByPriceID[li.Price.ID] = formatAmount(li.AmountTotal, details.Currency)
	}

	receiptItems := make([]receiptLineItem, 0, len(items))
	for _, item := range items {
		receiptItems = append(receiptItems, receiptLineItem{
			ProductName: item.Product,
			AmountLine:  amountByPriceID[item.PriceID],
			LicenseKey:  item.LicenseKey,
		})
	}

	totalLine := ""
	if details.AmountTotal > 0 {
		totalLine = formatAmount(details.AmountTotal, details.Currency)
	}

	purchaseDate := time.Now().Format("January 2, 2006")
	subject := "Your receipt and license key"
	if len(receiptItems) > 1 {
		subject = fmt.Sprintf("Your receipt and %d license keys", len(receiptItems))
	}

	client := resend.NewClient(getResendAPIKey())
	params := &resend.SendEmailRequest{
		From:    "licenses@lauden.dev",
		To:      []string{to},
		ReplyTo: "tlauden@duck.com", // your real inbox
		Subject: subject,
		Html:    buildReceiptHTML(receiptItems, totalLine, purchaseDate),
		Text:    buildReceiptText(receiptItems, totalLine, purchaseDate),
	}

	ctx := context.Background()
	sent, err := client.Emails.SendWithContext(ctx, params)
	if err != nil {
		return fmt.Errorf("resend send failed: %w", err)
	}
	log.Println("resend accepted, id:", sent.Id)
	return nil
}

func buildReceiptText(items []receiptLineItem, totalLine, purchaseDate string) string {
	var b strings.Builder
	b.WriteString("Thank you for your purchase!\n\n")
	b.WriteString("Order summary\n")
	b.WriteString("-------------\n")
	for _, item := range items {
		b.WriteString(fmt.Sprintf("Product: %s\n", item.ProductName))
		if item.AmountLine != "" {
			b.WriteString(fmt.Sprintf("Price: %s\n", item.AmountLine))
		}
		b.WriteString(fmt.Sprintf("License key: %s\n\n", item.LicenseKey))
	}
	if totalLine != "" {
		b.WriteString(fmt.Sprintf("Total paid: %s\n", totalLine))
	}
	b.WriteString(fmt.Sprintf("Date: %s\n\n", purchaseDate))
	b.WriteString("Keep these keys safe — each is tied to its product and you'll need it to activate your software.\n\n")
	b.WriteString("Questions? Just reply to this email.\n\n")
	b.WriteString("— Tyler L. Laudenslager, lauden.dev\n")
	return b.String()
}

func buildReceiptHTML(items []receiptLineItem, totalLine, purchaseDate string) string {
	var itemsHTML strings.Builder
	for _, item := range items {
		priceRow := ""
		if item.AmountLine != "" {
			priceRow = fmt.Sprintf(`
					<tr>
						<td style="padding:2px 0;color:#6B7280;font-family:'JetBrains Mono',monospace;font-size:13px;">Price</td>
						<td style="padding:2px 0;color:#111827;font-family:'JetBrains Mono',monospace;font-size:13px;text-align:right;">%s</td>
					</tr>`, item.AmountLine)
		}
		itemsHTML.WriteString(fmt.Sprintf(`
				<tr>
					<td colspan="2" style="padding:16px 0 4px;">
						<table width="100%%" cellpadding="0" cellspacing="0">
							<tr>
								<td style="padding:2px 0;color:#111827;font-family:'JetBrains Mono',monospace;font-size:13px;font-weight:600;">%s</td>
							</tr>%s
						</table>
						<div style="background:#FDF8F0;border:1px solid #EBE4D6;border-radius:10px;padding:12px 14px;margin-top:8px;font-family:'JetBrains Mono',monospace;font-size:12px;color:#111827;word-break:break-all;line-height:1.5;">
							%s
						</div>
					</td>
				</tr>`, item.ProductName, priceRow, item.LicenseKey))
	}

	totalRow := ""
	if totalLine != "" {
		totalRow = fmt.Sprintf(`
					<tr>
						<td style="padding:6px 0;color:#6B7280;font-family:'JetBrains Mono',monospace;font-size:14px;font-weight:600;">Total paid</td>
						<td style="padding:6px 0;color:#111827;font-family:'JetBrains Mono',monospace;font-size:14px;text-align:right;font-weight:600;">%s</td>
					</tr>`, totalLine)
	}

	licenseNote := "Keep this safe — it's tied to your product and you'll need it to activate your software."
	if len(items) > 1 {
		licenseNote = "Keep these safe — each key is tied to its own product and you'll need it to activate that software."
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
							<p style="color:#6B7280;font-size:14px;margin:0;">Here's your receipt and license key%s.</p>
						</td>
					</tr>
					<tr>
						<td style="padding:0 32px;">
							<table width="100%%" cellpadding="0" cellspacing="0" style="border-top:1px solid #EBE4D6;padding-bottom:8px;">%s
							</table>
							<table width="100%%" cellpadding="0" cellspacing="0" style="border-top:1px solid #EBE4D6;padding:16px 0;">%s
								<tr>
									<td style="padding:6px 0;color:#6B7280;font-family:'JetBrains Mono',monospace;font-size:14px;">Date</td>
									<td style="padding:6px 0;color:#111827;font-family:'JetBrains Mono',monospace;font-size:14px;text-align:right;">%s</td>
								</tr>
							</table>
						</td>
					</tr>
					<tr>
						<td style="padding:8px 32px 8px;">
							<p style="color:#6B7280;font-size:13px;margin:0;">%s</p>
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
</html>`, pluralSuffix(len(items)), itemsHTML.String(), totalRow, purchaseDate, licenseNote)
}

// pluralSuffix returns "s" when n != 1, for simple inline pluralization
// like "license key" vs "license keys".
func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
