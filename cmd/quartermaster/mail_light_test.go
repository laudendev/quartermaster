package main

import (
	"strings"
	"testing"
)

func TestReceiptHTMLDeclaresLightOnly(t *testing.T) {
	h := buildReceiptHTML([]receiptLineItem{{ProductName: "Pop Focus", AmountLine: "$9.00", LicenseKey: "KEY"}}, "", "$9.00", "Oct 30, 2026")
	for _, want := range []string{
		`<meta name="color-scheme" content="light only">`,
		`<meta name="supported-color-schemes" content="light only">`,
		`color-scheme:light only`,
		`<body bgcolor="#FDF8F0"`,
		`bgcolor="#FEFEFE"`,
		`&copy; 2026`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(h, "prefers-color-scheme") {
		t.Error("must not ship dark-mode styles")
	}
}
