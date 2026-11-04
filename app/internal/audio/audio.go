// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package audio is the single audio arbiter for soundtapd.
//
// The ST10 DAC (/dev/snd/snd_shelby) is an exclusive S16_LE 48 kHz stereo sink: only
// one writer at a time. Every logical source — TuneIn (soundtapd's own ffmpeg),
// AirPlay (shairport-sync), Bluetooth (bluealsa) and Spotify (librespot) — is
// therefore funnelled into ONE raw-PCM "source bus"; the Mixer is the only writer
// of the DAC. That gives two things the direct-to-device path could not:
//
//   - Exclusive ownership: the Arbiter tracks the single active source and can
//     preempt the previous owner's daemon when a new one starts.
//   - Sonos-style announcements: Announce() ducks the source bus down (linear
//     ramp), mixes an announcement clip over the top, then ramps back to full
//     when the clip ends — over any source, because they all share the bus.
//
// The mixing math (mixInto), the duck ramp (step) and the announce state machine
// are pure/synchronous so they run and are unit-tested under QEMU with no hardware
// or real device — see audio_test.go. Run() adds the source-bus feeder and DAC
// pacing on top.
package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/safego"
)

// PCM format on the source bus and nominal ALSA sink.
const (
	SampleRate   = 48000
	SinkRate     = 48000
	Channels     = 2
	bytesPerSamp = 2
	frameBytes   = Channels * bytesPerSamp // 4 bytes / stereo frame

	// blockFrames is one mix quantum (~20 ms). Small enough that a duck ramp and
	// announcement start feel instant; large enough that the loop is cheap.
	blockFrames = 960 // ~20 ms; keeping stereo blocks even simplifies every source
	blockBytes  = blockFrames * frameBytes

	// BlockBytes and BytesPerSecond let callers turn the Health byte counters into
	// "is this keeping up with real time", which is the only question those counters
	// are ever really asked.
	BlockBytes     = blockBytes
	BytesPerSecond = SampleRate * frameBytes // 192000

	// Jitter buffer between the source bus and the DAC. The bus writers (ffmpeg for
	// TuneIn, librespot/shairport/bluealsa through the ALSA file plugin) and the DAC
	// run on independent clocks, so the Mixer needs real elastic storage between them.
	// With the old 4-block (80 ms) queue and a non-blocking read, every scheduling
	// hiccup on the single-core A8 punched a 20 ms hole in the stream: the music both
	// stuttered *and* time-stretched, because the missing audio was delayed rather
	// than dropped. Run now waits for the source and re-prebuffers after a real
	// underrun instead of gapping block by block.
	sourceBufferBlocks = 150
	prebufferBlocks    = 100 // 2 s buffered before release; still starts within 10 s
	flushBufferBlocks  = 4   // 80 ms after a live source's flush; full prebuffer only at startup/underrun

	// A new track often stalls once just after it starts: librespot hands over its first
	// blocks, then fetches more. Refilling the full prebuffer for that (6 s on the ST20)
	// played 80 ms of the track, then seconds of silence, on every skip. An underrun in
	// a track's first trackSettleBlocks refills only trackRestartBlocks.
	trackSettleBlocks  = 150 // 3 s
	trackRestartBlocks = 25  // 500 ms

	// Never wait a full DAC period for late source PCM. The prebuffer absorbs ordinary
	// network jitter; after 5 ms emit faded silence so ALSA is never starved.
	sourceWait = 5 * time.Millisecond

	// A source that stops short of the prebuffer (a short clip, the tail after an
	// underrun) would otherwise wait forever for blocks that never come. Once the
	// queue has not grown for this long, what is queued plays anyway. A live stream adds
	// a block every 20 ms, so it never trips this.
	fillStall = 500 * time.Millisecond

	// Sink sizing. Everything downstream of the jitter buffer is audio a Flush can no
	// longer reach, and the master volume is applied before it too, so its depth is
	// how late a volume key is heard. With aplay's defaults (500 ms ring drained in
	// 125 ms chunks, plus a 64 KiB stdin pipe) roughly 1.4 s of the previous track
	// survived a track change; a 1 s ring here made every volume step lag that long.
	// 320 ms ran xrun-free on the ST20 (same single-core AM335x) through radio and
	// multiroom before it became the default for every model.
	SinkBufferMicros = 320_000 // aplay --buffer-time
	SinkPeriodMicros = 80000   // fewer DMA wakeups on the single-core ST10

	// SinkPipeBytes and BusPipeBytes cap the two kernel pipes in the chain (F_SETPIPE_SZ).
	// The bus FIFO is drained on flush, but keeping it tight also gives the bus writers
	// real back-pressure instead of letting them run 341 ms ahead.
	SinkPipeBytes = 8192  //  43 ms into aplay
	BusPipeBytes  = 16384 //  85 ms of source runway
	// TapPipeBytes is the snapserver tap's pipe, which the tap writes without blocking.
	// snapserver reads it in bursts: on an ST20 leading an ST300 its backlog swung between
	// 0 and 260 ms, and at the default 64 KiB (about 350 ms) the full pipe dropped PCM, 46 KB
	// in three minutes (2026-11-22). Every drop shortens the followers' stream against the
	// leader's, which plays it all, so they ran ahead until the chain meter caught up. The
	// leader's delay already follows the backlog, so room here costs no sync.
	TapPipeBytes = 262144 // about 1.4 s at 46875 Hz

	// duckLevel is where the source bus sits while an announcement plays (~-14 dB,
	// Sonos-ish). rampBlocks is how many blocks the gain takes to travel the full
	// 1.0<->duckLevel span (~160 ms), so the dip and recovery are audible fades,
	// not cuts.
	duckLevel  = 0.20
	rampBlocks = 8
)

var gainStep = (1.0 - duckLevel) / rampBlocks

// Mixer owns the DAC and mixes the source bus with an optional announcement clip.
type Mixer struct {
	sink       io.Writer
	paced      bool // true => pace the loop with a timer (sink doesn't block, e.g. QEMU)
	outputRate int  // measured physical rate; nominal source bus always stays 48 kHz

	sourceBytes         atomic.Uint64
	sourceNonzeroBlocks atomic.Uint64
	mixedBytes          atomic.Uint64
	mixedNonzeroBlocks  atomic.Uint64
	sinkBytes           atomic.Uint64
	sinkNonzeroBlocks   atomic.Uint64
	underruns           atomic.Uint64
	flushGen            atomic.Uint64
	flushAck            atomic.Uint64
	flushWaitMu         sync.Mutex
	flushWakeMu         sync.Mutex
	bus                 atomic.Pointer[os.File]
	queue               atomic.Pointer[chan pcmBlock] // the running jitter buffer
	live                atomic.Int32                  // SetLive refill target in blocks; 0 = not live
	prebuffer           atomic.Int32                  // SetPrebuffer override; 0 = prebufferBlocks
	held                atomic.Bool                   // live source kept silent until Unhold

	mu       sync.Mutex
	shut     float64       // how far the hold gate is closed: 0 open, 1 silent
	closing  bool          // SetLive closed the gate; the next block fades out or snaps
	srcHeard bool          // the last block carried the source above silence
	ann      <-chan []byte // blocks of the active announcement, nil when none
	annStop  chan struct{} // closed to retire the reader behind ann
	annBegun bool          // first block of the current clip has been taken
	annGain  float64
	srcTrim  float64 // SetSourceGain: the current source's fixed input gain
	target   float64 // source-bus gain the ramp is heading toward (1.0 or duckLevel)
	curGain  float64 // current source-bus gain
	master   float64 // user volume 0..1, applied to the source bus (all sources)
	// curMaster glides toward master a block at a time, so a big volume jump or a mute is a
	// short fade rather than a step the amp reproduces as a click. volumeSet is
	// false until the first SetVolume, which is taken at once: the start-up volume must
	// not fade down from full scale.
	curMaster float64
	volumeSet bool
	// codec, when set, does the master volume in the codec's DSP as stock does (ADR 0012):
	// the PCM stays at full scale and a volume step is heard at once instead of after the
	// sink buffer. boostSent is the level last scheduled for an announcement louder than
	// master, boostAt the one already reaching the DAC; 0 means none.
	codec      CodecVolume
	codecDelay func() time.Duration
	boostSent  float64
	boostAt    float64

	channel    atomic.Int32 // a Channel: which input channel this speaker plays (channel.go)
	bassMu     sync.Mutex
	bass       int
	bassFilter [Channels]biquad
}

// NewMixer returns a Mixer writing mixed PCM to sink. When paced, Run() clocks itself
// with a real-time (20 ms/block) ticker instead of relying on the sink to block at the
// sample rate. Production passes paced=true: a raw /dev/snd/snd_shelby write is NOT trusted
// to back-pressure (an on-box test stalled), and a self-clock is correct whether the
// DAC back-pressures (it becomes the slower gate) or not (the ticker sets the rate).
// paced also keeps Run() from spinning hot on a non-blocking sink like io.Discard.
func NewMixer(sink io.Writer, paced bool) *Mixer {
	return &Mixer{sink: sink, paced: paced, outputRate: SampleRate, target: 1.0, curGain: 1.0, master: 1.0, curMaster: 1.0, srcTrim: 1.0}
}

// SetOutputRate compensates a physical clock that differs from ALSA's nominal rate.
func (m *Mixer) SetOutputRate(rate int) {
	if rate <= 0 || rate > SampleRate {
		rate = SampleRate
	}
	m.mu.Lock()
	m.outputRate = rate
	m.mu.Unlock()
}

// SetSink replaces the output. The supervisor calls it after reopening the DAC and
// before the next Run; Run snapshots both fields at entry, so a sink swapped in
// while a Run is live takes effect on the following one rather than being read
// half-applied from under the mix loop.
func (m *Mixer) SetSink(sink io.Writer, paced bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sink = sink
	m.paced = paced
}

// currentSink snapshots the output for one Run.
func (m *Mixer) currentSink() (io.Writer, bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sink, m.paced, m.outputRate
}

// Run pumps the source bus through the Mixer to the DAC until ctx is cancelled.
// src is the raw-PCM source bus (a FIFO on hardware); when it has no data ready the
// Mixer emits silence so the DAC clock never starves and an announcement can still
// play with no source. Run returns once ctx is done and the sink write in flight, if
// any, has returned: a sink that can block must be closed on cancellation by its owner.
func (m *Mixer) Run(ctx context.Context, src io.Reader) error {
	// Own the feeder's lifetime so it doesn't leak if Run returns early (e.g. a sink
	// write error that the caller restarts on).
	fctx, fcancel := context.WithCancel(ctx)
	defer fcancel()
	ch := make(chan pcmBlock, max(sourceBufferBlocks, m.refillTarget()+sourceBufferBlocks-prebufferBlocks))
	m.queue.Store(&ch)
	defer m.queue.Store(nil)
	safego.Go("mixer.feed", func() { m.feed(fctx, src, ch) })

	sink, paced, outputRate := m.currentSink()
	gainSink, deferGain := sink.(GainWriter)
	tapper, _ := sink.(Tapper)
	var converter *rateConverter
	var tapConv rateConverter
	var tapConvLive bool // the announcement's frames are in converter's history
	var sinkOut, tapOut, tapSinkOut []byte
	if tapper != nil {
		tapOut = make([]byte, blockBytes)
	}
	if outputRate != SampleRate {
		converter = &rateConverter{outRate: int64(outputRate)}
		sinkOut = make([]byte, blockBytes)
		if tapper != nil {
			tapSinkOut = make([]byte, blockBytes)
		}
	}

	pace := newPaceClock(blockFrames * time.Second / SampleRate)
	defer pace.stop()

	wait := time.NewTimer(sourceWait)
	if !wait.Stop() {
		<-wait.C
	}
	defer wait.Stop()

	out := make([]byte, blockBytes)
	seenFlush := m.flushGen.Load()
	var lastL, lastR int16
	forceGap := false
	// filling: hold the stream at silence until prebufferBlocks are queued, so the
	// buffer starts with slack in both directions. Set again after every underrun and
	// after every flush — track changes need less slack than startup/underruns.
	filling := true
	bufferTarget := m.refillTarget()
	fillLen, fillGrew := 0, time.Now()
	// settling counts down the source blocks a track still has to play since a flush
	// before an underrun counts as a real stall again.
	settling := 0
	// carry is the first block of a new stream, taken from the queue by a flush.
	var carry pcmBlock
	carried := false

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if gen := m.flushGen.Load(); gen != seenFlush {
			seenFlush = gen
			carried = false // a second skip also retires a carried block
			// Drop the old stream only. A source that writes right after its flush (librespot
			// on a skip) has already queued the new track's first blocks behind it; they were
			// dropped too, and the track started with a stutter. Blocks queue in order, so
			// everything after the first new one is new as well.
			for len(ch) > 0 {
				if b := <-ch; b.gen >= gen {
					carry, carried = b, true
					break
				}
			}
			filling = true
			// A new track starts after trackRestartBlocks, not after 80 ms: librespot hands
			// over its first blocks and then fetches more, so 80 ms in the track stalled and
			// every skip played a blip, a gap and then the track (ST300 2026-11-08: 1-3
			// underruns per skip). A live source keeps its own buffer: AUX's 80 ms after the
			// flush its source change makes sat at one block for as long as AUX played, and
			// raising a shorter one to 80 ms only made AUX later.
			bufferTarget = trackRestartBlocks
			if live := int(m.live.Load()); live > 0 {
				bufferTarget = live
			}
			forceGap = true
			settling = trackSettleBlocks
		}

		var block []byte
		resuming := false
		if filling && !forceGap {
			queued := len(ch)
			if carried {
				queued++
			}
			if queued != fillLen {
				fillLen, fillGrew = queued, time.Now()
			}
			if queued >= bufferTarget || queued > 0 && time.Since(fillGrew) >= fillStall {
				filling = false
				resuming = true
			}
		}
		if !filling {
			var next pcmBlock
			var stalled, done bool
			if carried {
				next, carried = carry, false
			} else if next, stalled, done = takeBlock(ctx, ch, wait); done {
				return nil
			}
			if stalled {
				m.underruns.Add(1)
				counters.BufferUnderruns.Add(1) // /api/health (ADR 0127)
				filling = true
				bufferTarget = m.refillTarget()
				if settling > 0 {
					bufferTarget = min(bufferTarget, trackRestartBlocks)
				}
			}
			if next.gen >= seenFlush { // older generations are the flushed stream's tail
				block = next.pcm
			}
			if len(block) > 0 && settling > 0 {
				settling--
			}
		}
		if len(block) > 0 {
			m.sourceBytes.Add(uint64(len(block)))
			if nonzeroPCM(block) {
				m.sourceNonzeroBlocks.Add(1)
			}
		}

		if resuming && len(block) > 0 {
			fadePCM(block, true, 0, 0)
		} else if len(block) == 0 && (lastL != 0 || lastR != 0) {
			// Fade only the source. stepInto may add an announcement afterward; fading
			// the mixed output would punch a 20 ms hole into that announcement.
			fadePCM(out, false, lastL, lastR)
			block = out
		}
		if len(block) > 0 {
			last := len(block) - frameBytes
			lastL = int16(binary.LittleEndian.Uint16(block[last:]))
			lastR = int16(binary.LittleEndian.Uint16(block[last+bytesPerSamp:]))
		} else {
			lastL, lastR = 0, 0
		}
		_, gain, tapped := m.stepInto(out, tapOut, block, deferGain)
		forceGap = false
		m.mixedBytes.Add(uint64(len(out)))
		outNonzero := nonzeroPCM(out)
		if outNonzero {
			m.mixedNonzeroBlocks.Add(1)
		}
		sinkBlock, tapBlock := out, tapOut
		sinkSilent := !outNonzero
		convertedTap := false
		if converter != nil {
			switch {
			case tapped:
				// Only around an announcement: a second converter does not run all along.
				// It starts as a copy, while the history still holds the source alone,
				// and stays separate so that the filter never mixes the clip into the tap.
				if !tapConvLive {
					tapConv, tapConvLive = *converter, true
				}
				tapBlock, convertedTap = tapConv.convert(tapSinkOut, tapOut), true
			case tapConvLive:
				// One block more, until the clip has left the main converter's history.
				tapBlock, convertedTap = tapConv.convert(tapSinkOut, out), true
				tapConvLive = false
			}
			sinkBlock, sinkSilent = converter.convertKnown(sinkOut, out, !outNonzero)
		}
		if !tapped && !convertedTap {
			tapBlock = sinkBlock
		}
		// Bass goes on after the tap and after rate conversion: the shelf sits far below
		// the 2% between 48000 Hz and a speaker's real clock.
		if tapper != nil {
			tapper.Tap(tapBlock)
		}
		// After the tap as well: a stereo pair's leader sends both channels and each
		// member keeps its own.
		m.applyChannel(sinkBlock)
		if m.applyBass(sinkBlock) {
			sinkSilent = false
		}
		// In standby every stage above passes silence through: no third scan for it.
		sinkNonzero := gain > 0 && !sinkSilent && nonzeroPCM(sinkBlock)
		var n int
		var err error
		if deferGain {
			n, err = gainSink.WriteGain(sinkBlock, gain)
		} else {
			n, err = sink.Write(sinkBlock)
		}
		if n > 0 {
			m.sinkBytes.Add(uint64(n))
		}
		if err != nil {
			return fmt.Errorf("dac write: %w", err) // surfaced so the pump can restart/log
		}
		if n != len(sinkBlock) {
			return fmt.Errorf("dac write: %w", io.ErrShortWrite)
		}
		if sinkNonzero {
			m.sinkNonzeroBlocks.Add(1)
		}

		if paced && !pace.wait(ctx) {
			return nil
		}
	}
}
