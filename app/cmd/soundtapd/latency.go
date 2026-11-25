// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
)

// snapcastBuffer is snapserver.conf's [stream] buffer: a chunk the server reads at t is
// due at t+1000 ms on every client. The FLAC encoder (ADR 0013) holds up to one 1152-frame
// block (24 ms) before it sends, but snapserver stamps audio by when it was read, not when
// it was encoded, so the delay stays the same; the wait comes out of the followers' network
// headroom instead.
const snapcastBuffer = 1000 * time.Millisecond

// blockDuration is one Mixer block, the unit the leader's delay is set in.
const blockDuration = time.Duration(audio.BlockBytes) * time.Second / time.Duration(audio.BytesPerSecond)

// pipeMeter reads how much audio waits in one pipe.
type pipeMeter struct {
	fd             uintptr
	bytesPerSecond int
}

func (p *pipeMeter) delay() time.Duration {
	if p == nil || p.bytesPerSecond <= 0 {
		return 0
	}

	n, err := pipeQueued(p.fd)
	if err != nil {
		return 0
	}

	return time.Duration(n) * time.Second / time.Duration(p.bytesPerSecond)
}

// sinkPipe is aplay's stdin while the sink is open.
//
//nolint:gochecknoglobals // process-wide state: the sink sets it, the chain meter reads it
var sinkPipe atomic.Pointer[pipeMeter]

// openPipeMeter opens a FIFO's read end without blocking and without ever reading it, so
// FIONREAD has a descriptor. soundtapd already holds both FIFOs open, so this changes nothing
// for their writers.
func openPipeMeter(path string, bytesPerSecond int) *pipeMeter {
	if path == "" {
		return nil
	}

	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}

	return &pipeMeter{fd: uintptr(fd), bytesPerSecond: bytesPerSecond}
}

// alsaDelay is the PCM in the DAC ring: "delay" frames in the substream status, at the
// rate hw_params reports. Zero when the card is not running.
func alsaDelay(dir string) time.Duration {
	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return 0
	}

	params, err := os.ReadFile(filepath.Join(dir, "hw_params"))
	if err != nil {
		return 0
	}

	frames, rate := procField(string(status), "delay"), procField(string(params), "rate")
	if frames <= 0 || rate <= 0 {
		return 0
	}

	return time.Duration(frames) * time.Second / time.Duration(rate)
}

// procField reads "key : 123 ..." from an ALSA proc file.
func procField(text, key string) int {
	for line := range strings.SplitSeq(text, "\n") {
		k, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}

		if f := strings.Fields(rest); len(f) > 0 {
			n, _ := strconv.Atoi(f[0])

			return n
		}
	}

	return 0
}

// chainSample is one look at where the audio is.
type chainSample struct {
	sink     time.Duration // aplay's pipe and the DAC ring: Mixer output to the speaker
	upstream time.Duration // source bus FIFO and jitter buffer: bus to Mixer output
	backlog  time.Duration // the snapserver tap's pipe: Mixer output to snapserver's read
}

// leaderDelayBlocks is how long the leader holds its own output so it sounds when the
// followers do: they play a chunk snapcastBuffer after snapserver read it, and snapserver
// reads it backlog after the Mixer wrote it, while the leader's own copy still has sink
// ahead of it.
func leaderDelayBlocks(s chainSample) int64 {
	d := snapcastBuffer + s.backlog - s.sink
	if d <= 0 {
		return 0
	}

	return int64((d + blockDuration/2) / blockDuration)
}

// followerSettled is how little a follower's chain may move between two windows to
// count as settled: the leader's multiroom.LatencyStep, so it would send no further correction.
const followerSettled = 10 * time.Millisecond

// followerFirstTrusted is the first window with audio that is compared: the one before it
// compares against the idle chain.
const followerFirstTrusted = 2

// followerHoldMax is how many windows (2 s each while held) a follower waits at most.
const followerHoldMax = 15

// latencyApplied is how long after the leader read a follower's downstream its latency
// counts as set: the leader sends it to snapserver right after the read.
const latencyApplied = 500 * time.Millisecond

// chainReport is the downstream a follower publishes for its leader, and the value and
// time of the leader's last read of it.
type chainReport struct{ value, read, readAt atomic.Int64 }

// Read is what GET /api/multiroom/downstream serves; only the leader asks for it.
func (r *chainReport) Read() time.Duration {
	d := r.value.Load()
	r.read.Store(d)
	r.readAt.Store(time.Now().UnixNano())

	return time.Duration(d)
}

// publish sets the downstream and returns the one it replaces.
func (r *chainReport) publish(d time.Duration) time.Duration {
	return time.Duration(r.value.Swap(int64(d)))
}

// applied reports whether the leader has read, latencyApplied ago or longer, a value
// close enough to the published one that it sends no further correction.
func (r *chainReport) applied(now time.Time) bool {
	at := r.readAt.Load()
	off := time.Duration(r.read.Load() - r.value.Load())

	return at != 0 && off.Abs() < followerSettled && now.Sub(time.Unix(0, at)) >= latencyApplied
}

// followerHold decides when a held follower may be heard: once its chain has settled
// and its leader has set the latency for it. Waiting out fixed 5 s windows instead kept
// it silent for ~15 s after joining (2026-10-30).
type followerHold struct {
	windows int  // windows with audio while held
	settled bool // the last window's chain moved less than followerSettled
}

// window takes a window's chain against the one before; true means stop waiting. The
// first window with audio is never trusted: it compares against the idle chain.
func (h *followerHold) window(chain, prev time.Duration) bool {
	h.windows++
	h.settled = h.windows >= followerFirstTrusted && (chain-prev).Abs() < followerSettled

	return h.windows >= followerHoldMax
}

// meanSample averages: the DAC ring alone swings by a whole period (80 ms) between looks.
func meanSample(samples []chainSample) chainSample {
	var sum chainSample
	for _, s := range samples {
		sum.sink += s.sink
		sum.upstream += s.upstream
		sum.backlog += s.backlog
	}

	n := time.Duration(max(len(samples), 1))

	return chainSample{sink: sum.sink / n, upstream: sum.upstream / n, backlog: sum.backlog / n}
}

// alsaStatusDir is the playback substream whose status alsaDelay reads.
func alsaStatusDir() string {
	if p := os.Getenv("AUDIO_CLOCK_STATUS"); p != "" {
		return filepath.Dir(p)
	}

	return "/proc/asound/card0/pcm0p/sub0"
}

// afterMixer is the PCM the Mixer has already written that the DAC has not played yet:
// the leader's delay blocks, aplay's pipe and the DAC ring. Zero with the sink closed.
func afterMixer(delayBlocks int64) time.Duration {
	return time.Duration(delayBlocks)*blockDuration + sinkPipe.Load().delay() + alsaDelay(alsaStatusDir())
}

// chainPipes are the FIFOs runChainMeter measures: the source bus into the Mixer and the
// snapserver tap out of it, with the tap's byte rate.
type chainPipes struct {
	bus, tap          string
	tapBytesPerSecond int
}

// runChainMeter keeps downstream (what a follower reports to its leader) and, while this
// speaker leads, the leader's own delay in step with the measured chain. Before, the
// leader held a fixed 340 ms and followers counted their chain as zero; on the WLA, ST10
// and ST20 that left them 50 to 400 ms apart (2026-10-09).
func runChainMeter(ctx context.Context, mix *audio.Mixer, pipes chainPipes,
	tapOn *atomic.Bool, delayBlocks *atomic.Int64, downstream *chainReport, log *slog.Logger,
) {
	busMeter := openPipeMeter(pipes.bus, audio.BytesPerSecond)
	tapMeter := openPipeMeter(pipes.tap, pipes.tapBytesPerSecond)

	defer closePipeMeters(busMeter, tapMeter)

	alsaDir := alsaStatusDir()
	// A held follower samples five times as often, so its windows take 2 s, not 5 s.
	pace := map[bool]chainPace{
		false: {every: chainEvery, window: chainWindow},
		true:  {every: chainHeldEvery, window: chainHeldWindow},
	}

	ticker := time.NewTicker(pace[false].every)
	defer ticker.Stop()

	var (
		samples []chainSample
		hold    followerHold
	)

	held := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if nowHeld := mix.Held(); nowHeld != held {
			held, hold, samples = nowHeld, followerHold{windows: 0, settled: false}, samples[:0]
			ticker.Reset(pace[held].every)
		}
		// The leader polls every 3 s; once it has read a settled chain there is nothing
		// left to wait for.
		if held && hold.settled && downstream.applied(time.Now()) {
			mix.Unhold()
			log.Info("multiroom follower in sync",
				"downstream", time.Duration(downstream.value.Load()), "windows", hold.windows)
		}

		samples = append(samples, chainSample{
			sink:     sinkPipe.Load().delay() + alsaDelay(alsaDir),
			upstream: busMeter.delay() + time.Duration(mix.Queued())*blockDuration,
			backlog:  tapMeter.delay(),
		})
		if len(samples) < pace[held].window {
			continue
		}

		mean := meanSample(samples)
		samples = samples[:0]
		prev := downstream.publish(mean.upstream + mean.sink)
		// A chain that never settles is still played after followerHoldMax: late sound
		// beats none.
		if !held || mean.sink == 0 {
			hold = followerHold{windows: 0, settled: false}
		} else if hold.window(mean.upstream+mean.sink, prev) {
			mix.Unhold()
			log.Info("multiroom follower not settled, playing anyway", "downstream", mean.upstream+mean.sink)
		}

		if tapOn.Load() && mean.sink != 0 {
			adjustLeaderDelay(mean, delayBlocks, log)
		}
	}
}

// How often runChainMeter samples and how many samples make a window: a window takes 5 s,
// and 2 s for a held follower.
const (
	chainEvery      = 500 * time.Millisecond
	chainWindow     = 10
	chainHeldEvery  = 100 * time.Millisecond
	chainHeldWindow = 20
)

// chainPace is how often runChainMeter samples and how many samples make a window.
type chainPace struct {
	every  time.Duration
	window int
}

// leaderDelayStep is the smallest error in blocks worth moving the leader's delay for.
const leaderDelayStep = 2

// adjustLeaderDelay moves the leader's delay to what the measured chain asks for. Moving
// it drops or repeats PCM, so only a real error is worth it.
func adjustLeaderDelay(mean chainSample, delayBlocks *atomic.Int64, log *slog.Logger) {
	want, have := leaderDelayBlocks(mean), delayBlocks.Load()
	if want-have >= leaderDelayStep || have-want >= leaderDelayStep {
		delayBlocks.Store(want)
		log.Info("multiroom leader delay", "blocks", want, "sink", mean.sink, "backlog", mean.backlog)
	}
}

// closePipeMeters closes the descriptors of the meters that opened.
func closePipeMeters(meters ...*pipeMeter) {
	for _, meter := range meters {
		if meter != nil {
			_ = syscall.Close(int(meter.fd))
		}
	}
}
