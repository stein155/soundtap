// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// noAudioGrace is how long a source may sit in BUFFERING_STATE with no player running
// before the watchdog stops it. Long enough that a selection still shows as "starting",
// short enough that nobody waits for music that cannot arrive.
const noAudioGrace = 5 * time.Second

// noAudioPollsPerGrace is how many times runNoAudioWatchdog looks within the grace period.
const noAudioPollsPerGrace = 5

// runNoAudioWatchdog replaces the player when soundtapd runs with -no-audio. Nothing else
// ever calls ConfirmPlaying or FailPlayback in that mode, so a station selected on a
// board that brought up no sound card buffered forever. That is exactly what the WLA
// showed while its McASP card failed to bind: "TUNEIN (BUFFERING_STATE)" for as long as
// it was left alone. Report it as stopped instead, and say why in the log.
func runNoAudioWatchdog(ctx context.Context, speaker *speakerstate.Speaker, grace time.Duration, log *slog.Logger) {
	poll := min(grace/noAudioPollsPerGrace, time.Second)

	tick := time.NewTicker(poll)
	defer tick.Stop()

	var since time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		switch {
		case speaker.StatusSnapshot().PlayStatus != playBuffering:
			since = time.Time{}
		case since.IsZero():
			since = time.Now()
		case time.Since(since) >= grace:
			log.Warn("no audio device on this board; reporting playback as stopped")
			speaker.FailCurrentPlayback()

			since = time.Time{}
		default: // buffering, still within the grace period
		}
	}
}
