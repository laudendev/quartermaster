package main

import (
	"strconv"
	"time"
)

const maxMinAge = time.Hour

// parseMinAge reads the optional min_age query value (whole seconds, 0-3600).
// Empty means 0.
func parseMinAge(v string) (time.Duration, bool) {
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || time.Duration(n)*time.Second > maxMinAge {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}
