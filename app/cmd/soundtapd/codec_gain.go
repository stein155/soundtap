// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// The TLV320AIC3256's full-scale DSP pass — aic3256-loader --volume — ends in a coefficient
// buffer flip, and the codec only performs that flip while its DSP is clocked, which on
// this board means the McASP is streaming. The firmware init runs the pass during
// bring-up, when nothing is playing and there is therefore no clock: it polls 200 times,
// times out, and the speaker comes up at default gain. Every source then plays perfectly
// and is completely inaudible, which is what makes this so confusing to debug — the PCM
// counters all advance and the silence is two stages further down, in the codec.
//
// On mainline the DAC is an ALSA device (AUDIO_DEV=alsa:...) and the Mixer may write a
// FIFO in front of it, so wait for ALSA to report the PCM as RUNNING. Only the legacy
// direct sink can use the Mixer's byte counter. There is no /dev/snd/snd_shelby on
// mainline; gating on it skipped this pass on every mainline speaker.

const aicLoader = "/usr/sbin/aic3256-loader"

// The ST20's mainline kernel may come up without the SPI controller the loader needs. The
// boot script then configures the codec over GPIO with aic3256-bitbang and exports
// AIC_GPIO_SPI=1, and the gain pass has to take the same road: the loader fails at once
// without its device, every time, and the amplifier stays muted for good. The boot
// script's choice is the one to follow; guessing from /dev/spidev0.1 sent the ST10,
// whose loader talks to another bus, down the GPIO road too.
const aicBitbang = "/usr/sbin/aic3256-bitbang"

func codecOverSPI() bool { return os.Getenv("AIC_GPIO_SPI") != "1" }

const codecGainMarker = "/run/soundtap/codec-gain-ready"

// Written by the boot script when the codec accepted its configuration without answering a
// single read. See applyCodecGain.
const codecBlindMarker = "/run/soundtap/codec-blind"

var codecGainReady atomic.Bool

func codecOutputReady() bool { return codecGainReady.Load() }

const (
	// codecClockWait bounds how long we wait for the DAC to start clocking. If the sink
	// never opens, audio is broken for reasons this cannot fix and the retry is noise.
	codecClockWait = 90 * time.Second
	// codecGainAttempts/codecGainRetry: with the clock confirmed running the pass should
	// succeed the first time. The retries cover the codec being mid-reset.
	codecGainAttempts = 6
	codecGainRetry    = 5 * time.Second
)

// applyCodecGain brings the codec to full scale once the DAC is actually
// streaming. No-op off the mainline image (no loader binary) and when codec init was
// reported unavailable, in which case there is nothing to raise the gain on. onReady gets
// the command that worked, so the volume can go through the same road (ADR 0012).
func applyCodecGain(ctx context.Context, health audioHealth, log *slog.Logger, onReady func(command []string)) {
	if board.AICConfig == "" {
		codecGainReady.Store(true)
		return
	}
	// The boot script may have run the full-scale pass already (the ST10 does). The volume
	// still has to move into the codec, or it stays in the PCM and the lowest steps
	// crackle.
	passDone := fileExists(codecGainMarker)
	if passDone {
		codecGainReady.Store(true)
	}
	if health == nil || os.Getenv("AUDIO_NOINIT") == "1" {
		codecGainReady.Store(true)
		return
	}
	if _, err := os.Stat(aicLoader); err != nil {
		codecGainReady.Store(true)
		return // stock-kernel image: the codec is configured through sysfs instead
	}
	if !waitForDACClock(ctx, health, log) {
		return
	}
	// The boot script leaves this behind when the codec did not answer a single read, which
	// is this board's normal state. Every readback then costs 200 polls that end in
	// ETIMEDOUT, and this loop runs more than once, so take the blind path straight away
	// rather than walking the doomed one again per attempt.
	blind := fileExists(codecBlindMarker)
	if passDone {
		onReady(gainPassArgs(codecOverSPI(), fileExists(aicBitbang), blind))
		return
	}
	for attempt := 1; attempt <= codecGainAttempts; attempt++ {
		args := gainPassArgs(codecOverSPI(), fileExists(aicBitbang), blind)
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil && !blind && args[0] == aicLoader {
			blind = true
			args = []string{aicLoader, "--volume-blind"}
			out, err = exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		}
		if err == nil {
			_ = os.WriteFile(codecGainMarker, []byte("ready\n"), 0o600)
			codecGainReady.Store(true)
			log.Info("codec DSP full scale applied", "attempt", attempt)
			onReady(args)
			return
		}
		log.Warn("codec DSP gain pass failed", "attempt", attempt, "err", err,
			"out", strings.TrimSpace(string(out)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(codecGainRetry):
		}
	}
	log.Error("codec stays at default gain: the speaker will play silently")
}

// waitForDACClock reports whether PCM is reaching the DAC, which is the proxy for "the
// McASP is clocking the codec". It waits for the counter to *advance*, not merely to be
// non-zero: a stale count from a sink that has since died would pass that weaker test.
func waitForDACClock(ctx context.Context, health audioHealth, log *slog.Logger) bool {
	deadline := time.NewTimer(codecClockWait)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	base := health().SinkBytes
	statusPath := os.Getenv("AUDIO_CLOCK_STATUS")
	if statusPath == "" {
		statusPath = "/proc/asound/card0/pcm0p/sub0/status"
	}
	alsa := strings.HasPrefix(os.Getenv("AUDIO_DEV"), "alsa:")
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			// Nothing has played yet. Keep waiting: giving up here left the codec at
			// default gain and the amplifier muted until the next restart.
			log.Warn("no PCM reached the DAC yet; the codec gain pass waits for playback", "after", codecClockWait)
		case <-tick.C:
			if alsa && pcmRunning(statusPath) {
				return true
			}
			if !alsa && health().SinkBytes > base {
				return true
			}
		}
	}
}

func pcmRunning(path string) bool {
	status, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(status), "state: RUNNING")
}

// gainPassArgs is the command for the full-scale pass: the loader over SPI when the board
// has the device (or there is no GPIO tool to fall back on), aic3256-bitbang otherwise.
func gainPassArgs(spi, bitbang, blind bool) []string {
	if !spi && bitbang {
		return []string{aicBitbang, "--volume"}
	}
	if blind {
		return []string{aicLoader, "--volume-blind"}
	}
	return []string{aicLoader, "--volume"}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// codecVolume writes the master volume into the codec's DSP, as stock does (ADR 0012).
// The newest value wins: a held key asks faster than bit-banged SPI writes, and only
// the latest step matters. A write that fails codecWriteTries times calls failed once and
// stops: the Mixer then takes the volume back into the PCM, on top of the codec's last
// level, which can only make the speaker quieter than asked, never louder.
//
// Like stock APServer (APAudioParameterControl, a 250 ms volume timer), a change lands at
// once and the next one no sooner than codecVolumeGap later, holding only the newest
// value: a drag or held key then costs a few coefficient flips, not one per step.
type codecVolume struct{ want chan float64 }

const codecVolumeGap = 250 * time.Millisecond

func newCodecVolume(ctx context.Context, command []string, health audioHealth, failed func(), log *slog.Logger) *codecVolume {
	c := &codecVolume{want: make(chan float64, 1)}
	go func() {
		// The volume is reconciled every 50 ms; only a change costs a codec write.
		last := -1.0
		// The pump generation the last write ran under. It starts at the current one: the
		// gain pass already waited for the clock of this run, and a nil health (tests) has
		// no clock to wait for.
		_, ranUnder, _ := pumpRest.state()
		for {
			select {
			case <-ctx.Done():
				return
			case gain := <-c.want:
				if gain == last {
					continue
				}
				last = gain
				if !clockFor(ctx, &ranUnder, health, log) || !writeAndRest(ctx, command, gain, health, failed, log) {
					return
				}
			}
		}
	}()
	return c
}

// writeAndRest writes gain, then holds off codecVolumeGap. False ends the writer: ctx is
// done, or the write failed codecWriteTries times and the volume went back to the PCM.
func writeAndRest(ctx context.Context, command []string, gain float64, health audioHealth, failed func(), log *slog.Logger) bool {
	if err := writeCodecGainTries(ctx, command, gain, codecWriteTries, health, log); err != nil {
		log.Error("codec volume write failed; volume back in the PCM", "err", err)
		failed()
		return false
	}
	log.Debug("codec volume", "gain", gain)
	select {
	case <-ctx.Done():
		return false
	case <-time.After(codecVolumeGap):
		return true
	}
}

// clockFor waits until a codec write can land: the write needs the DAC clock, and while
// the pump rests there is none. A failed write would put the volume back in the PCM for
// good, so it waits for the pump and, on a run it has not written under yet, for the clock.
func clockFor(ctx context.Context, ranUnder *uint64, health audioHealth, log *slog.Logger) bool {
	gen, ok := pumpRest.awake(ctx)
	if !ok {
		return false
	}
	if gen != *ranUnder && !waitForDACClock(ctx, health, log) {
		return false
	}
	*ranUnder = gen
	return true
}

// codecWriteTries bounds the writes of one volume step. A write ends in the codec's
// coefficient flip, which only happens while the DAC is clocked, and an ALSA underrun
// stops the McASP for a moment: on the ST20, whose writes are bit-banged and slow, a busy
// start-up makes one fail now and then. One failure must not put the volume in the PCM for
// good, where the lowest steps crackle, so a failed write waits for the clock and tries
// again first.
const codecWriteTries = 3

// codecWriteRetry is the pause before a failed write is tried again; tests shorten it.
var codecWriteRetry = 2 * time.Second

// writeCodecGainTries writes gain, up to tries times, waiting for the DAC clock between
// tries. It returns the last error.
func writeCodecGainTries(ctx context.Context, command []string, gain float64, tries int, health audioHealth, log *slog.Logger) error {
	var err error
	for try := 1; ; try++ {
		if err = writeCodecGain(ctx, command, gain); err == nil || try >= tries {
			return err
		}
		log.Warn("codec volume write failed; trying again once the DAC is clocked", "try", try, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(codecWriteRetry):
		}
		if health != nil && !waitForDACClock(ctx, health, log) {
			return ctx.Err()
		}
	}
}

func writeCodecGain(ctx context.Context, command []string, gain float64) error {
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := append(slices.Clone(command[1:]), codecGainHex(gain))
	out, err := exec.CommandContext(wctx, command[0], args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *codecVolume) SetGain(gain float64) {
	for {
		select {
		case c.want <- gain:
			return
		default:
			select {
			case <-c.want:
			default:
			}
		}
	}
}

// codecGainHex is gain as the DSP's 48-bit volume coefficient, 0x400000000000 at unity.
func codecGainHex(gain float64) string {
	return fmt.Sprintf("%012x", uint64(math.Round(min(max(gain, 0), 1)*(1<<46))))
}
