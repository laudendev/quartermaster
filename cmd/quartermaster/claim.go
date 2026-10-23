package main

import (
	"time"

	"quartermaster/activations"
	"quartermaster/trials"
)

// signalWire is one hashed hardware identifier in a licence request. The field
// is optional: apps older than 0.1.13 do not send it.
type signalWire struct {
	Kind string `json:"kind"`
	Hash string `json:"hash"`
}

// parseSignals validates the optional signals of a licence request with the
// same rules as the trial endpoint (known kinds, 64 hex characters, at most 8,
// no repeats). No signals is fine.
func parseSignals(in []signalWire) ([]activations.Signal, error) {
	if len(in) == 0 {
		return nil, nil
	}
	conv := make([]trials.Signal, len(in))
	for i, s := range in {
		conv[i] = trials.Signal{Kind: s.Kind, Hash: s.Hash}
	}
	ok, err := trials.ValidateSignals(conv)
	if err != nil {
		return nil, err
	}
	out := make([]activations.Signal, len(ok))
	for i, s := range ok {
		out[i] = activations.Signal{Kind: s.Kind, Hash: s.Hash}
	}
	return out, nil
}

// seatClock is the time source for seat claims; tests may replace it.
var seatClock = func() int64 { return time.Now().Unix() }
