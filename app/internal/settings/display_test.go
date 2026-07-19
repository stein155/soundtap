// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"path/filepath"
	"testing"
)

func TestDisplayDefaultsToDarkUntilATownIsPicked(t *testing.T) {
	st := Open(filepath.Join(t.TempDir(), "s.json"))
	d := st.Get().Display
	if d.ClockOn() || d.WeatherEnabled() || d.Hour12 {
		t.Errorf("default display = %+v, want dark, 24-hour", d)
	}
}

func TestSetDisplayNeedsATown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st := Open(path)

	if err := st.SetDisplay(Display{}); err == nil {
		t.Error("clock turned on without a timezone")
	}
	if err := st.SetDisplay(Display{City: "X", Lat: 91, Timezone: "UTC"}); err == nil {
		t.Error("latitude 91 accepted")
	}
	if err := st.SetDisplay(Display{City: "X", Lat: 1, Lon: 1}); err == nil {
		t.Error("town without timezone accepted")
	}
	if err := st.SetDisplay(Display{City: "X", Lat: 1, Lon: 1, Timezone: "Mars/Olympus"}); err == nil {
		t.Error("unknown timezone accepted")
	}
	utrecht := Display{City: "Utrecht, Nederland", Lat: 52.09, Lon: 5.12, Timezone: "Europe/Amsterdam", WeatherOn: true, Hour12: true}
	if err := st.SetDisplay(utrecht); err != nil {
		t.Fatal(err)
	}
	if got := Open(path).Get().Display; got != utrecht || !got.ClockOn() || !got.WeatherEnabled() {
		t.Fatalf("reloaded display = %+v", got)
	}

	// Clearing the town drops the place and the weather; the zone keeps the clock.
	if err := st.SetDisplay(Display{Lat: 52.09, Lon: 5.12, Timezone: "Europe/Amsterdam", WeatherOn: true}); err != nil {
		t.Fatal(err)
	}
	got := st.Get().Display
	if got != (Display{Timezone: "Europe/Amsterdam"}) || !got.ClockOn() || got.WeatherEnabled() {
		t.Fatalf("display after clearing the town = %+v", got)
	}
	if err := st.SetDisplay(Display{Timezone: "Mars/Olympus"}); err == nil {
		t.Error("unknown timezone accepted without a town")
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
