// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"testing"
	"time"
)

func TestPresetLoad(t *testing.T) {
	now := time.Unix(1000, 0)
	p := &presetLoad{now: func() time.Time { return now }}

	// No preset: a phone starting Spotify claims as always.
	p.leave()
	if !claimed(p) {
		t.Fatal("claim without a preset load was refused")
	}

	// Preset loads and starts while still selected, and says how long that took.
	p.start()
	now = now.Add(3 * time.Second)
	if ok, took := p.claim(); !ok || took != 3*time.Second {
		t.Fatalf("preset's own start = %v after %v, want true after 3s", ok, took)
	}

	// Preset left before librespot started: the late start is refused, once.
	p.start()
	now = now.Add(11 * time.Second)
	p.leave()
	if claimed(p) {
		t.Fatal("late start of an abandoned preset took the amp")
	}
	if !claimed(p) {
		t.Fatal("a later claim was refused too")
	}

	// Left long after the window: a later start is someone else's.
	p.start()
	now = now.Add(presetLoadWindow)
	p.leave()
	if !claimed(p) {
		t.Fatal("claim outside the load window was refused")
	}

	// A failed load leaves nothing behind.
	p.start()
	p.clear()
	p.leave()
	if !claimed(p) {
		t.Fatal("cleared load still refused a claim")
	}
}

func claimed(p *presetLoad) bool {
	ok, _ := p.claim()
	return ok
}

// A load librespot accepted but never started is settled once, and only that load:
// a claimed one or one replaced by a newer preset is left alone.
func TestPresetLoadUnclaimed(t *testing.T) {
	now := time.Unix(1000, 0)
	p := &presetLoad{now: func() time.Time { return now }}

	silent := p.start()
	if !p.unclaimed(silent) {
		t.Fatal("a load nothing claimed was not reported")
	}
	if p.unclaimed(silent) {
		t.Fatal("the same load was reported twice")
	}

	played := p.start()
	p.claim()
	if p.unclaimed(played) {
		t.Fatal("a claimed load was reported as never started")
	}

	old := p.start()
	now = now.Add(time.Second)
	p.start()
	if p.unclaimed(old) {
		t.Fatal("an older load's timer settled the newer one")
	}
}

// librespot stopping a load before it started is retried once; a second stop, a claimed
// load, a left one or no load at all releases as before.
func TestPresetLoadStoppedEarly(t *testing.T) {
	now := time.Unix(1000, 0)
	p := &presetLoad{now: func() time.Time { return now }}

	if p.stoppedEarly() {
		t.Fatal("a stop with no preset loading asked for a retry")
	}
	p.start()
	if !p.stoppedEarly() {
		t.Fatal("the first early stop did not ask for a retry")
	}
	if p.stoppedEarly() {
		t.Fatal("a second early stop asked for another retry")
	}
	p.start()
	if !p.stoppedEarly() {
		t.Fatal("a new preset did not get its own retry")
	}

	p.start()
	p.claim()
	if p.stoppedEarly() {
		t.Fatal("a stop after the preset started asked for a retry")
	}

	p.start()
	p.leave()
	if p.stoppedEarly() {
		t.Fatal("a stop of a preset the user left asked for a retry")
	}

	p.start()
	now = now.Add(presetLoadWindow)
	if p.stoppedEarly() {
		t.Fatal("a stop after the load window asked for a retry")
	}
}
