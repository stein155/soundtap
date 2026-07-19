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
	sp := speakerstate.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runNoAudioWatchdog(ctx, sp, 50*time.Millisecond, quietLog())

	if err := sp.Select(context.Background(), "TUNEIN", "", "/station/s47309", "NPO Radio 2", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := sp.StatusSnapshot().PlayStatus; got != "BUFFERING_STATE" {
		t.Fatalf("playStatus right after selecting = %q, want BUFFERING_STATE", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sp.StatusSnapshot().PlayStatus == "STOP_STATE" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("still %q after the grace period; the UI would buffer forever", sp.StatusSnapshot().PlayStatus)
}

// The watchdog must not touch a source that is genuinely playing.
func TestNoAudioWatchdogLeavesPlayingSourcesAlone(t *testing.T) {
	sp := speakerstate.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runNoAudioWatchdog(ctx, sp, 50*time.Millisecond, quietLog())

	if err := sp.SetSource(context.Background(), "AIRPLAY"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := sp.StatusSnapshot().PlayStatus; got != "PLAY_STATE" {
		t.Fatalf("playStatus = %q, want PLAY_STATE left untouched", got)
	}
}
