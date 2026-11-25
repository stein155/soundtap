// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// A board with no sound card runs soundtapd with -no-audio, so no player ever confirms
// or fails playback. Selecting a station must not leave the UI buffering forever.
func TestNoAudioWatchdogStopsEndlessBuffering(t *testing.T) {
	t.Parallel()

	speaker := speakerstate.New()

	ctx := t.Context()

	go runNoAudioWatchdog(ctx, speaker, 50*time.Millisecond, quietLog())

	err := speaker.Select(context.Background(), "TUNEIN", "", "/station/s47309", "NPO Radio 2", "", "")
	if err != nil {
		t.Fatal(err)
	}

	if got := speaker.StatusSnapshot().PlayStatus; got != "BUFFERING_STATE" {
		t.Fatalf("playStatus right after selecting = %q, want BUFFERING_STATE", got)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if speaker.StatusSnapshot().PlayStatus == "STOP_STATE" {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("still %q after the grace period; the UI would buffer forever", speaker.StatusSnapshot().PlayStatus)
}

// The watchdog must not touch a source that is genuinely playing.
func TestNoAudioWatchdogLeavesPlayingSourcesAlone(t *testing.T) {
	t.Parallel()

	speaker := speakerstate.New()

	ctx := t.Context()

	go runNoAudioWatchdog(ctx, speaker, 50*time.Millisecond, quietLog())

	err := speaker.SetSource(context.Background(), "AIRPLAY")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	if got := speaker.StatusSnapshot().PlayStatus; got != "PLAY_STATE" {
		t.Fatalf("playStatus = %q, want PLAY_STATE left untouched", got)
	}
}
