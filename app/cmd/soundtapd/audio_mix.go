// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// Audio arbiter wiring. Enabled only when AUDIO_MIX=1 (the firmware init sets it;
// without it soundtapd keeps the legacy direct-to-device path). When on, the Mixer is the single writer of /dev/snd/snd_shelby: every
// source funnels raw S16_LE PCM into the clock-corrected bus FIFO, and notifications duck the
// bus and play over the top — see internal/audio.
//
// AUDIO_MIX was the seam for the old Alpine QEMU smoke test (verify.sh,
// removed); the gate can go once nothing runs soundtapd without it.

const (
	defaultBus    = "/run/soundtap/source.pcm"
	ampPowerDelay = 3 * time.Second
	// ampMuteDelay is how long the amp stays unmuted after the pipeline stopped; muting on
	// the tick the source went away cut the amplifier with the last audio still in the DAC.
	// one value for every model. Stock's AmpMute delayDisable is shorter: 100 ms
	// in APConfig-rhino.xml (ST10), 500 ms in APConfig-spotty.xml and -mojo.xml (ST20/30).
	// Go per model if the slower mute ever matters; the sound itself does not change.
	ampMuteDelay = time.Second
	// sourceStartHold keeps an unmuted amp unmuted while a newly picked source starts.
	// A Spotify preset takes about two seconds to produce sound; muting after
	// ampMuteDelay and unmuting when it arrived made the amp pop on every switch.
	sourceStartHold = 5 * time.Second
)

var defaultDev = shelbyDev // the model's audio device (internal/hw/model)

func mixEnabled() bool { return os.Getenv("AUDIO_MIX") == "1" }

func busPath() string {
	if p := os.Getenv("AUDIO_BUS"); p != "" {
		return p
	}
	return defaultBus
}

// audioTap is what the rest of the daemon may do to the running Mixer: drop the audio
// queued between the bus writers and the DAC, and read PCM flow counters.
type audioTap struct {
	FlushWait    func(context.Context) error
	Flush        func()
	Health       audioHealth
	SetMultiroom func(bool)
	SetChannel   func(audio.Channel)
	FadeOut      func()
	// Downstream is how long PCM takes from the source bus to the DAC (runChainMeter).
	Downstream func() time.Duration
}

// noopTap is the tap the daemon runs with when there is no arbiter — either
// AUDIO_MIX is off or setup failed. Every field is non-nil: main wires these
// straight into callers that do not check.
func noopTap() audioTap {
	return audioTap{FlushWait: func(context.Context) error { return fmt.Errorf("audio mixer unavailable") }, Flush: func() {}, SetMultiroom: func(bool) {}, SetChannel: func(audio.Channel) {}, FadeOut: func() {}, Downstream: func() time.Duration { return 0 }}
}

// setupArbiter builds the Mixer + Arbiter, starts the DAC pump, and wires the
// notification hook and source-ownership tracker. Sink failures are supervised so
// the daemon keeps serving the API and can recover audio later.
func setupArbiter(ctx context.Context, sp *speakerstate.Speaker, leaveMultiroom func(), log *slog.Logger) audioTap {
	bus := busPath()
	if err := ensureFIFO(bus); err != nil {
		log.Error("source bus fifo", "path", bus, "err", err)
		return noopTap()
	}
	dev := os.Getenv("AUDIO_DEV")
	if dev == "" {
		dev = defaultDev
	}
	mix := audio.NewMixer(io.Discard, true)
	if board.Prebuffer > 0 {
		mix.SetPrebuffer(board.Prebuffer)
	}
	var shuttingDown atomic.Bool
	var ampDemand atomic.Int32
	var notificationReady atomic.Bool
	var delayBlocks atomic.Int64
	var tapOn atomic.Bool
	var downstream chainReport // from the source bus to the DAC
	outputRate := audio.SampleRate
	if rate, err := strconv.Atoi(os.Getenv("AUDIO_PHYSICAL_RATE")); err == nil && rate > 0 {
		outputRate = rate
		mix.SetOutputRate(rate)
	}
	sinkBytesPerSecond := audio.BytesPerSecond / audio.SampleRate * outputRate
	snapshot := sp.StatusSnapshot()
	mix.SetVolume(snapshot.Output()) // start at persisted controls, not full-scale
	mix.SetBass(snapshot.Bass)
	arb := audio.NewArbiter(mix, func(old string) {
		// Preempt the previous source so two daemons never write the bus at once.
		// Log-only until it is confirmed on hardware which signal each daemon
		// honours; runPlayer already yields the bus for TUNEIN, and external daemons
		// only emit during an active session, so double-writers are the rare case.
		log.Info("preempt source", "old", old)
	})

	go runMixerPump(ctx, mix, dev, bus, &delayBlocks, &tapOn, log)

	// The codec's gain pass needs the DAC clock the pump above provides — see codec_gain.go.
	// Then the volume moves into the codec, where stock has it: a step is heard at once
	// and the PCM keeps its full resolution at the lowest steps.
	// An image whose codec tool predates the coefficient argument refuses every try of the
	// first write and keeps the volume in the PCM. A write that fails only for want of the
	// clock is tried again, as the gain pass is.
	go applyCodecGain(ctx, mix.Health, log, func(command []string) {
		if err := writeCodecGainTries(ctx, command, 1, codecGainAttempts, mix.Health, log); err != nil {
			log.Warn("codec takes no volume steps; volume stays in the PCM", "err", err)
			return
		}
		failed := func() { mix.SetCodecVolume(nil, nil) }
		mix.SetCodecVolume(newCodecVolume(ctx, command, mix.Health, failed, log), func() time.Duration {
			return afterMixer(delayBlocks.Load())
		})
		log.Info("volume in the codec DSP")
	})

	// Volume is pushed, not polled. trackOwnership still reconciles it every 50 ms as
	// a safety net, but waiting for that tick put up to 50 ms between the key press
	// and the gain change, on top of the PCM already queued for the DAC. The hook runs
	// on the caller's goroutine (keypad, IR, API) and only takes the Mixer's mutex.
	sp.OnVolume(func(level int) { arb.SetVolume(soundbar.MixerLevel(level)) })

	var announcements announcementLifetime
	sp.OnNotify(func(n *speakerstate.Notification) {
		announcements.Start(ctx, func(announceCtx context.Context) {
			playAnnouncement(announceCtx, arb, n, &ampDemand, &notificationReady, log)
		})
	})
	sp.OnStandby(announcements.Stop)
	go runChainMeter(ctx, mix, bus, os.Getenv("AUDIO_TAP"), sinkBytesPerSecond, &tapOn, &delayBlocks, &downstream, log)
	go runTVShare(ctx, sp, os.Getenv("AUDIO_TAP"), &tapOn, log)
	go trackOwnership(ctx, sp, arb, mix.Health, &shuttingDown, &ampDemand, &notificationReady, leaveMultiroom, log)
	go logPCMHealth(ctx, mix, arb, sinkBytesPerSecond, log)

	log.Info("audio arbiter up", "dev", dev, "bus", bus)
	return audioTap{
		FlushWait: mix.FlushWait, Flush: mix.Flush, Health: mix.Health,
		FadeOut: func() {
			shuttingDown.Store(true)
			start := sp.StatusSnapshot().Output()
			for step := 9; step >= 0; step-- {
				arb.SetVolume(start * step / 10)
				time.Sleep(10 * time.Millisecond)
			}
			// The ramp happens in the Mixer, but up to ~1.3 s of PCM sits downstream of it
			// (delayWriter, the pipe, aplay's 1 s ring). Muting now cut that full-level tail
			// and popped; wait until the faded blocks have reached the DAC. The margin covers
			// the period aplay holds between its read and its write.
			time.Sleep(afterMixer(delayBlocks.Load()) + fadeMargin)
			amplifier.SetMute(true, log)
			time.Sleep(50 * time.Millisecond)
			amplifier.SetPowered(false, log)
		},
		SetMultiroom: func(enabled bool) {
			// Called on every WatchMultiroom tick: only a change resets the delay, or the
			// chain meter's correction would be undone every 3 s.
			if tapOn.Swap(enabled) == enabled {
				return
			}
			if enabled {
				// A first guess (740 ms + the ST10's ~260 ms ring = snapcastBuffer) until
				// runChainMeter has measured this speaker's chain.
				delayBlocks.Store(int64((snapcastBuffer - 260*time.Millisecond) / blockDuration))
			} else {
				delayBlocks.Store(0)
			}
		},
		SetChannel: mix.SetChannel,
		Downstream: downstream.Read,
	}
}

// runMixerPump feeds the source bus through the Mixer into the DAC and restarts it
// when the sink or the bus fails.
func runMixerPump(ctx context.Context, mix *audio.Mixer, dev, bus string, delayBlocks *atomic.Int64, tapOn *atomic.Bool, log *slog.Logger) {
	dacMissing := false
	for ctx.Err() == nil {
		if _, ok := pumpRest.awake(ctx); !ok {
			return
		}
		if _, err := os.Stat(dev); !strings.HasPrefix(dev, "alsa:") && os.IsNotExist(err) { //nolint:gosec // AUDIO_DEV comes from the init script
			if !dacMissing {
				log.Info("DAC unavailable; mixer paused", "dev", dev)
				dacMissing = true
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}
		dacMissing = false
		// The pump runs until standby has lasted pumpRestAfter; a wake ends the rest.
		pctx, cancel := context.WithCancel(ctx)
		_, _, changed := pumpRest.state()
		go func() {
			select {
			case <-changed:
				cancel()
			case <-pctx.Done():
			}
		}()
		err := pumpOnce(pctx, mix, dev, bus, delayBlocks, tapOn, log)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if rest, _, _ := pumpRest.state(); rest {
			log.Info("mixer pump rests in standby")
			continue
		}
		log.Warn("mixer pump exited, restarting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// pumpOnce opens the DAC and runs the Mixer on the bus until either fails.
func pumpOnce(ctx context.Context, mix *audio.Mixer, dev, bus string, delayBlocks *atomic.Int64, tapOn *atomic.Bool, log *slog.Logger) error {
	var sink io.Writer
	var paced bool
	var closeSink func() error
	var err error
	if strings.HasPrefix(dev, "alsa:") {
		sink, closeSink, err = openTimedSink(ctx, dev, mix.Flush, log)
	} else {
		sink, paced, closeSink, err = openAudioSink(ctx, dev, defaultSinkTiming, log)
	}
	if err != nil {
		log.Error("open DAC", "dev", dev, "err", err)
		return err
	}
	sink = &delayWriter{w: sink, blocks: delayBlocks}
	if tapPath := os.Getenv("AUDIO_TAP"); tapPath != "" {
		tap, tapErr := os.OpenFile(tapPath, os.O_RDWR|syscall.O_NONBLOCK, 0) //nolint:gosec // AUDIO_TAP comes from the init script
		if tapErr != nil {
			log.Warn("open audio tap", "path", tapPath, "err", tapErr)
		} else {
			setPipeSize(tap, audio.TapPipeBytes, "snapserver tap", log)
			primaryClose := closeSink
			sink = &tapWriter{primary: sink, tapFD: int(tap.Fd()), on: tapOn}
			closeSink = func() error {
				_ = tap.Close()
				return primaryClose()
			}
		}
	}
	mix.SetSink(sink, paced)
	// Run cannot interrupt a sink write that blocks (a DAC whose clock
	// stopped): closing the sink on cancellation releases it.
	closeSink = sync.OnceValue(closeSink)
	stopClose := context.AfterFunc(ctx, func() { _ = closeSink() })
	// Open the bus O_RDWR, not O_RDONLY: soundtapd then always holds a write handle, so
	// the FIFO never signals EOF when a source (e.g. TuneIn's ffmpeg on a station
	// switch, or an ended AirPlay session) closes its writer — the read just blocks
	// until the next source writes. With O_RDONLY the feeder would hit EOF on the first
	// gap and die, leaving the bus permanently silent. We never write this handle.
	r, openErr := os.OpenFile(bus, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if openErr != nil {
		err = openErr
	} else {
		// Keep the bus shallow: the jitter buffer that absorbs scheduling
		// noise now lives in the Mixer, where a flush can reach it. A deep
		// kernel pipe would just be un-droppable latency in front of it.
		setPipeSize(r, audio.BusPipeBytes, "source bus", log)
		err = mix.Run(ctx, r)
		_ = r.Close()
	}
	stopClose()
	if closeErr := closeSink(); err == nil {
		err = closeErr
	}
	return err
}

// healthyLogEvery is how often a clean 10 s window is still logged. At one ~430-byte line
// every 10 s, healthy playback filled two thirds of the 32 KiB log tail kept on flash for
// after a crash, pushing out the lines that explain it.
const healthyLogEvery = time.Minute

// logHealthWindow reports whether a 10 s window goes to the log: always when it had
// silence, an underrun or a new owner, and otherwise once per healthyLogEvery.
func logHealthWindow(owner, loggedOwner string, silence int64, underruns uint64, sinceLogged time.Duration) bool {
	return silence != 0 || underruns != 0 || owner != loggedOwner || sinceLogged >= healthyLogEvery
}

// logPCMHealth logs the Mixer's counters every 10 s while audio moves and something is
// off, and once a minute while it is clean.
//
// Health telemetry. The counters are cumulative, but every question anyone actually
// asks of them is about a rate: is the source keeping up with real time, and how much
// silence did the Mixer have to invent to cover it. Doing that arithmetic by hand off
// a log line is error-prone — two passes over the same log gave two different answers
// — so report the derived numbers alongside the raw ones.
func logPCMHealth(ctx context.Context, mix *audio.Mixer, arb *audio.Arbiter, sinkBytesPerSecond int, log *slog.Logger) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	last, lastAt := mix.Health(), time.Now()
	loggedOwner, loggedAt := "", time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			h := mix.Health()
			owner := arb.Owner()
			if owner != "" || h.SourceBytes != last.SourceBytes || h.SinkNonzeroBlocks != last.SinkNonzeroBlocks {
				elapsed := now.Sub(lastAt)
				// Blocks the Mixer wrote with no source audio behind them: the
				// audible dropouts, and not the same thing as Underruns, which only
				// counts the transition into a dry buffer and not the time spent
				// there.
				mixedBlocks := (h.MixedBytes - last.MixedBytes) / audio.BlockBytes
				silence := int64(mixedBlocks) - int64(h.MixedNonzeroBlocks-last.MixedNonzeroBlocks) //nolint:gosec // 10 s of blocks
				// The precise rates and the queue depth come first: the debug bundle
				// cuts long lines, and a drift of 0.5 % hides inside a rounded percent.
				perSec := func(d uint64) int64 { return int64(float64(d) / elapsed.Seconds()) }
				if !logHealthWindow(owner, loggedOwner, silence, h.Underruns-last.Underruns, now.Sub(loggedAt)) {
					last, lastAt = h, now
					continue
				}
				loggedOwner, loggedAt = owner, now
				log.Info("audio pcm health", "owner", owner,
					"silence_blocks", silence, "underruns_delta", h.Underruns-last.Underruns,
					"source_Bps", perSec(h.SourceBytes-last.SourceBytes),
					"mixed_Bps", perSec(h.MixedBytes-last.MixedBytes),
					"sink_Bps", perSec(h.SinkBytes-last.SinkBytes),
					"queued", mix.Queued(),
					"tap_dropped", tapDropped.Load(),
					"source_realtime_pct", realtimePct(h.SourceBytes-last.SourceBytes, elapsed, audio.BytesPerSecond),
					"sink_realtime_pct", realtimePct(h.SinkBytes-last.SinkBytes, elapsed, sinkBytesPerSecond),
					"underruns", h.Underruns,
					"source_bytes", h.SourceBytes, "source_nonzero_blocks", h.SourceNonzeroBlocks,
					"mixed_bytes", h.MixedBytes, "mixed_nonzero_blocks", h.MixedNonzeroBlocks,
					"sink_bytes", h.SinkBytes, "sink_nonzero_blocks", h.SinkNonzeroBlocks)
			}
			last, lastAt = h, now
		}
	}
}

// fadeMargin is added to the measured chain before FadeOut mutes.
const fadeMargin = 150 * time.Millisecond

// auxLiveBuffer is AUX's live buffer. The line input comes off the codec that plays it,
// one clock and no network, so the 2 s prebuffer (6 s on the ST20) only made a TV on
// AUX that late. Since the capture opens at the playback rate, 60 ms held a
// 30-minute loopback soak on the Wireless Link with no underrun; 200 ms before
// that was the biggest part of AUX's delay after the DAC's own ring.
const auxLiveBuffer = 60 * time.Millisecond

// liveBuffer is the Mixer's live buffer for source, 0 for a source it prebuffers.
func liveBuffer(source string) time.Duration {
	switch source {
	case sourceMultiroom:
		return audio.FollowerBuffer
	case sourceAux:
		return auxLiveBuffer
	}
	return 0
}

// announcementLifetime keeps one decoder alive. Cancelling its context closes
// ffmpeg's pipe and releases its goroutine, descriptor, and amp demand promptly.
type announcementLifetime struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (a *announcementLifetime) Start(ctx context.Context, play func(context.Context)) {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	playCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()
	go func() {
		defer cancel()
		play(playCtx)
	}()
}

// Stop ends the announcement that is playing, if any. Turning the speaker off silences
// it like any other sound. It only cancels a context, so it may run on the
// speaker state's goroutine.
func (a *announcementLifetime) Stop() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
}
