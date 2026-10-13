package main

import (
	"strings"
	"testing"
)

func TestReceiptsExplainActivation(t *testing.T) {
	items := []receiptLineItem{{ProductName: "Pop Focus", AmountLine: "$16.00", LicenseKey: "KEY123"}}
	bodies := map[string]string{
		"text": buildReceiptText(items, "", "", "January 2, 2026"),
		"html": buildReceiptHTML(items, "", "", "January 2, 2026"),
	}
	for name, body := range bodies {
		if !strings.Contains(body, "paste the key") {
			t.Errorf("%s receipt lacks activation instructions", name)
		}
		if !strings.Contains(body, "KEY123") {
			t.Errorf("%s receipt lacks the license key", name)
		}
	}
}
