package main

import "testing"

func TestMinAgeFromEnv(t *testing.T) {
	t.Setenv("SIGNER_MIN_AGE", "")
	if n, err := minAgeFromEnv(); n != 0 || err != nil {
		t.Fatalf("unset: %d, %v", n, err)
	}
	t.Setenv("SIGNER_MIN_AGE", "120")
	if n, err := minAgeFromEnv(); n != 120 || err != nil {
		t.Fatalf("120: %d, %v", n, err)
	}
	for _, bad := range []string{"-1", "3601", "abc"} {
		t.Setenv("SIGNER_MIN_AGE", bad)
		if _, err := minAgeFromEnv(); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}
