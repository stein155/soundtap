// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"bytes"
	"testing"

	"github.com/stein155/soundtap/app/oled"
)

// While music plays no clock is drawn: the frame is the same whatever the time.
func TestPlayingScreenHasNoClock(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"SPOTIFY", "TUNEIN", "BLUETOOTH", "AUX"} {
		info := infoOf(playingNow(source, "NPO Radio 2", "Sun Goes Down", "Robin Schulz", "PLAY_STATE"), "Keuken")

		morning := playingScreen(&info, barAt("08:42"), 61000, 214000).Pix(fullLevel)
		evening := playingScreen(&info, barAt("21:17"), 61000, 214000).Pix(fullLevel)

		if !bytes.Equal(morning, evening) {
			t.Errorf("%s: the playing screen changes with the clock", source)
		}
	}
}

// The orbit moves the playing screen one pixel at a time around its place, keeps every lit
// pixel of a real screen, and comes back where it started.
func TestOrbitMovesThePlayingScreenOnePixel(t *testing.T) {
	t.Parallel()

	info := infoOf(playingNow("SPOTIFY", "", "Sun Goes Down", "Robin Schulz", "PLAY_STATE"), "Keuken")
	pix := playingScreen(&info, barAt("08:42"), 61000, 214000).Pix(fullLevel)
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
