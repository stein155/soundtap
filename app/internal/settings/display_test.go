// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/settings"
)

func TestDisplayDefaultsToDarkUntilAZoneIsKnown(t *testing.T) {
	t.Parallel()

	store := settings.Open(filepath.Join(t.TempDir(), "s.json"))

	d := store.Get().Display
	if d.ClockOn() || d.Hour12 {
		t.Errorf("default display = %+v, want dark, 24-hour", d)
	}
}

func TestSetDisplayNeedsAZone(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "s.json")
	store := settings.Open(path)

	err := store.SetDisplay(settings.Display{ClockOff: false, Hour12: false, Timezone: ""})
	if err == nil {
		t.Error("clock turned on without a timezone")
	}

	err = store.SetDisplay(settings.Display{ClockOff: false, Hour12: false, Timezone: "Mars/Olympus"})
	if err == nil {
		t.Error("unknown timezone accepted")
	}

	err = store.SetDisplay(settings.Display{ClockOff: true, Hour12: false, Timezone: ""})
	if err != nil {
		t.Errorf("clock off without a zone refused: %v", err)
	}

	amsterdam := settings.Display{ClockOff: false, Hour12: true, Timezone: "Europe/Amsterdam"}

	err = store.SetDisplay(amsterdam)
	if err != nil {
		t.Fatal(err)
	}

	if got := settings.Open(path).Get().Display; got != amsterdam || !got.ClockOn() {
		t.Fatalf("reloaded display = %+v", got)
	}
}

// A settings file from before the weather line and the brightness setting went still
// loads: its town, location, weather switch and brightness are ignored and the clock keeps
// its zone.
func TestDisplayIgnoresTheOldWeatherFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "s.json")

	old := `{"display":{"clockOff":false,"hour12":true,"city":"Utrecht, Nederland","lat":52.09,"lon":5.12,` +
		`"timezone":"Europe/Amsterdam","weatherOn":true,"brightness":50}}`

	err := os.WriteFile(path, []byte(old), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := settings.Open(path).Get().Display
	if got != (settings.Display{ClockOff: false, Hour12: true, Timezone: "Europe/Amsterdam"}) || !got.ClockOn() {
		t.Fatalf("display from an old file = %+v", got)
	}
}
