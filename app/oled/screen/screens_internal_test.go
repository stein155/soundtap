// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// sampleBar is the design's status bar: source and room as given, at 08:42.
func sampleBar(source, room string) Bar {
	return Bar{Source: source, Room: room, Time: "08:42", State: "", Mark: "", Battery: 0, Charging: false}
}

// goldenScreens is every screen as the design draws it in Chromium
// (app/oled/design/golden.sh), next to the same screen from this package.
func goldenScreens() map[string]func() *Frame {
	return map[string]func() *Frame{
		"radio": func() *Frame {
			return Station(sampleBar("", ""), "NPO Radio 2", "Er is muziek voor iedereen", 1, "LIVE", 10)
		},
		"spotify": func() *Frame {
			bar := sampleBar("Spotify", "+1")

			return Track(bar, "Sun Goes Down", "Robin Schulz · Discover Weekly", 60.5, 214)
		},
		"volume":    func() *Frame { return Volume(32) },
		"volume-0":  func() *Frame { return Volume(0) },
		"volume-80": func() *Frame { return Volume(80) },
		"mute":      func() *Frame { return Mute("Gedempt") },
		"preset":    func() *Frame { return Preset("PRESET", 3, "Qmusic") },
		"bluetooth": func() *Frame {
			return Bluetooth(sampleBar(SourceBT, "Keuken"), "Bluetooth", "iPhone van Sanne")
		},
		"holding": func() *Frame { return Holding("Opslaan als preset", 3, 0.6) },
		"saved":   func() *Frame { return Saved("Opgeslagen als preset") },
		"buffering": func() *Frame {
			return Station(sampleBar("", ""), "NPO Radio 2", "Er is muziek voor iedereen", 1, "BUFFEREN", 7)
		},
		"aux":           func() *Frame { return Aux(sampleBar("", ""), "Externe ingang") },
		"pairing":       func() *Frame { return Pairing("Koppelen…", "Zoek naar Keuken") },
		"standby":       func() *Frame { return Standby("08:42", "", 0) },
		"standby-drift": func() *Frame { return Standby("08:42", "", 3) },
		"standby-12h":   func() *Frame { return Standby("8:42", "PM", 0) },
		"offline": func() *Frame {
			return Offline(sampleBar("", ""), "Geen verbinding", "Opnieuw proberen", 0)
		},
	}
}

// snapshotScreens are screens the design does not draw, kept as the frames this package
// renders: the boot bar (stock-style, without the design's wordmark and word), Message
// and Notice, and each with a long translation. Regenerate with UPDATE_SNAPSHOTS=1
// after a change meant to move them.
func snapshotScreens() map[string]func() *Frame {
	return map[string]func() *Frame{
		"boot":    func() *Frame { return Boot(0.5) },
		"message": func() *Frame { return Message("Preset empty") },
		"message-long": func() *Frame {
			return Message("Der Speicherplatz für diese Voreinstellung konnte nicht beschrieben werden")
		},
		"notice-update": func() *Frame { return Notice("Updating", []string{"40%", "Do not unplug"}, 0.4) },
		"notice-setup": func() *Frame {
			return Notice("Setup", []string{"Join Wi-Fi", "SoundTap ST20 A1B2C3", "Then open", "192.168.4.1"}, -1)
		},
		"notice-amp": func() *Frame { return Notice("Amp fault", []string{"No sound"}, -1) },
		"notice-long": func() *Frame {
			return Notice("Ενημέρωση συστήματος σε εξέλιξη", []string{
				"Μην αποσυνδέετε το ηχείο από το ρεύμα", "40%",
			}, 0.4)
		},
		"notice-long-setup-ja": func() *Frame {
			return Notice("セットアップ", []string{
				"Wi-Fiに接続してください", "SoundTap ST20 A1B2C3", "その後、次のアドレスを開いてください", "192.168.4.1",
			}, -1)
		},
	}
}

func TestSnapshots(t *testing.T) {
	t.Parallel()

	for name, render := range snapshotScreens() {
		got := render().Pix(maxLevel)

		path := filepath.Join("testdata", name+".png")
		if os.Getenv("UPDATE_SNAPSHOTS") == "1" {
			writeGray(t, path, got)
		}

		want := readGolden(t, name)
		if !bytes.Equal(got, want.Pix) {
			t.Errorf("%s differs from %s; UPDATE_SNAPSHOTS=1 regenerates it", name, path)
		}
	}
}

// TestScreensMatchDesign holds each screen to the design. Skia's glyph rasteriser and
// this one's atlas round anti-aliased edges a little differently, so a level or two
// off along text edges is expected; a pixel off by more is a shape, a position or a
// weight that moved, and only a handful of those are allowed per screen.
func TestScreensMatchDesign(t *testing.T) {
	t.Parallel()

	const maxFar = 20 // pixels more than two levels off

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

		t.Logf("%-14s %4d pixels off, %3d by more than 2 levels, worst %d", name, off, far, worst)

		if far > maxFar {
			t.Errorf("%s: %d pixels more than 2 levels off the design (max %d)", name, far, maxFar)
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

// The Portable's battery sits left of the time, in stock's bands; without one nothing is
// drawn there and the stereo mark keeps its place.
func TestStatusBarBattery(t *testing.T) {
	t.Parallel()

	lit := func(f *Frame, x0, x1 int) float64 {
		var sum float64

		for y := 2; y < 14; y++ {
			for x := x0; x < x1; x++ {
				sum += f.lum[y*frameWidth+x]
			}
		}

		return sum
	}
	bar := func(mark string, battery int) *Frame {
		f := newFrame()
		f.statusBar(&Bar{Source: "", Room: "", Time: "12:00", State: "", Mark: mark, Battery: battery, Charging: false})

		return f
	}
	plain := bar("", 0)
	low := bar("", 10)
	full := bar("", 100)

	if lit(plain, 60, 96) != 0 {
		t.Fatal("something drawn where the battery goes, without a battery")
	}

	if l, f := lit(low, 60, 96), lit(full, 60, 96); l == 0 || f <= l {
		t.Fatalf("battery icon light %v at 10 %%, %v at 100 %%: want more bars when full", l, f)
	}

	mark := bar("L", 0)

	markBattery := bar("L", 50)
	if lit(mark, 60, 120) == lit(markBattery, 60, 120) {
		t.Fatal("the battery did not change the bar with a stereo mark")
	}
}
