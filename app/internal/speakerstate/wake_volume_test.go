// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import "testing"

// A speaker left loud comes back at stock's thresholdHigh, however it wakes.
func TestWakeBoundsTheVolume(t *testing.T) {
	wakes := map[string]func(*Speaker){
		"POWER": func(sp *Speaker) { sp.ApplyKey("POWER") },
		"Wake":  func(sp *Speaker) { sp.Wake(bg) },
		"source": func(sp *Speaker) {
			if err := sp.Select(bg, "AUX", "", "", "AUX", "", ""); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, wake := range wakes {
		for _, c := range []struct{ max, before, after int }{
			{70, 100, 70},
			{70, 40, 40},
			{0, 100, 100}, // the WLA: no bound
		} {
			sp := New()
			sp.SetWakeVolumeMax(c.max)
			selectX(t, sp)
			if err := sp.SetVolume(bg, c.before); err != nil {
				t.Fatal(err)
			}
			sp.ApplyKey("POWER")
			if np := now(t, sp); np.Source != "STANDBY" {
				t.Fatalf("%s: not in standby: %+v", name, np)
			}
			wake(sp)
			if got, _ := sp.Volume(bg); got != c.after {
				t.Errorf("%s with max %d: volume %d woke at %d, want %d", name, c.max, c.before, got, c.after)
			}
		}
	}
}

// The bound is for waking only: in use, the volume goes past it as before.
func TestWakeBoundLeavesAWakeSpeakerAlone(t *testing.T) {
	sp := New()
	sp.SetWakeVolumeMax(70)
	selectX(t, sp)
	if err := sp.SetVolume(bg, 90); err != nil {
		t.Fatal(err)
	}
	if got, _ := sp.Volume(bg); got != 90 {
		t.Fatalf("volume = %d, want 90", got)
	}
}

// A muted speaker that wakes and unmutes does not jump past the bound either.
func TestWakeBoundsTheMutedLevel(t *testing.T) {
	sp := New()
	sp.SetWakeVolumeMax(70)
	selectX(t, sp)
	if err := sp.SetVolume(bg, 95); err != nil {
		t.Fatal(err)
	}
	sp.ApplyKey("MUTE")
	sp.ApplyKey("POWER")
	sp.ApplyKey("POWER")
	sp.ApplyKey("MUTE")
	if got, _ := sp.Volume(bg); got != 70 {
		t.Fatalf("unmuted after wake at %d, want 70", got)
	}
}

// A speaker left quiet comes back at stock's thresholdLow, however it wakes.
func TestWakeRaisesTheVolumeToTheFloor(t *testing.T) {
	for _, c := range []struct{ min, before, after int }{
		{10, 3, 10},
		{10, 0, 10},
		{10, 40, 40},
		{0, 3, 3}, // the WLA: no floor
	} {
		sp := New()
		sp.SetWakeVolumeMin(c.min)
		selectX(t, sp)
		if err := sp.SetVolume(bg, c.before); err != nil {
			t.Fatal(err)
		}
		sp.ApplyKey("POWER")
		sp.ApplyKey("POWER")
		if got, _ := sp.Volume(bg); got != c.after {
			t.Errorf("min %d: volume %d woke at %d, want %d", c.min, c.before, got, c.after)
		}
	}
}

// A controller's Stop and Play for its next track is not a wake: the level it set stays.
func TestControllerRestartKeepsTheVolume(t *testing.T) {
	sp := New()
	sp.SetWakeVolumeMin(10)
	if err := sp.Select(bg, "UPNP", "", "http://a", "A", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetVolume(bg, 3); err != nil {
		t.Fatal(err)
	}
	if err := sp.ReleaseSource(bg, "UPNP"); err != nil {
		t.Fatal(err)
	}
	sp.Wake(bg)
	if err := sp.Select(bg, "UPNP", "", "http://b", "B", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := sp.Volume(bg); got != 3 {
		t.Fatalf("volume after the controller's restart = %d, want 3", got)
	}
	sp.releasedAt = sp.releasedAt.Add(-controllerRestartWindow)
	sp.ApplyKey("POWER")
	sp.ApplyKey("POWER")
	if got, _ := sp.Volume(bg); got != 10 {
		t.Fatalf("a later wake left the volume at %d, want 10", got)
	}
}
