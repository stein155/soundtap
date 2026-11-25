// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"testing"
	"time"
)

func TestPresetLoad(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	loads := newPresetLoad(func() time.Time { return now })

	// No preset: a phone starting Spotify claims as always.
	loads.leave()

	if !claimed(loads) {
		t.Fatal("claim without a preset load was refused")
	}

	// Preset loads and starts while still selected, and says how long that took.
	loads.start()

	now = now.Add(3 * time.Second)
	if ok, took := loads.claim(); !ok || took != 3*time.Second {
		t.Fatalf("preset's own start = %v after %v, want true after 3s", ok, took)
	}

	// Preset left before librespot started: the late start is refused, once.
	loads.start()

	now = now.Add(11 * time.Second)

	loads.leave()

	if claimed(loads) {
		t.Fatal("late start of an abandoned preset took the amp")
	}

	if !claimed(loads) {
		t.Fatal("a later claim was refused too")
	}

	// Left long after the window: a later start is someone else's.
	loads.start()

	now = now.Add(presetLoadWindow)

	loads.leave()

	if !claimed(loads) {
		t.Fatal("claim outside the load window was refused")
	}

	// A failed load leaves nothing behind.
	loads.start()
	loads.clear()
	loads.leave()

	if !claimed(loads) {
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
	t.Parallel()

	now := time.Unix(1000, 0)
	loads := newPresetLoad(func() time.Time { return now })

	silent := loads.start()
	if !loads.unclaimed(silent) {
		t.Fatal("a load nothing claimed was not reported")
	}

	if loads.unclaimed(silent) {
		t.Fatal("the same load was reported twice")
	}

	played := loads.start()
	loads.claim()

	if loads.unclaimed(played) {
		t.Fatal("a claimed load was reported as never started")
	}

	old := loads.start()
	now = now.Add(time.Second)

	loads.start()

	if loads.unclaimed(old) {
		t.Fatal("an older load's timer settled the newer one")
	}
}

// librespot stopping a load before it started is retried once; a second stop, a claimed
// load, a left one or no load at all releases as before.
func TestPresetLoadStoppedEarly(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	loads := newPresetLoad(func() time.Time { return now })

	if loads.stoppedEarly() {
		t.Fatal("a stop with no preset loading asked for a retry")
	}

	loads.start()

	if !loads.stoppedEarly() {
		t.Fatal("the first early stop did not ask for a retry")
	}

	if loads.stoppedEarly() {
		t.Fatal("a second early stop asked for another retry")
	}

	loads.start()

	if !loads.stoppedEarly() {
		t.Fatal("a new preset did not get its own retry")
	}

	loads.start()
	loads.claim()

	if loads.stoppedEarly() {
		t.Fatal("a stop after the preset started asked for a retry")
	}

	loads.start()
	loads.leave()

	if loads.stoppedEarly() {
		t.Fatal("a stop of a preset the user left asked for a retry")
	}

	loads.start()

	now = now.Add(presetLoadWindow)

	if loads.stoppedEarly() {
		t.Fatal("a stop after the load window asked for a retry")
	}
}
