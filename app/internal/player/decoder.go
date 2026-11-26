// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/childproc"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/supervise"
)

// DecoderNice is the niceness the decoder runs at: below the Mixer and aplay, which must
// not run dry while ffmpeg has seconds of stream buffered (a pop on the ST20's one CPU,
// 2026-11-24).
const DecoderNice = "5"

// decoderCommand is the decoder process Run starts, run as user at DecoderNice. A variable
// so a test can start a stand-in instead of ffmpeg and drive the loop.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var decoderCommand = func(ctx context.Context, user, name string, args ...string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "nice", decoderArgs(name, args)...)

	return cmd, childproc.RunAs(cmd, user)
}

// decoderArgs is nice's command line for the decoder: nice execs it, so the process
// soundtapd stops is the decoder itself.
func decoderArgs(name string, args []string) []string {
	return append([]string{"-n", DecoderNice, name}, args...)
}

// tailMax bounds how long after a UPnP track's end the next one still continues
// behind it: longer than any jitter-buffer tail.
const tailMax = 30 * time.Second

// radioFadeIn is how long a station takes to come up to volume when it starts. Only
// the ffmpeg path (TuneIn, UPnP) gets it: Spotify and the other receivers do not pass
// through here. The curve is cubic: afade's default is linear in amplitude, which is
// -6 dB halfway and so already sounds almost full a few hundred milliseconds in --
// on the ST20 the 2 s linear fade could not be heard (2026-10-07).
const radioFadeIn = 3 * time.Second

// legacyConfirmAfter is how long ffmpeg must survive on the legacy path, where no PCM
// telemetry exists, before the station counts as playing.
const legacyConfirmAfter = 2 * time.Second

// confirmPoll is how often confirmPlayback looks at the PCM flow.
const confirmPoll = 250 * time.Millisecond

// playbackAudioFilter is ffmpeg's -af: the gain, and a fade-in over fade unless it is 0.
func playbackAudioFilter(gain float64, fade time.Duration) string {
	filter := fmt.Sprintf("volume=%.2f", gain)
	if fade > 0 {
		filter += fmt.Sprintf(",afade=t=in:d=%g:curve=cub", fade.Seconds())
	}

	return filter
}

// confirmPlayback watches real PCM flow and promotes the station from buffering to
// playing once audio genuinely reaches the DAC — a running ffmpeg only proves a socket
// was opened. If nothing arrives before bufferDeadline the attempt is reported as
// failed, so the UI can never spin forever.
func confirmPlayback(ctx context.Context, speakerState *speakerstate.Speaker, health func() audio.Health,
	playGen uint64, deadlineAfter time.Duration, failed chan<- uint64, log *slog.Logger,
) {
	if playGen == 0 {
		return // the source already changed hands; nothing to confirm
	}

	if health == nil {
		// Legacy direct-to-device path (no Mixer, so no PCM telemetry): fall back to
		// "ffmpeg survived its first couple of seconds".
		select {
		case <-ctx.Done():
			return
		case <-time.After(legacyConfirmAfter):
			speakerState.ConfirmPlaying(playGen)
		}

		return
	}

	base := health().SourceNonzeroBlocks

	deadline := time.NewTimer(deadlineAfter)
	defer deadline.Stop()

	tick := time.NewTicker(confirmPoll)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			// Standby or another selection superseded this attempt; nothing failed.
			if !speakerState.StillBuffering(playGen) {
				return
			}

			log.Warn("stream produced no audio", "after", deadlineAfter)
			// The player decides what the UI shows: a dropped station stays buffering.
			select {
			case failed <- playGen:
			default:
				speakerState.FailPlayback(playGen)
			}

			return
		case <-tick.C:
			if !speakerState.StillBuffering(playGen) {
				return
			}

			if health().SourceNonzeroBlocks-base >= confirmBlocks {
				speakerState.ConfirmPlaying(playGen)

				return
			}
		}
	}
}

func preemptSource(previous, source string, stopDecoder func(), processes *supervise.Processes) {
	stopDecoder() // decoder first: external takeover cannot overlap its writer
	processes.Preempt(previous, source)
}
