// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noCorner is a playing screen with nothing in its corners.
func noCorner() Corner {
	return Corner{State: "", Preset: 0, Badge: "", BadgeLevel: 0, Mark: "", Battery: 0, Charging: false}
}

// noticeOf is a notice with the given options.
func noticeOf(kind Icon, title, line string, small bool, dots int) *Frame {
	return Notice(kind, title, line, NoticeOpt{IconLevel: 0, Small: small, Dots: dots})
}

// markAt is BootMark's frame alone.
func markAt(seconds float64) *Frame {
	frame, _ := BootMark(seconds)

	return frame
}

// goldenScreens is every screen as the design draws it in Chromium (app/oled/design/
// golden-cases.js, rendered by golden.sh), next to the same screen from this package.
func goldenScreens() map[string]func() *Frame {
	live := noCorner()
	live.Preset, live.Badge, live.BadgeLevel = 1, "LIVE", maxLevel
	long := live
	long.Preset = 3
	buffering := live
	buffering.Badge, buffering.BadgeLevel = "BUFFEREN", 12
	paused := noCorner()
	paused.State = StatePaused
	stopped := noCorner()
	stopped.State = StateStopped
	left := noCorner()
	left.Mark = "L"

	return map[string]func() *Frame{
		"boot-empty":    func() *Frame { return Boot(0) },
		"boot-fill":     func() *Frame { return Boot(0.6) },
		"boot-slide":    func() *Frame { return markAt(0.2) },
		"boot-head":     func() *Frame { return markAt(0.95) },
		"boot-tap":      func() *Frame { return markAt(2.8) },
		"connecting":    func() *Frame { return noticeOf(IconWifi, "Verbinden…", "Thuis", false, 2) },
		"no-connection": func() *Frame { return noticeOf(IconWifiOff, "Geen verbinding", "", false, 1) },
		"setup-1":       func() *Frame { return SetupJoin("1 · Kies wifi", "SoundTap ST20 A1B2C3") },
		"setup-2":       func() *Frame { return SetupOpen("2 · Open daarna", "192.168.42.1", "soundtap.local") },
		"switch-eth":    func() *Frame { return noticeOf(IconCable, "Ethernet", "", false, 0) },
		"switch-wifi":   func() *Frame { return noticeOf(IconWifi, "Wifi", "", false, 0) },
		"hold-setup":    func() *Frame { return Countdown(3, 0.4, "Instelmodus") },
		"hold-reset":    func() *Frame { return Countdown(7, 0.3, "Fabrieksinstellingen") },
		"chord-setup":   func() *Frame { return noticeOf(IconCheck, "Instelmodus aan", "", false, 0) },
		"resetting":     func() *Frame { return noticeOf(IconNone, "Terugzetten", "Laat de stekker erin", false, 2) },
		"radio-live": func() *Frame {
			return Station(live, "NPO Radio 2", "Er is muziek voor iedereen", false, 0)
		},
		"radio-now":       func() *Frame { return RadioNow("Radar Love", "Golden Earring") },
		"radio-long":      func() *Frame { return Station(long, "Radio Swiss Classic Français", "", false, 0) },
		"radio-buffering": func() *Frame { return Station(buffering, "NPO Radio 2", "", false, -1) },
		"radio-paused":    func() *Frame { return Station(paused, "NPO Radio 2", "", false, -1) },
		"radio-failed":    func() *Frame { return Station(noCorner(), "Sky Radio", "Niet beschikbaar", true, -1) },
		"spotify":         func() *Frame { return Song(noCorner(), "Sun Goes Down", "Robin Schulz", 59, 214) },
		"spotify-long": func() *Frame {
			return Song(noCorner(), "The Less I Know The Better", "Tame Impala", 129, 216)
		},
		"upnp-stopped":   func() *Frame { return Song(stopped, "So What", "Miles Davis", 0, 562) },
		"stereo-left":    func() *Frame { return Song(left, "Sun Goes Down", "Robin Schulz", 59, 214) },
		"stream":         func() *Frame { return Song(noCorner(), "Sun Goes Down", "Robin Schulz", 0, 0) },
		"playback-error": func() *Frame { return noticeOf(IconWarn, "Afspeelfout", "", false, 0) },
		"multiroom":      func() *Frame { return noticeOf(IconGroup, "Spotify", "Met Woonkamer", false, 0) },
		"aux":            func() *Frame { return noticeOf(IconAux, "AUX", "Externe ingang", false, 0) },
		"dimmed":         func() *Frame { return Ambient("NPO Radio 2", 3, -2) },
		"bt-device":      func() *Frame { return noticeOf(IconBluetooth, "iPhone van Sanne", "", false, 0) },
		"bt-pairing":     func() *Frame { return noticeOf(IconBluetooth, "Koppelen…", "Zoek naar Keuken", false, 0) },
		"bt-connected":   func() *Frame { return noticeOf(IconCheck, "iPhone van Sanne", "", true, 0) },
		"bt-cant":        func() *Frame { return noticeOf(IconBluetoothOff, "Kan niet verbinden", "", true, 0) },
		"volume":         func() *Frame { return Volume(32) },
		"volume-0":       func() *Frame { return Volume(0) },
		"volume-100":     func() *Frame { return Volume(100) },
		"mute":           func() *Frame { return noticeOf(IconMute, "Gedempt", "", false, 0) },
		"preset":         func() *Frame { return Preset("PRESET", 3, "Qmusic") },
		"holding":        func() *Frame { return Holding("Opslaan als preset", 4, 0.55) },
		"saved":          func() *Frame { return Saved("Opgeslagen") },
		"preset-empty":   func() *Frame { return PresetEmpty(5, "Preset leeg") },
		"store-failed":   func() *Frame { return noticeOf(IconCross, "Niet opgeslagen", "", true, 0) },
		"action-unavailable": func() *Frame {
			return Notice(IconSkip, "Niet beschikbaar", "", NoticeOpt{IconLevel: 6, Small: true, Dots: 0})
		},
		"key-updating":  func() *Frame { return noticeOf(IconLock, "Bijwerken", "Laat de stekker erin", false, 0) },
		"standby":       func() *Frame { return Standby("08:42", "", 0, 0) },
		"standby-12h":   func() *Frame { return Standby("8:42", "AM", 0, 0) },
		"standby-moved": func() *Frame { return Standby("08:42", "", -3, -3) },
		"updating":      func() *Frame { return Progress("Bijwerken", 42, "Laat de stekker erin") },
		"netcard":       func() *Frame { return Netcard(0.32, 64) },
		"netcard-fade":  func() *Frame { return Netcard(7.35, 30) },
		"netcard-th":    func() *Frame { return Netcard(44.4, 80) },
		"amp-fault":     func() *Frame { return noticeOf(IconWarn, "Versterkerfout", "Geen geluid", false, 0) },
		"confirm":       func() *Frame { return Confirm("BEVESTIG", "Installeren?", "Houd een knop vast", 1) },
		"update-el":     func() *Frame { return Progress("Ενημέρωση", 42, "Μην αποσυνδέετε") },
	}
}

// Every case the design renders has a screen here, and the other way round.
func TestEveryGoldenHasAScreen(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("testdata", "*.png"))
	if err != nil {
		t.Fatal(err)
	}

	screens := goldenScreens()
	if len(files) != len(screens) {
		t.Errorf("%d goldens, %d screens", len(files), len(screens))
	}

	for _, file := range files {
		if _, ok := screens[strings.TrimSuffix(filepath.Base(file), ".png")]; !ok {
			t.Errorf("%s has no screen", file)
		}
	}
}

// TestScreensMatchDesign holds each screen to the design. Skia's glyph rasteriser and
// this one's atlas round anti-aliased edges a little differently, so a level or two
// off along text edges is expected; a pixel off by more is a shape, a position or a
// weight that moved, and only a handful of those are allowed per screen.
func TestScreensMatchDesign(t *testing.T) {
	t.Parallel()

	const maxFar = 40 // pixels more than two levels off: the 20 px titles have long edges

	// Greek comes from the fallback font (Manrope), whose own kerning Chromium applies to the
	// run and the atlas does not keep: its letters sit up to a pixel off.
	allow := map[string]int{"update-el": 150}

	out := os.Getenv("ST20_RENDER_DIR") // set to look at the Go renders

	for name, render := range goldenScreens() {
		want := readGolden(t, name)
		got := render().Pix(maxLevel)
		off, far, worst := 0, 0, 0

		for i := range got {
			levels := abs(int(got[i])/levelStep - int(want.Pix[i])/levelStep)
			if levels > 0 {
				off++
			}

			if levels > 2 {
				far++
			}

			worst = max(worst, levels)
		}

		if out != "" {
			writeGray(t, filepath.Join(out, name+".png"), got)
		}

		t.Logf("%-18s %4d pixels off, %3d by more than 2 levels, worst %d", name, off, far, worst)

		limit := maxFar
		if n, ok := allow[name]; ok {
			limit = n
		}

		if far > limit {
			t.Errorf("%s: %d pixels more than 2 levels off the design (max %d)", name, far, limit)
		}
	}
}

func readGolden(t *testing.T, name string) *image.Gray {
	t.Helper()

	file, err := os.Open(filepath.Join("testdata", name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}

	gray, ok := img.(*image.Gray)
	if !ok {
		t.Fatalf("%s: not greyscale", name)
	}

	return gray
}

func writeGray(t *testing.T, path string, pix []byte) {
	t.Helper()

	img := &image.Gray{Pix: pix, Stride: frameWidth, Rect: image.Rect(0, 0, frameWidth, frameHeight)}

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	err = png.Encode(file, img)
	if err != nil {
		t.Fatal(err)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}

	return n
}

// The Portable's battery sits top right; without one nothing is drawn there and the stereo
// mark keeps its place.
func TestCornerBattery(t *testing.T) {
	t.Parallel()

	lit := func(f *Frame, x0, x1 int) float64 {
		var sum float64

		for y := 2; y < 16; y++ {
			for x := x0; x < x1; x++ {
				sum += f.lum[y*frameWidth+x]
			}
		}

		return sum
	}
	corner := func(mark string, battery int) *Frame {
		f := newFrame()
		c := noCorner()
		c.Mark, c.Battery = mark, battery
		f.corner(&c)

		return f
	}
	plain := corner("", 0)
	low := corner("", 10)
	full := corner("", 100)

	if lit(plain, 60, 128) != 0 {
		t.Fatal("something drawn where the battery goes, without a battery")
	}

	if l, f := lit(low, 100, 128), lit(full, 100, 128); l == 0 || f <= l {
		t.Fatalf("battery icon light %v at 10 %%, %v at 100 %%: want more bars when full", l, f)
	}

	if lit(corner("L", 0), 60, 128) == lit(corner("L", 50), 60, 128) {
		t.Fatal("the battery did not change the corner with a stereo mark")
	}
}
