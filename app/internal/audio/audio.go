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
// or real device — see audio_internal_test.go. Run() adds the source-bus feeder and
// DAC pacing on top.
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

	// SinkBufferMicros and SinkPeriodMicros size the sink. Everything downstream of the
	// jitter buffer is audio a Flush can no longer reach, and the master volume is applied
	// before it too, so its depth is how late a volume key is heard. With aplay's defaults (500 ms ring drained in
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

// gainStep is how far the source-bus gain moves per block on its way to target.
const gainStep = (1.0 - duckLevel) / rampBlocks

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
	return &Mixer{
		sink:                sink,
		paced:               paced,
		outputRate:          SampleRate,
		sourceBytes:         atomic.Uint64{},
		sourceNonzeroBlocks: atomic.Uint64{},
		mixedBytes:          atomic.Uint64{},
		mixedNonzeroBlocks:  atomic.Uint64{},
		sinkBytes:           atomic.Uint64{},
		sinkNonzeroBlocks:   atomic.Uint64{},
		underruns:           atomic.Uint64{},
		flushGen:            atomic.Uint64{},
		flushAck:            atomic.Uint64{},
		flushWaitMu:         sync.Mutex{},
		flushWakeMu:         sync.Mutex{},
		bus:                 atomic.Pointer[os.File]{},
		queue:               atomic.Pointer[chan pcmBlock]{},
		live:                atomic.Int32{},
		prebuffer:           atomic.Int32{},
		held:                atomic.Bool{},
		mu:                  sync.Mutex{},
		shut:                0,
		closing:             false,
		srcHeard:            false,
		ann:                 nil,
		annStop:             nil,
		annBegun:            false,
		annGain:             0,
		srcTrim:             1.0,
		target:              1.0,
		curGain:             1.0,
		master:              1.0,
		curMaster:           1.0,
		volumeSet:           false,
		codec:               nil,
		codecDelay:          nil,
		boostSent:           0,
		boostAt:             0,
		channel:             atomic.Int32{},
		bassMu:              sync.Mutex{},
		bass:                0,
		bassFilter:          [Channels]biquad{},
	}
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

	blocks := make(chan pcmBlock, max(sourceBufferBlocks, m.refillTarget()+sourceBufferBlocks-prebufferBlocks))

	m.queue.Store(&blocks)
	defer m.queue.Store(nil)

	safego.Go("mixer.feed", func() { m.feed(fctx, src, blocks) })

	var state mixRun

	state.ch = blocks
	m.startRun(&state)

	pace := newPaceClock(blockFrames * time.Second / SampleRate)
	defer pace.stop()

	state.wait = time.NewTimer(sourceWait)
	if !state.wait.Stop() {
		<-state.wait.C
	}
	defer state.wait.Stop()

	state.out = make([]byte, blockBytes)
	state.seenFlush = m.flushGen.Load()

	// filling: hold the stream at silence until prebufferBlocks are queued, so the
	// buffer starts with slack in both directions. Set again after every underrun and
	// after every flush — track changes need less slack than startup/underruns.
	state.filling = true
	state.bufferTarget = m.refillTarget()
	state.fillGrew = time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		m.noteFlush(&state)

		state.checkFill()

		block, done := m.takeSource(ctx, &state)
		if done {
			return nil
		}

		block = m.shapeSource(&state, block)

		var mixed mixedBlock

		_, mixed.gain, mixed.tapped = m.stepInto(state.out, state.tapOut, block, state.deferGain)
		state.forceGap = false

		err := m.emit(&state, mixed)
		if err != nil {
			return err
		}

		if state.paced && !pace.wait(ctx) {
			return nil
		}
	}
}

// mixRun is what one Run carries from block to block.
type mixRun struct {
	ch        chan pcmBlock
	wait      *time.Timer
	sink      io.Writer
	paced     bool
	gainSink  GainWriter
	deferGain bool
	tapper    Tapper

	converter   *rateConverter
	tapConv     rateConverter
	tapConvLive bool // the announcement's frames are in converter's history

	sinkOut, tapOut, tapSinkOut []byte
	out                         []byte
	seenFlush                   uint64
	lastL, lastR                int16
	forceGap                    bool
	resuming                    bool // this block ends a fill: fade it in
	// filling: hold the stream at silence until bufferTarget blocks are queued.
	filling      bool
	bufferTarget int
	fillLen      int
	fillGrew     time.Time
	// settling counts down the source blocks a track still has to play since a flush
	// before an underrun counts as a real stall again.
	settling int
	// carry is the first block of a new stream, taken from the queue by a flush.
	carry   pcmBlock
	carried bool
}

// currentSink snapshots the output for one Run.
func (m *Mixer) currentSink() (io.Writer, bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.sink, m.paced, m.outputRate
}

// startRun snapshots the sink for one Run and sets up the buffers it needs.
func (m *Mixer) startRun(state *mixRun) {
	var outputRate int

	state.sink, state.paced, outputRate = m.currentSink()
	state.gainSink, state.deferGain = state.sink.(GainWriter)
	state.tapper, _ = state.sink.(Tapper)

	if state.tapper != nil {
		state.tapOut = make([]byte, blockBytes)
	}

	if outputRate != SampleRate {
		state.converter = newRateConverter(int64(outputRate))

		state.sinkOut = make([]byte, blockBytes)
		if state.tapper != nil {
			state.tapSinkOut = make([]byte, blockBytes)
		}
	}
}

// noteFlush starts a new stream in Run after a Flush.
func (m *Mixer) noteFlush(state *mixRun) {
	gen := m.flushGen.Load()
	if gen == state.seenFlush {
		return
	}

	state.seenFlush = gen
	state.carried = false // a second skip also retires a carried block
	// Drop the old stream only. A source that writes right after its flush (librespot
	// on a skip) has already queued the new track's first blocks behind it; they were
	// dropped too, and the track started with a stutter. Blocks queue in order, so
	// everything after the first new one is new as well.
	for len(state.ch) > 0 {
		if queued := <-state.ch; queued.gen >= gen {
			state.carry, state.carried = queued, true

			break
		}
	}

	state.filling = true
	// A new track starts after trackRestartBlocks, not after 80 ms: librespot hands
	// over its first blocks and then fetches more, so 80 ms in the track stalled and
	// every skip played a blip, a gap and then the track (ST300 2026-11-08: 1-3
	// underruns per skip). A live source keeps its own buffer: AUX's 80 ms after the
	// flush its source change makes sat at one block for as long as AUX played, and
	// raising a shorter one to 80 ms only made AUX later.
	state.bufferTarget = trackRestartBlocks
	if live := int(m.live.Load()); live > 0 {
		state.bufferTarget = live
	}

	state.forceGap = true
	state.settling = trackSettleBlocks
}

// mixedBlock is what stepInto made of one block, on its way to the sink.
type mixedBlock struct {
	gain    float64 // still to apply, by a GainWriter sink
	tapped  bool    // the tap has a block of its own: an announcement is mixed in
	nonzero bool    // the mixed block is not silence
}

// checkFill ends filling once enough is queued, and notes that the stream resumes.
func (state *mixRun) checkFill() {
	state.resuming = false

	if !state.filling || state.forceGap {
		return
	}

	queued := len(state.ch)
	if state.carried {
		queued++
	}

	if queued != state.fillLen {
		state.fillLen, state.fillGrew = queued, time.Now()
	}

	if queued >= state.bufferTarget || queued > 0 && time.Since(state.fillGrew) >= fillStall {
		state.filling = false
		state.resuming = true
	}
}

// takeSource takes the next source block unless Run is filling; done is ctx ending.
func (m *Mixer) takeSource(ctx context.Context, state *mixRun) ([]byte, bool) {
	if state.filling {
		return nil, false
	}

	var (
		next          pcmBlock
		stalled, done bool
	)
	if state.carried {
		next, state.carried = state.carry, false
	} else if next, stalled, done = takeBlock(ctx, state.ch, state.wait); done {
		return nil, true
	}

	if stalled {
		m.underruns.Add(1)
		counters.BufferUnderruns.Add(1) // /api/health (ADR 0127)

		state.filling = true

		state.bufferTarget = m.refillTarget()
		if state.settling > 0 {
			state.bufferTarget = min(state.bufferTarget, trackRestartBlocks)
		}
	}

	var block []byte
	if next.gen >= state.seenFlush { // older generations are the flushed stream's tail
		block = next.pcm
	}

	if len(block) > 0 && state.settling > 0 {
		state.settling--
	}

	return block, false
}

// shapeSource counts the source block and fades it in after a gap, or fades the last
// sample out when the source has none.
func (m *Mixer) shapeSource(state *mixRun, block []byte) []byte {
	if len(block) > 0 {
		m.sourceBytes.Add(uint64(len(block)))

		if nonzeroPCM(block) {
			m.sourceNonzeroBlocks.Add(1)
		}
	}

	if state.resuming && len(block) > 0 {
		fadeIn(block)
	} else if len(block) == 0 && (state.lastL != 0 || state.lastR != 0) {
		// Fade only the source. stepInto may add an announcement afterward; fading
		// the mixed output would punch a 20 ms hole into that announcement.
		fadeOut(state.out, state.lastL, state.lastR)
		block = state.out
	}

	if len(block) > 0 {
		last := len(block) - frameBytes
		state.lastL = int16(binary.LittleEndian.Uint16(block[last:]))
		state.lastR = int16(binary.LittleEndian.Uint16(block[last+bytesPerSamp:]))
	} else {
		state.lastL, state.lastR = 0, 0
	}

	return block
}

// emit counts the mixed block, converts it, taps it and writes it to the sink.
func (m *Mixer) emit(state *mixRun, mixed mixedBlock) error {
	m.mixedBytes.Add(uint64(len(state.out)))

	mixed.nonzero = nonzeroPCM(state.out)
	if mixed.nonzero {
		m.mixedNonzeroBlocks.Add(1)
	}

	sinkBlock, tapBlock, sinkSilent := state.convertOut(mixed)
	// Bass goes on after the tap and after rate conversion: the shelf sits far below
	// the 2% between 48000 Hz and a speaker's real clock.
	if state.tapper != nil {
		state.tapper.Tap(tapBlock)
	}
	// After the tap as well: a stereo pair's leader sends both channels and each
	// member keeps its own.
	m.applyChannel(sinkBlock)

	if m.applyBass(sinkBlock) {
		sinkSilent = false
	}
	// In standby every stage above passes silence through: no third scan for it.
	sinkNonzero := mixed.gain > 0 && !sinkSilent && nonzeroPCM(sinkBlock)

	err := m.writeSink(state, sinkBlock, mixed.gain)
	if err != nil {
		return err
	}

	if sinkNonzero {
		m.sinkNonzeroBlocks.Add(1)
	}

	return nil
}

// convertOut rate-converts the mixed block for the sink and the tap, and reports
// whether the sink's block is known to be silence.
func (state *mixRun) convertOut(mixed mixedBlock) ([]byte, []byte, bool) {
	if state.converter == nil {
		if mixed.tapped {
			return state.out, state.tapOut, !mixed.nonzero
		}

		return state.out, state.out, !mixed.nonzero
	}

	tapBlock, convertedTap := state.tapOut, false
	if mixed.tapped {
		tapBlock, convertedTap = state.convertAnnouncedTap(), true
	} else if state.tapConvLive {
		tapBlock, convertedTap = state.convertTapTail(), true
	}

	var (
		sinkBlock  []byte
		sinkSilent bool
	)
	if mixed.nonzero {
		sinkBlock, sinkSilent = state.converter.interpolate(state.sinkOut, state.out, widthS16, 0), false
	} else {
		sinkBlock, sinkSilent = state.converter.convertSilence(state.sinkOut, state.out)
	}

	if !mixed.tapped && !convertedTap {
		tapBlock = sinkBlock
	}

	return sinkBlock, tapBlock, sinkSilent
}

// convertAnnouncedTap rate-converts the tap's own block while an announcement is mixed in.
func (state *mixRun) convertAnnouncedTap() []byte {
	// Only around an announcement: a second converter does not run all along.
	// It starts as a copy, while the history still holds the source alone,
	// and stays separate so that the filter never mixes the clip into the tap.
	if !state.tapConvLive {
		state.tapConv, state.tapConvLive = *state.converter, true
	}

	return state.tapConv.convert(state.tapSinkOut, state.tapOut)
}

// convertTapTail runs the tap's converter one block past the announcement, until the
// clip has left the main converter's history.
func (state *mixRun) convertTapTail() []byte {
	tapBlock := state.tapConv.convert(state.tapSinkOut, state.out)
	state.tapConvLive = false

	return tapBlock
}

// writeSink writes one block to the sink, with the gain still to apply for a GainWriter.
func (m *Mixer) writeSink(state *mixRun, sinkBlock []byte, gain float64) error {
	var (
		written int
		err     error
	)
	if state.deferGain {
		written, err = state.gainSink.WriteGain(sinkBlock, gain)
	} else {
		written, err = state.sink.Write(sinkBlock)
	}

	if written > 0 {
		m.sinkBytes.Add(uint64(written))
	}

	if err != nil {
		return fmt.Errorf("dac write: %w", err) // surfaced so the pump can restart/log
	}

	if written != len(sinkBlock) {
		return fmt.Errorf("dac write: %w", io.ErrShortWrite)
	}

	return nil
}
