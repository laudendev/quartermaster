package main

import (
	"fmt"
	"os"
	"strconv"
)

// minAgeFromEnv reads SIGNER_MIN_AGE (whole seconds, 0-3600). Unset means 0:
// the primary signer claims requests immediately. A backup signer sets it so
// it only picks up requests the primary has left unsigned for that long.
func minAgeFromEnv() (int, error) {
	v := os.Getenv("SIGNER_MIN_AGE")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 3600 {
		return 0, fmt.Errorf("SIGNER_MIN_AGE must be 0-3600 seconds, got %q", v)
	}
	return n, nil
}
