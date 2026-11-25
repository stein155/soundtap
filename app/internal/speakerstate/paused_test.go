// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate_test

import (
	"context"
	"testing"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// SetPaused is bookkeeping only: it flips the play status the UI reads, for the source
// that owns the amp, and never runs the transport hook (the command already went out).
func TestSetPausedTracksTheOwningSourceOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spk := speakerstate.New()
	hooked := 0

	spk.OnTransport(func(string) error {
		hooked++

		return nil
	})

	err := spk.SetSource(ctx, "SPOTIFY")
	if err != nil {
		t.Fatal(err)
	}

	spk.SetPaused("AIRPLAY", true) // not the owner: ignored

	if got := spk.StatusSnapshot().PlayStatus; got != "PLAY_STATE" {
		t.Fatalf("status after a stranger's pause = %q", got)
	}

	spk.SetPaused("SPOTIFY", true)

	if got := spk.StatusSnapshot().PlayStatus; got != "PAUSE_STATE" {
		t.Fatalf("status after pause = %q, want PAUSE_STATE", got)
	}

	spk.SetPaused("SPOTIFY", false)

	if got := spk.StatusSnapshot().PlayStatus; got != "PLAY_STATE" {
		t.Fatalf("status after resume = %q, want PLAY_STATE", got)
	}

	if hooked != 0 {
		t.Fatalf("transport hook ran %d times; SetPaused must not resend the command", hooked)
	}

	err = spk.EnterStandby(ctx)
	if err != nil {
		t.Fatal(err)
	}

	spk.SetPaused("STANDBY", true)

	if got := spk.StatusSnapshot().PlayStatus; got == "PAUSE_STATE" {
		t.Fatal("a speaker in standby reported paused")
	}
}

// OnStandby fires once for every transition into standby, whatever caused it, and not
// for waking or for a standby request on a speaker already off.
func TestOnStandbyFiresForEveryWayIntoStandby(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spk := speakerstate.New()
	fired := 0

	spk.OnStandby(func() { fired++ })

	expect := func(want int, what string) {
		t.Helper()

		if fired != want {
			t.Fatalf("%s: hook fired %d times, want %d", what, fired, want)
		}
	}

	err := spk.EnterStandby(ctx)
	if err != nil {
		t.Fatal(err)
	}

	expect(0, "standby while already off")

	_ = spk.SetSource(ctx, "AUX")

	err = spk.EnterStandby(ctx)
	if err != nil {
		t.Fatal(err)
	}

	expect(1, "EnterStandby")
	spk.ApplyKey("POWER") // wake
	expect(1, "wake by POWER")
	spk.ApplyKey("POWER") // off
	expect(2, "POWER key")

	_ = spk.SetSource(ctx, "SPOTIFY")
	_ = spk.ReleaseSource(ctx, "AIRPLAY") // stale: not the owner

	expect(2, "stale release")

	_ = spk.ReleaseSource(ctx, "SPOTIFY")

	expect(3, "source session ended")

	_ = spk.ReleaseSource(ctx, "STANDBY")

	expect(3, "release of STANDBY itself")
}

// Spotify sends metadata on a track change while paused too; only a resume lifts the
// pause.
func TestTrackMetaKeepsAPause(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spk := speakerstate.New()
	_ = spk.SetSource(ctx, "SPOTIFY")
	spk.SetPaused("SPOTIFY", true)

	_ = spk.SetTrackMeta(ctx, "SPOTIFY", "Next", "Artist", "")
	if n, _ := spk.NowPlaying(ctx); n.PlayStatus != "PAUSE_STATE" || n.Track != "Next" {
		t.Fatalf("after metadata while paused: %s %q", n.PlayStatus, n.Track)
	}

	spk.SetPaused("SPOTIFY", false)

	_ = spk.SetTrackMeta(ctx, "SPOTIFY", "Next", "Artist", "")
	if n, _ := spk.NowPlaying(ctx); n.PlayStatus != "PLAY_STATE" {
		t.Fatalf("after resume + metadata: %s", n.PlayStatus)
	}
}
