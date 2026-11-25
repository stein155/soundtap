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

	err := st.SetDisplay(Display{})
	if err == nil {
		t.Error("clock turned on without a timezone")
	}

	err = st.SetDisplay(Display{Timezone: "Mars/Olympus"})
	if err == nil {
		t.Error("unknown timezone accepted")
	}

	err = st.SetDisplay(Display{ClockOff: true})
	if err != nil {
		t.Errorf("clock off without a zone refused: %v", err)
	}

	amsterdam := Display{Timezone: "Europe/Amsterdam", Hour12: true}

	err = st.SetDisplay(amsterdam)
	if err != nil {
		t.Fatal(err)
	}

	if got := Open(path).Get().Display; got != amsterdam || !got.ClockOn() {
		t.Fatalf("reloaded display = %+v", got)
	}
}

// A settings file from before the weather line and the brightness setting went still
// loads: its town, location, weather switch and brightness are ignored and the clock keeps
// its zone.
func TestDisplayIgnoresTheOldWeatherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")

	old := `{"display":{"clockOff":false,"hour12":true,"city":"Utrecht, Nederland","lat":52.09,"lon":5.12,"timezone":"Europe/Amsterdam","weatherOn":true,"brightness":50}}`

	err := os.WriteFile(path, []byte(old), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := Open(path).Get().Display
	if got != (Display{Hour12: true, Timezone: "Europe/Amsterdam"}) || !got.ClockOn() {
		t.Fatalf("display from an old file = %+v", got)
	}
}
