// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"sync"
	"time"
)

// pumpRestAfter is how long the speaker stays idle in standby before the Mixer pump stops.
// Until then it keeps the DAC clocked as before; after it, aplay is closed and nothing
// wakes 320 times a second for silence nobody hears.
const pumpRestAfter = 30 * time.Second

// pumpGate tells runMixerPump when to rest and when to run again. Every change closes
// changed and replaces it, and bumps gen, so a waiter sees each one.
type pumpGate struct {
	mu      sync.Mutex
	rest    bool
	gen     uint64
	changed chan struct{}
}

func newPumpGate() *pumpGate { return &pumpGate{changed: make(chan struct{})} }

// pumpRest is the gate of this daemon's one Mixer pump.
var pumpRest = newPumpGate()

func (g *pumpGate) set(rest bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rest == rest {
		return
	}
	g.rest = rest
	g.gen++
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *pumpGate) state() (rest bool, gen uint64, changed <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rest, g.gen, g.changed
}

// awake blocks while the pump rests and returns the generation it ran under, or false when
// ctx ends first.
func (g *pumpGate) awake(ctx context.Context) (uint64, bool) {
	for {
		rest, gen, changed := g.state()
		if !rest {
			return gen, true
		}
		select {
		case <-ctx.Done():
			return 0, false
		case <-changed:
		}
	}
}

// pumpMayRest reports whether the pump may stop: idle (standby, no announcement, no amp
// demand) for pumpRestAfter, after the codec gain pass, which needs the DAC clock, is done.
func pumpMayRest(ampIdle bool, idleSince, now time.Time, codecReady, tvSharing bool) bool {
	return ampIdle && codecReady && !tvSharing && !idleSince.IsZero() && now.Sub(idleSince) >= pumpRestAfter
}
