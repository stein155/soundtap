// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

// Source lifecycle: a new source replaces the old one; releasing it goes to standby.
// A late stop must never clobber whoever took over.
func TestSourceLifecycle(t *testing.T) {
	ctx := context.Background()
	src := func(sp *Speaker) string { s, _, _, _ := sp.PlaybackState(); return s }
	loc := func(sp *Speaker) string { _, l, _, _ := sp.PlaybackState(); return l }

	// tunein plays a station with a location to replay.
	tunein := func() *Speaker {
		sp := New()
		if err := sp.Select(ctx, "TUNEIN", "", "/station/s47309", "NPO Radio 2", "", "logo"); err != nil {
			t.Fatal(err)
		}
		return sp
	}

	t.Run("release stops instead of resuming interrupted TuneIn", func(t *testing.T) {
		sp := tunein()
		_ = sp.SetSource(ctx, "AIRPLAY")
		if src(sp) != "AIRPLAY" {
			t.Fatalf("acquire: source=%q", src(sp))
		}
		_ = sp.ReleaseSource(ctx, "AIRPLAY")
		if src(sp) != "STANDBY" || loc(sp) != "" {
			t.Fatalf("release: source=%q loc=%q, want standby", src(sp), loc(sp))
		}
	})

	t.Run("release with nothing to resume goes to standby", func(t *testing.T) {
		sp := New() // no resumable source
		_ = sp.SetSource(ctx, "BLUETOOTH")
		_ = sp.ReleaseSource(ctx, "BLUETOOTH")
		if src(sp) != "STANDBY" {
			t.Fatalf("release: source=%q, want STANDBY", src(sp))
		}
	})

	t.Run("stale stop does not clobber the new owner", func(t *testing.T) {
		sp := tunein()
		_ = sp.SetSource(ctx, "AIRPLAY")
		_ = sp.SetSource(ctx, "SPOTIFY")     // Spotify takes over
		_ = sp.ReleaseSource(ctx, "AIRPLAY") // late AirPlay stop — must be ignored
		if src(sp) != "SPOTIFY" {
			t.Fatalf("stale stop changed source to %q, want SPOTIFY", src(sp))
		}
	})

	t.Run("Spotify stop after TuneIn selection is stale", func(t *testing.T) {
		sp := New()
		_ = sp.SetSource(ctx, "SPOTIFY")
		_ = sp.Select(ctx, "TUNEIN", "", "/station/s6712", "Radio 2", "", "")
		_ = sp.ReleaseSource(ctx, "SPOTIFY")
		if src(sp) != "TUNEIN" || loc(sp) != "/station/s6712" {
			t.Fatalf("stale Spotify stop changed source=%q loc=%q", src(sp), loc(sp))
		}
	})

	t.Run("station switch replaces location", func(t *testing.T) {
		sp := tunein()
		_ = sp.Select(ctx, "TUNEIN", "", "/station/s6712", "Radio 2", "", "")
		if src(sp) != "TUNEIN" || loc(sp) != "/station/s6712" {
			t.Fatalf("station switch source=%q loc=%q", src(sp), loc(sp))
		}
	})

	t.Run("re-acquiring the same source still releases to standby", func(t *testing.T) {
		sp := tunein()
		_ = sp.SetSource(ctx, "AIRPLAY")
		_ = sp.SetSource(ctx, "AIRPLAY") // duplicate start is idempotent
		_ = sp.ReleaseSource(ctx, "AIRPLAY")
		if src(sp) != "STANDBY" {
			t.Fatalf("re-acquire release: source=%q, want standby", src(sp))
		}
	})
}

func TestStandbyWakesToResumableSource(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.Select(ctx, "TUNEIN", "", "/station/s47309", "NPO", "", ""); err != nil {
		t.Fatal(err)
	}
	_ = sp.EnterStandby(ctx)
	_ = sp.Key(ctx, "POWER") // wake

	np, _ := sp.NowPlaying(ctx)
	if np.Source != "TUNEIN" || np.Station != "NPO" {
		t.Fatalf("woke into %q %q, want the last station back", np.Source, np.Station)
	}
}

// AirPlay takes the station's place and leaves no location behind when it ends.
// POWER still leaves standby, but must not claim a dead external session or buffer
// an empty TuneIn location.
func TestStandbyWakesIdleAfterDeadSession(t *testing.T) {
	ctx := context.Background()
	sp := New()
	_ = sp.Select(ctx, "TUNEIN", "", "/station/s47309", "NPO", "", "")
	_ = sp.SetSource(ctx, "AIRPLAY") // external takes over, dropping the station
	_ = sp.EnterStandby(ctx)         // standby while AirPlay owns
	_ = sp.Key(ctx, "POWER")         // wake

	np, _ := sp.NowPlaying(ctx)
	if np.Source != "TUNEIN" || np.PlayStatus != "STOP_STATE" {
		t.Fatalf("woke into %q/%q, want idle TuneIn", np.Source, np.PlayStatus)
	}
}

// With a remembered station, the same wake plays it instead of idling
// (2026-10-25: the ST10's power key lit amber and played nothing after multiroom).
func TestStandbyWakesLastStationAfterDeadSession(t *testing.T) {
	ctx := context.Background()
	sp := New()
	sp.OnWakeStation(func() (string, string, string, bool) {
		return "/station/s17523", "NPO Radio 1", "logo", true
	})
	_ = sp.SetSource(ctx, "AIRPLAY")
	_ = sp.EnterStandby(ctx)
	_ = sp.Key(ctx, "POWER")

	np, _ := sp.NowPlaying(ctx)
	if np.Source != "TUNEIN" || np.Station != "NPO Radio 1" || np.PlayStatus != "BUFFERING_STATE" {
		t.Fatalf("woke into %q %q/%q, want NPO Radio 1 buffering", np.Source, np.Station, np.PlayStatus)
	}
}

// Standby from Bluetooth wakes on Bluetooth, paused until the phone streams, and runs the
// select hook that calls the phone back.
func TestStandbyFromBluetoothWakesOnBluetooth(t *testing.T) {
	ctx := context.Background()
	sp := New()
	calls := make(chan struct{}, 2)
	sp.OnBluetoothSelect(func() { calls <- struct{}{} })
	sp.OnBluetoothPaired(func() bool { return true })
	_ = sp.SetSource(ctx, "BLUETOOTH")
	<-calls // the select itself
	_ = sp.EnterStandby(ctx)
	_ = sp.Key(ctx, "POWER")

	np, _ := sp.NowPlaying(ctx)
	if np.Source != "BLUETOOTH" || np.PlayStatus != "PAUSE_STATE" {
		t.Fatalf("woke into %q/%q, want paused Bluetooth", np.Source, np.PlayStatus)
	}
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the wake did not run the Bluetooth select hook")
	}
}

// With nothing paired there is no phone to call back, so the wake goes to TuneIn.
func TestStandbyFromBluetoothWithoutPairingWakesOnTuneIn(t *testing.T) {
	ctx := context.Background()
	sp := New()
	sp.OnBluetoothSelect(func() {})
	sp.OnBluetoothPaired(func() bool { return false })
	_ = sp.SetSource(ctx, "BLUETOOTH")
	_ = sp.EnterStandby(ctx)
	_ = sp.Key(ctx, "POWER")

	np, _ := sp.NowPlaying(ctx)
	if np.Source != "TUNEIN" || np.PlayStatus != "STOP_STATE" {
		t.Fatalf("woke into %q/%q, want idle TuneIn", np.Source, np.PlayStatus)
	}
}

// The track and artist of what played before standby do not come along into the station
// a wake starts: a DLNA track's artist showed under the radio station.
func TestWakeDropsTheOldTrack(t *testing.T) {
	ctx := context.Background()
	sp := New()
	sp.OnWakeStation(func() (string, string, string, bool) {
		return "/station/s17523", "NPO Radio 1", "logo", true
	})
	_ = sp.Select(ctx, "UPNP", "", "http://192.168.1.9/a.mp3", "Tone", "", "")
	_ = sp.SetTrackMeta(ctx, "UPNP", "Song", "Some Artist", "")
	_ = sp.EnterStandby(ctx)
	_ = sp.Key(ctx, "POWER")

	np, _ := sp.NowPlaying(ctx)
	if np.Station != "NPO Radio 1" || np.Track != "" || np.Artist != "" {
		t.Fatalf("woke into %q with track %q by %q, want no track", np.Station, np.Track, np.Artist)
	}
}

func TestStoreCurrentAsPreset(t *testing.T) {
	ctx := context.Background()
	sp := New()
	_ = sp.Select(ctx, "TUNEIN", "", "/station/s6712", "Radio 2", "", "logo")

	if stored, err := sp.StoreCurrentAsPreset("PRESET_3"); err != nil || !stored {
		t.Fatal("hold-store on a playing TuneIn station should store")
	}
	ps, _ := sp.Presets(ctx)
	found := false
	for _, p := range ps {
		if p.Slot == 3 && p.Location == "/station/s6712" {
			found = true
		}
	}
	if !found {
		t.Fatalf("preset 3 not stored: %+v", ps)
	}
	// External source is not storable.
	_ = sp.SetSource(ctx, "AIRPLAY")
	if stored, err := sp.StoreCurrentAsPreset("PRESET_4"); err != nil || stored {
		t.Fatal("hold-store on AirPlay should be a no-op")
	}
}

func TestStoreCurrentUPnPAsPreset(t *testing.T) {
	ctx := context.Background()
	sp := New()
	_ = sp.Select(ctx, "UPNP", "audioItem", "http://media.local/song.flac", "Song", "", "cover")

	if stored, err := sp.StoreCurrentAsPreset("PRESET_2"); err != nil || !stored {
		t.Fatalf("store UPnP = %v, %v", stored, err)
	}
	_ = sp.EnterStandby(ctx)
	_ = sp.PlayPreset(ctx, 2)
	np, _ := sp.NowPlaying(ctx)
	if np.Source != "UPNP" || np.Station != "Song" {
		t.Fatalf("recalled UPnP preset = %+v", np)
	}
}

// SetTrackMeta (Spotify/librespot metadata) updates now-playing only for the source
// that owns the amp, and never in standby.
func TestSetTrackMeta(t *testing.T) {
	ctx := context.Background()
	np := func(sp *Speaker) (string, string) {
		n, _ := sp.NowPlaying(ctx)
		return n.Track, n.Artist
	}

	sp := New()
	_ = sp.SetSource(ctx, "SPOTIFY")
	_ = sp.SetTrackMeta(ctx, "SPOTIFY", "Song", "Artist", "http://cover")
	if tr, ar := np(sp); tr != "Song" || ar != "Artist" {
		t.Fatalf("meta not applied: track=%q artist=%q", tr, ar)
	}

	// A stale event for a source that no longer owns the amp is ignored.
	_ = sp.SetSource(ctx, "AIRPLAY")
	_ = sp.SetTrackMeta(ctx, "SPOTIFY", "Ghost", "Nobody", "")
	if tr, _ := np(sp); tr != "" {
		t.Fatalf("stale spotify meta painted over AIRPLAY: track=%q", tr)
	}

	// No metadata in standby.
	_ = sp.EnterStandby(ctx)
	_ = sp.SetTrackMeta(ctx, "", "X", "Y", "")
	if tr, _ := np(sp); tr != "" {
		t.Fatalf("meta applied in standby: track=%q", tr)
	}
}

// F25: shairport can push the first AirPlay track's metadata just before
// airplay-source.sh flips the source to AIRPLAY. The early bundle must not be lost —
// it's latched and applied when the source becomes AIRPLAY.
func TestAirplayMetaBeforeSourceFlip(t *testing.T) {
	ctx := context.Background()
	sp := New()
	_ = sp.SetSource(ctx, "TUNEIN") // not AIRPLAY yet

	// Metadata arrives before the source flip — dropped from the live display for now.
	_ = sp.SetTrackMeta(ctx, "AIRPLAY", "First Song", "First Artist", "")
	if n, _ := sp.NowPlaying(ctx); n.Track != "" {
		t.Fatalf("AirPlay meta leaked onto TUNEIN: track=%q", n.Track)
	}

	// Now the source flips to AIRPLAY — the latched track should appear.
	_ = sp.SetSource(ctx, "AIRPLAY")
	if n, _ := sp.NowPlaying(ctx); n.Track != "First Song" || n.Artist != "First Artist" {
		t.Fatalf("latched first-track meta not applied: track=%q artist=%q", n.Track, n.Artist)
	}

	// Leaving AIRPLAY drops the latch so it can't resurface on a later flip.
	_ = sp.SetSource(ctx, "TUNEIN")
	_ = sp.SetSource(ctx, "AIRPLAY")
	if n, _ := sp.NowPlaying(ctx); n.Track != "" {
		t.Fatalf("stale latch resurfaced on later AIRPLAY: track=%q", n.Track)
	}
}

// A released source must not leave its now-playing behind: /api/now is served from
// these fields, so the last AirPlay track used to sit next to a speaker that is off.
func TestReleaseSourceClearsNowPlaying(t *testing.T) {
	ctx := context.Background()
	sp := New()
	if err := sp.Select(ctx, "TUNEIN", "", "/station/s47309", "NPO Radio 2", "", "logo"); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetTrackMeta(ctx, "AIRPLAY", "Song", "Artist", "cover"); err != nil {
		t.Fatal(err)
	}
	if err := sp.ReleaseSource(ctx, "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	np, err := sp.NowPlaying(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if np.Track != "" || np.Artist != "" || np.Station != "" || np.Art != "" {
		t.Fatalf("standby still advertises track=%q artist=%q station=%q art=%q",
			np.Track, np.Artist, np.Station, np.Art)
	}
	if got := sp.StatusSnapshot().PlayStatus; got == "PLAY_STATE" {
		t.Fatalf("released source reports %q", got)
	}
}

// TestOnVolumeFiresImmediately pins the push path the audio arbiter depends on: the
// mixer's gain must follow a volume change at once. Polling it cost up to 50 ms on
// every key press, on top of the PCM already queued for the DAC.
func TestOnVolumeFiresImmediately(t *testing.T) {
	sp := New()
	var mu sync.Mutex
	var levels []int
	sp.OnVolume(func(level int) {
		mu.Lock()
		defer mu.Unlock()
		levels = append(levels, level)
	})
	got := func() []int {
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), levels...)
	}

	start := sp.StatusSnapshot().Volume
	sp.ApplyKey("VOLUME_UP")
	sp.ApplyKey("VOLUME_UP")
	sp.ApplyKey("VOLUME_DOWN")
	if want := []int{start + 1, start + 2, start + 1}; !slices.Equal(got(), want) {
		t.Fatalf("keypad levels = %v, want %v", got(), want)
	}

	// The API path fires too, so the app slider is as immediate as the keypad.
	if err := sp.SetVolume(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if last := got()[len(got())-1]; last != 42 {
		t.Errorf("after SetVolume(42) hook saw %d", last)
	}

	// A write that does not change the level must not fire: the mixer would take its
	// mutex 20 times a second for nothing while a slider is dragged at its limit.
	before := len(got())
	if err := sp.SetVolume(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	sp.ApplyKey("VOLUME_UP") // 43
	for range 100 {          // clamped at 100, so no further change
		sp.ApplyKey("VOLUME_UP")
	}
	sp.ApplyKey("VOLUME_UP")
	if n := len(got()) - before; n != 100-42 {
		t.Errorf("fired %d times, want %d (one per real change, none at the rail)", n, 100-42)
	}

	// MUTE is a volume change like any other: it must reach the gain immediately.
	before = len(got())
	sp.ApplyKey("MUTE")
	if levels := got(); len(levels)-before != 1 || levels[len(levels)-1] != 0 {
		t.Errorf("MUTE hook levels = %v, want one entry of 0", levels[before:])
	}
	sp.ApplyKey("MUTE")
	if last := got()[len(got())-1]; last != 100 {
		t.Errorf("unmute restored %d, want the muted-over 100", last)
	}
	before = len(got())
	sp.ApplyKey("MUTE")
	sp.ApplyKey("VOLUME_DOWN") // unmutes at the level, without a step
	if levels := got()[before:]; len(levels) != 2 || levels[0] != 0 || levels[1] != 100 {
		t.Errorf("mute then Vol- hook levels = %v, want [0 100]", levels)
	}
}
