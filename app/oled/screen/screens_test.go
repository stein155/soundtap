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

// golden is every screen as the design draws it in Chromium (app/oled/design/golden.sh),
// next to the same screen from this package.
var golden = map[string]func() *Frame{
	"radio": func() *Frame {
		return Station(Bar{Time: "08:42"}, "NPO Radio 2", "Er is muziek voor iedereen", 1, "LIVE", 10)
	},
	"spotify": func() *Frame {
		return Track(Bar{Source: "Spotify", Room: "+1", Time: "08:42"}, "Sun Goes Down", "Robin Schulz · Discover Weekly", 60.5, 214)
	},
	"volume":    func() *Frame { return Volume(32) },
	"volume-0":  func() *Frame { return Volume(0) },
	"volume-80": func() *Frame { return Volume(80) },
	"mute":      func() *Frame { return Mute("Gedempt") },
	"preset":    func() *Frame { return Preset("PRESET", 3, "Qmusic") },
	"bluetooth": func() *Frame {
		return Bluetooth(Bar{Source: SourceBT, Room: "Keuken", Time: "08:42"}, "Bluetooth", "iPhone van Sanne")
	},
	"holding": func() *Frame { return Holding("Opslaan als preset", 3, 0.6) },
	"saved":   func() *Frame { return Saved("Opgeslagen als preset") },
	"buffering": func() *Frame {
		return Station(Bar{Time: "08:42"}, "NPO Radio 2", "Er is muziek voor iedereen", 1, "BUFFEREN", 7)
	},
	"aux":           func() *Frame { return Aux(Bar{Time: "08:42"}, "Externe ingang") },
	"pairing":       func() *Frame { return Pairing("Koppelen…", "Zoek naar Keuken") },
	"standby":       func() *Frame { return Standby("08:42", "donderdag 25 sep", "", "", 0) },
	"standby-drift": func() *Frame { return Standby("08:42", "donderdag 25 sep", "", "", 3) },
	"standby-temp":  func() *Frame { return Standby("08:42", "", "do 25 sep", "14°", 0) },
	"offline":       func() *Frame { return Offline(Bar{Time: "08:42"}, "Geen verbinding", "Opnieuw proberen", 0) },
}

// snapshots are screens the design does not draw, kept as the frames this package
// renders: the boot bar (stock-style, without the design's wordmark and word), Message
// and Notice, and each with a long translation. Regenerate with UPDATE_SNAPSHOTS=1
// after a change meant to move them.
var snapshots = map[string]func() *Frame{
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
		return Notice("Ενημέρωση συστήματος σε εξέλιξη", []string{"Μην αποσυνδέετε το ηχείο από το ρεύμα", "40%"}, 0.4)
	},
	"notice-long-setup-ja": func() *Frame {
		return Notice("セットアップ", []string{"Wi-Fiに接続してください", "SoundTap ST20 A1B2C3", "その後、次のアドレスを開いてください", "192.168.4.1"}, -1)
	},
}

func TestSnapshots(t *testing.T) {
	for name, render := range snapshots {
		got := render().Pix(15)
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
	const maxFar = 20                   // pixels more than two levels off
	out := os.Getenv("ST20_RENDER_DIR") // set to look at the Go renders
	for name, render := range golden {
		want := readGolden(t, name)
		got := render().Pix(15)
		off, far, worst := 0, 0, 0
		for i := range got {
			d := abs(int(got[i])/17 - int(want.Pix[i])/17)
			if d > 0 {
				off++
			}
			if d > 2 {
				far++
			}
			worst = max(worst, d)
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
	fh, err := os.Open(filepath.Join("testdata", name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	img, err := png.Decode(fh)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := img.(*image.Gray)
	if !ok {
		t.Fatalf("%s: not greyscale", name)
	}
	return g
}

func writeGray(t *testing.T, path string, pix []byte) {
	t.Helper()
	img := &image.Gray{Pix: pix, Stride: W, Rect: image.Rect(0, 0, W, H)}
	fh, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if err := png.Encode(fh, img); err != nil {
		t.Fatal(err)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
