// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/supervise"
)

// realtimePct reports bytes as a percentage of the PCM a real-time stream would have
// carried in elapsed. 100 means keeping up; below that is audible.
func realtimePct(bytes uint64, elapsed time.Duration, bytesPerSecond int) int {
	want := float64(bytesPerSecond) * elapsed.Seconds()
	if want <= 0 {
		return 0
	}
	return int(float64(bytes) / want * 100)
}

// formatS32 is the sample format the McASP cards take.
const formatS32 = "S32_LE"

// alsaSinkFormat is the device, sample format and rate aplay plays this model's card at.
func alsaSinkFormat(target string) (string, string, int) {
	format, rate := "S16_LE", audio.SinkRate
	// The ST20's probe card takes S32_LE and nothing else, so there the raw device
	// is the right one. Its link clocks 46875 Hz like every other McASP here: aplay
	// asked for 48000 on hw:0,0 got 46875 without an error, played everything 2.3%
	// slow and drained the Mixer -- and with it the snapserver tap -- 23 ms a second
	// short, which is the onResync line snapserver logged every second.
	if os.Getenv("SOUNDTAP_FIRMWARE") == "1" && board.Name == model.ST20 && target == "plughw:0,0" {
		target, format, rate = "hw:0,0", formatS32, mcaspCardRate
	}
	// The mainline ST10 and WLA analog cards take S32_LE at 46875 Hz. Handing them
	// exactly that makes plughw a pass-through: its rate converter works in 16 bits,
	// and at low volume 16 bits is where the crackle came from. The widen, the gain
	// and the resample all happen in S32Writer instead. The WLA was left on S16 and
	// rounded quiet passages at 2 % to digital zero: 50 of 500 blocks per 10 s.
	if os.Getenv("SOUNDTAP_FIRMWARE") == "1" && (board.Name == model.ST10 || board.Name == model.WLA) && target == "plughw:0,0" {
		format, rate = formatS32, mcaspCardRate
	}
	return target, format, rate
}

func openAudioSink(ctx context.Context, dev string, timing sinkTiming, log *slog.Logger) (io.Writer, bool, func() error, error) {
	if dev == "-" {
		return io.Discard, true, func() error { return nil }, nil
	}
	if target, ok := strings.CutPrefix(dev, "alsa:"); ok { //nolint:nestif // moved unchanged
		if strings.TrimSpace(target) == "" {
			return nil, false, nil, fmt.Errorf("empty ALSA device")
		}
		device, format, rate := alsaSinkFormat(target)
		cmd := exec.CommandContext(ctx, "aplay", "-q", "-D", device, "-t", "raw", //nolint:gosec // AUDIO_DEV comes from the init script
			"-f", format, "-r", fmt.Sprint(rate), "-c", "2",
			fmt.Sprintf("--buffer-time=%d", timing.bufferMicros),
			fmt.Sprintf("--period-time=%d", timing.periodMicros))
		// aplay says "underrun!!!" for every xrun; /api/health counts them (ADR 0127).
		cmd.Stderr = counters.Matches(supervise.ChildLog("aplay"), "underrun!!!", &counters.ALSAUnderruns)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, false, nil, err
		}
		setPipeSize(stdin, timing.pipeBytes, "aplay stdin", log)
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			return nil, false, nil, err
		}
		if f, ok := stdin.(interface{ Fd() uintptr }); ok {
			frameBytes := 4
			if format == formatS32 {
				frameBytes = 8
			}
			sinkPipe.Store(&pipeMeter{fd: f.Fd(), bytesPerSecond: frameBytes * rate})
		}
		// ALSA is the hardware clock. A second ticker drifts against it and inserts
		// periodic silence blocks, heard as crackling during buffered streams.
		var sink io.Writer = stdin
		if format == formatS32 {
			sink = audio.NewS32Writer(stdin, rate)
		}
		return sink, false, func() error {
			sinkPipe.Store(nil)
			_ = stdin.Close()
			return cmd.Wait()
		}, nil
	}

	ensureDACStreaming(dev, log)
	f, err := os.OpenFile(dev, os.O_WRONLY, 0) //nolint:gosec // moved unchanged
	if err != nil {
		return nil, false, nil, err
	}
	return f, true, f.Close, nil
}

// mcaspCardRate is the rate the mainline ST10 and ST20 McASP links clock: 46875 Hz,
// measured from hw_ptr on both, and nothing else.
const mcaspCardRate = 46875
