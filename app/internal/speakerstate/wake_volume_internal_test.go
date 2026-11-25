// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import "testing"

// A speaker left loud comes back at stock's thresholdHigh, however it wakes.
func TestWakeBoundsTheVolume(t *testing.T) {
	t.Parallel()

	wakes := map[string]func(*Speaker){
		"POWER": func(sp *Speaker) { sp.ApplyKey("POWER") },
		"Wake":  func(sp *Speaker) { sp.Wake(bg()) },
		"source": func(sp *Speaker) {
			err := sp.Select(bg(), "AUX", "", "", "AUX", "", "")
			if err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, wake := range wakes {
		for _, test := range []struct{ max, before, after int }{
			{70, 100, 70},
			{70, 40, 40},
			{0, 100, 100}, // the WLA: no bound
		} {
			spk := New()
			spk.SetWakeVolumeMax(test.max)
			selectX(t, spk)

			err := spk.SetVolume(bg(), test.before)
			if err != nil {
				t.Fatal(err)
			}

			spk.ApplyKey("POWER")

			if np := now(t, spk); np.Source != "STANDBY" {
				t.Fatalf("%s: not in standby: %+v", name, np)
			}

			wake(spk)

			if got, _ := spk.Volume(bg()); got != test.after {
				t.Errorf("%s with max %d: volume %d woke at %d, want %d", name, test.max, test.before, got, test.after)
			}
		}
	}
}

// The bound is for waking only: in use, the volume goes past it as before.
func TestWakeBoundLeavesAWakeSpeakerAlone(t *testing.T) {
	t.Parallel()

	spk := New()
	spk.SetWakeVolumeMax(70)
	selectX(t, spk)

	err := spk.SetVolume(bg(), 90)
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := spk.Volume(bg()); got != 90 {
		t.Fatalf("volume = %d, want 90", got)
	}
}

// A muted speaker that wakes and unmutes does not jump past the bound either.
func TestWakeBoundsTheMutedLevel(t *testing.T) {
	t.Parallel()

	spk := New()
	spk.SetWakeVolumeMax(70)
	selectX(t, spk)

	err := spk.SetVolume(bg(), 95)
	if err != nil {
		t.Fatal(err)
	}

	spk.ApplyKey("MUTE")
	spk.ApplyKey("POWER")
	spk.ApplyKey("POWER")
	spk.ApplyKey("MUTE")

	if got, _ := spk.Volume(bg()); got != 70 {
		t.Fatalf("unmuted after wake at %d, want 70", got)
	}
}

// A speaker left quiet comes back at stock's thresholdLow, however it wakes.
func TestWakeRaisesTheVolumeToTheFloor(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ min, before, after int }{
		{10, 3, 10},
		{10, 0, 10},
		{10, 40, 40},
		{0, 3, 3}, // the WLA: no floor
	} {
		spk := New()
		spk.SetWakeVolumeMin(test.min)
		selectX(t, spk)

		err := spk.SetVolume(bg(), test.before)
		if err != nil {
			t.Fatal(err)
		}

		spk.ApplyKey("POWER")
		spk.ApplyKey("POWER")

		if got, _ := spk.Volume(bg()); got != test.after {
			t.Errorf("min %d: volume %d woke at %d, want %d", test.min, test.before, got, test.after)
		}
	}
}

// A controller's Stop and Play for its next track is not a wake: the level it set stays.
func TestControllerRestartKeepsTheVolume(t *testing.T) {
	t.Parallel()

	spk := New()
	spk.SetWakeVolumeMin(10)

	err := spk.Select(bg(), "UPNP", "", "http://a", "A", "", "")
	if err != nil {
		t.Fatal(err)
	}

	err = spk.SetVolume(bg(), 3)
	if err != nil {
		t.Fatal(err)
	}

	err = spk.ReleaseSource(bg(), "UPNP")
	if err != nil {
		t.Fatal(err)
	}

	spk.Wake(bg())

	err = spk.Select(bg(), "UPNP", "", "http://b", "B", "", "")
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := spk.Volume(bg()); got != 3 {
		t.Fatalf("volume after the controller's restart = %d, want 3", got)
	}

	spk.releasedAt = spk.releasedAt.Add(-controllerRestartWindow)
	spk.ApplyKey("POWER")
	spk.ApplyKey("POWER")

	if got, _ := spk.Volume(bg()); got != 10 {
		t.Fatalf("a later wake left the volume at %d, want 10", got)
	}
}
