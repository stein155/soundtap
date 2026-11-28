// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"bytes"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/oled"
)

// While music plays no clock is drawn: the frame is the same whatever the time, within a
// minute's orbit step.
func TestPlayingScreenHasNoClock(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"SPOTIFY", "TUNEIN", "BLUETOOTH", "AUX"} {
		info := infoOf(playingNow(source, "NPO Radio 2", "Sun Goes Down", "Robin Schulz", "PLAY_STATE"), "Keuken")
		corner := noCorner()

		var memo playingMemo

		morning := viewAt(time.Date(2026, 9, 25, 8, 42, 1, 0, time.UTC), time.Second, false)
		evening := viewAt(time.Date(2026, 9, 25, 21, 17, 1, 0, time.UTC), time.Second, false)
		evening.orbit, evening.dotStep = morning.orbit, morning.dotStep

		first := append([]byte(nil), memo.playing(&info, &corner, 61000, 214000, &morning)...)
		if !bytes.Equal(first, memo.playing(&info, &corner, 61000, 214000, &evening)) {
			t.Errorf("%s: the playing screen changes with the clock", source)
		}
	}
}

// The orbit moves the playing screen one pixel at a time around its place, keeps every lit
// pixel of a real screen, and comes back where it started.
func TestOrbitMovesThePlayingScreenOnePixel(t *testing.T) {
	t.Parallel()

	info := infoOf(playingNow("SPOTIFY", "", "Sun Goes Down", "Robin Schulz", "PLAY_STATE"), "Keuken")
	moment := view{page: 0, dimmed: false, orbit: 0, drift: 0, dotStep: 0, fade: 0}
	corner := noCorner()
	pix := playingScreen(&info, &corner, 61000, 214000, &moment).Pix(fullLevel)
	lit := func(frame []byte) int { return len(frame) - bytes.Count(frame, []byte{0}) }

	if !bytes.Equal(orbitFrame(pix, 0), pix) || !bytes.Equal(orbitFrame(pix, len(orbitPath)), pix) {
		t.Fatal("the orbit does not start and end in place")
	}

	for step := 1; step < len(orbitPath); step++ {
		moved := orbitFrame(pix, step)
		if bytes.Equal(moved, pix) {
			t.Errorf("step %d: the screen did not move", step)
		}

		if lit(moved) != lit(pix) {
			t.Errorf("step %d: %d lit pixels, want %d: something moved off the panel", step, lit(moved), lit(pix))
		}

		dx, dy := orbitPath[step][0], orbitPath[step][1]
		if dx < -1 || dx > 1 || dy < -1 || dy > 1 {
			t.Errorf("step %d: offset (%d, %d) is more than a pixel", step, dx, dy)
		}
	}

	if len(pix) != oled.Width*oled.Height {
		t.Fatalf("frame is %d bytes", len(pix))
	}
}
