// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"testing"
	"time"
)

func TestPumpMayRest(t *testing.T) {
	now := time.Now()
	long, short := now.Add(-pumpRestAfter), now.Add(-time.Second)
	for _, c := range []struct {
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
		if got := pumpMayRest(c.idle, c.since, now, c.codecReady, c.tvSharing); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A waiter sees the rest end, and each run gets a new generation.
func TestPumpGateWakes(t *testing.T) {
	g := newPumpGate()
	gen0, ok := g.awake(t.Context())
	if !ok {
		t.Fatal("an awake gate blocked")
	}
	g.set(true)
	got := make(chan uint64, 1)
	go func() { gen, _ := g.awake(t.Context()); got <- gen }()
	select {
	case <-got:
		t.Fatal("awake returned while resting")
	case <-time.After(50 * time.Millisecond):
	}
	g.set(false)
	select {
	case gen := <-got:
		if gen == gen0 {
			t.Error("the run after a rest kept the old generation")
		}
	case <-time.After(time.Second):
		t.Fatal("awake did not return after the rest ended")
	}
	g.set(true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, ok := g.awake(ctx); ok {
		t.Error("awake reported a run after ctx ended")
	}
}
