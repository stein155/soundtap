// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisplayDefaultsToDarkUntilAZoneIsKnown(t *testing.T) {
	st := Open(filepath.Join(t.TempDir(), "s.json"))
	d := st.Get().Display
	if d.ClockOn() || d.Hour12 {
		t.Errorf("default display = %+v, want dark, 24-hour", d)
	}
}

func TestSetDisplayNeedsAZone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st := Open(path)

	if err := st.SetDisplay(Display{}); err == nil {
		t.Error("clock turned on without a timezone")
	}
	if err := st.SetDisplay(Display{Timezone: "Mars/Olympus"}); err == nil {
		t.Error("unknown timezone accepted")
	}
	if err := st.SetDisplay(Display{ClockOff: true}); err != nil {
		t.Errorf("clock off without a zone refused: %v", err)
	}
	amsterdam := Display{Timezone: "Europe/Amsterdam", Hour12: true, Brightness: 50}
	if err := st.SetDisplay(amsterdam); err != nil {
		t.Fatal(err)
	}
	if got := Open(path).Get().Display; got != amsterdam || !got.ClockOn() {
		t.Fatalf("reloaded display = %+v", got)
	}
}

// A settings file from before the weather line went still loads: its town, location and
// weather switch are ignored and the clock keeps its zone.
func TestDisplayIgnoresTheOldWeatherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	old := `{"display":{"clockOff":false,"hour12":true,"city":"Utrecht, Nederland","lat":52.09,"lon":5.12,"timezone":"Europe/Amsterdam","weatherOn":true,"brightness":50}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Open(path).Get().Display
	if got != (Display{Hour12: true, Timezone: "Europe/Amsterdam", Brightness: 50}) || !got.ClockOn() {
		t.Fatalf("display from an old file = %+v", got)
	}
}

func TestDisplayBrightnessSteps(t *testing.T) {
	d := Display{ClockOff: true}
	for _, ok := range []int{0, 25, 50, 80} {
		d.Brightness = ok
		if _, err := NormalizeDisplay(d); err != nil {
			t.Errorf("%d refused: %v", ok, err)
		}
	}
	d.Brightness = 100
	if _, err := NormalizeDisplay(d); err == nil {
		t.Error("100 accepted")
	}
}
