// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"testing"
)

// SetPaused is bookkeeping only: it flips the play status the UI reads, for the source
// that owns the amp, and never runs the transport hook (the command already went out).
func TestSetPausedTracksTheOwningSourceOnly(t *testing.T) {
	ctx := context.Background()
	s := New()
	hooked := 0
	s.OnTransport(func(string) error { hooked++; return nil })
	if err := s.SetSource(ctx, "SPOTIFY"); err != nil {
		t.Fatal(err)
	}

	s.SetPaused("AIRPLAY", true) // not the owner: ignored
	if got := s.StatusSnapshot().PlayStatus; got != "PLAY_STATE" {
		t.Fatalf("status after a stranger's pause = %q", got)
	}
	s.SetPaused("SPOTIFY", true)
	if got := s.StatusSnapshot().PlayStatus; got != "PAUSE_STATE" {
		t.Fatalf("status after pause = %q, want PAUSE_STATE", got)
	}
	s.SetPaused("SPOTIFY", false)
	if got := s.StatusSnapshot().PlayStatus; got != "PLAY_STATE" {
		t.Fatalf("status after resume = %q, want PLAY_STATE", got)
	}
	if hooked != 0 {
		t.Fatalf("transport hook ran %d times; SetPaused must not resend the command", hooked)
	}

	if err := s.EnterStandby(ctx); err != nil {
		t.Fatal(err)
	}
	s.SetPaused("STANDBY", true)
	if got := s.StatusSnapshot().PlayStatus; got == "PAUSE_STATE" {
		t.Fatal("a speaker in standby reported paused")
	}
}

// OnStandby fires once for every transition into standby, whatever caused it, and not
// for waking or for a standby request on a speaker already off.
func TestOnStandbyFiresForEveryWayIntoStandby(t *testing.T) {
	ctx := context.Background()
	s := New()
	fired := 0
	s.OnStandby(func() { fired++ })
	expect := func(want int, what string) {
		t.Helper()
		if fired != want {
			t.Fatalf("%s: hook fired %d times, want %d", what, fired, want)
		}
	}
	if err := s.EnterStandby(ctx); err != nil {
		t.Fatal(err)
	}
	expect(0, "standby while already off")
	_ = s.SetSource(ctx, "AUX")
	if err := s.EnterStandby(ctx); err != nil {
		t.Fatal(err)
	}
	expect(1, "EnterStandby")
	s.ApplyKey("POWER") // wake
	expect(1, "wake by POWER")
	s.ApplyKey("POWER") // off
	expect(2, "POWER key")
	_ = s.SetSource(ctx, "SPOTIFY")
	_ = s.ReleaseSource(ctx, "AIRPLAY") // stale: not the owner
	expect(2, "stale release")
	_ = s.ReleaseSource(ctx, "SPOTIFY")
	expect(3, "source session ended")
	_ = s.ReleaseSource(ctx, "STANDBY")
	expect(3, "release of STANDBY itself")
}

// Spotify sends metadata on a track change while paused too; only a resume lifts the
// pause.
func TestTrackMetaKeepsAPause(t *testing.T) {
	ctx := context.Background()
	sp := New()
	_ = sp.SetSource(ctx, "SPOTIFY")
	sp.SetPaused("SPOTIFY", true)
	_ = sp.SetTrackMeta(ctx, "SPOTIFY", "Next", "Artist", "")
	if n, _ := sp.NowPlaying(ctx); n.PlayStatus != "PAUSE_STATE" || n.Track != "Next" {
		t.Fatalf("after metadata while paused: %s %q", n.PlayStatus, n.Track)
	}
	sp.SetPaused("SPOTIFY", false)
	_ = sp.SetTrackMeta(ctx, "SPOTIFY", "Next", "Artist", "")
	if n, _ := sp.NowPlaying(ctx); n.PlayStatus != "PLAY_STATE" {
		t.Fatalf("after resume + metadata: %s", n.PlayStatus)
	}
}
