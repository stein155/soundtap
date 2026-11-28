// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"bytes"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/oled/screen"
)

// A held key combination shows what it does, the seconds left and a ring, above a preset key's
// "storing" bar; it never shows a negative count, and it is gone once the combination ends.
func TestChordCountShowsWhatItDoesAndTheSecondsLeft(t *testing.T) {
	t.Parallel()

	front := New(time.Second)
	front.PresetDown(2)
	front.ChordDown("Setup mode", 10*time.Second)

	start := front.chordAt
	overlay, _ := front.Current(start.Add(3200 * time.Millisecond))

	if overlay == nil {
		t.Fatal("no overlay while the combination is held")
	}

	want := screen.Countdown(7, 0.32, Text("Setup mode")).Pix(fullLevel)
	if !bytes.Equal(overlay().Pix(fullLevel), want) {
		t.Error("at 3.2 s: want \"Setup mode\", 7 and the ring at 32 %")
	}

	late, _ := front.Current(start.Add(11 * time.Second))
	if !bytes.Equal(late().Pix(fullLevel), screen.Countdown(0, 1.1, Text("Setup mode")).Pix(fullLevel)) {
		t.Error("past the hold: want 0, a full ring")
	}

	front.ChordUp()
	front.PresetUp()

	if after, _ := front.Current(start.Add(4 * time.Second)); after != nil {
		t.Error("the count stayed after the combination ended")
	}
}
