// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"testing"
	"time"
)

func TestPumpMayRest(t *testing.T) {
	t.Parallel()

	now := time.Now()

	long, short := now.Add(-pumpRestAfter), now.Add(-time.Second)
	for _, row := range []struct {
		name                  string
		idle                  bool
		since                 time.Time
		codecReady, tvSharing bool
		want                  bool
	}{
		{"long standby", true, long, true, false, true},
		{"standby just began", true, short, true, false, false},
		{"playing", false, time.Time{}, true, false, false},
		{"codec pass not done", true, long, false, false, false},
		{"sharing the TV input", true, long, true, true, false},
	} {
		if got := pumpMayRest(row.idle, row.since, now, row.codecReady, row.tvSharing); got != row.want {
			t.Errorf("%s: %v, want %v", row.name, got, row.want)
		}
	}
}

// A waiter sees the rest end, and each run gets a new generation.
func TestPumpGateWakes(t *testing.T) {
	t.Parallel()

	gate := newPumpGate()

	gen0, ok := gate.awake(t.Context())
	if !ok {
		t.Fatal("an awake gate blocked")
	}

	gate.set(true)

	got := make(chan uint64, 1)

	go func() { gen, _ := gate.awake(t.Context()); got <- gen }()

	select {
	case <-got:
		t.Fatal("awake returned while resting")
	case <-time.After(50 * time.Millisecond):
	}

	gate.set(false)

	select {
	case gen := <-got:
		if gen == gen0 {
			t.Error("the run after a rest kept the old generation")
		}
	case <-time.After(time.Second):
		t.Fatal("awake did not return after the rest ended")
	}

	gate.set(true)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, ok := gate.awake(ctx); ok {
		t.Error("awake reported a run after ctx ended")
	}
}
