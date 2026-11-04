// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"context"
	"fmt"
	"time"
)

// Health is a cumulative PCM flow snapshot. Nonzero block counts distinguish real
// audio from the silence Run emits to keep the hardware clock alive.
type Health struct {
	SourceBytes         uint64
	SourceNonzeroBlocks uint64
	MixedBytes          uint64
	MixedNonzeroBlocks  uint64
	SinkBytes           uint64
	SinkNonzeroBlocks   uint64
	Underruns           uint64 // times the jitter buffer ran dry and silence was emitted
}

// Health reports PCM crossing the FIFO reader, mixer, and sink writer respectively.
func (m *Mixer) Health() Health {
	return Health{
		SourceBytes:         m.sourceBytes.Load(),
		SourceNonzeroBlocks: m.sourceNonzeroBlocks.Load(),
		MixedBytes:          m.mixedBytes.Load(),
		MixedNonzeroBlocks:  m.mixedNonzeroBlocks.Load(),
		SinkBytes:           m.sinkBytes.Load(),
		SinkNonzeroBlocks:   m.sinkNonzeroBlocks.Load(),
		Underruns:           m.underruns.Load(),
	}
}

// Queued is how many blocks wait in the jitter buffer: audio that is already off the
// source bus but not yet in the sink. Multiroom sync counts it as part of the chain.
func (m *Mixer) Queued() int {
	if ch := m.queue.Load(); ch != nil {
		return len(*ch)
	}
	return 0
}

// FollowerBuffer is the live refill target for a multiroom follower: the track-change
// buffer, since snapclient has already done the jitter buffering.
const FollowerBuffer = flushBufferBlocks * blockFrames * time.Second / SampleRate

// SetLive marks the source as one that already delivers each block when it is due, as
// a multiroom follower's snapclient does. Such a source gets the short track-change
// buffer instead of the 2 s prebuffer: snapclient has done the jitter buffering, and the
// prebuffer only made the follower 2 s late, more than Snapcast's latency can make up
// (2026-10-09: an ST10 follower measured 2.4 s after one underrun).
//
// A source going live also starts held: silent until Unhold. A follower's first seconds
// play before its leader has measured and set its latency, out of sync and with jumps
// at every correction; the project wants its sound on only once it is in sync.
//
// buffer is the refill target: FollowerBuffer for a follower, longer for a source whose
// blocks come in with more jitter. 0 ends live mode.
func (m *Mixer) SetLive(buffer time.Duration) {
	blocks := int32(buffer / (blockFrames * time.Second / SampleRate))
	live := blocks > 0
	m.live.Store(blocks)
	m.held.Store(live)
	m.mu.Lock()
	m.closing = live
	m.mu.Unlock()
}

// Unhold lets a held live source fade in over holdFade.
func (m *Mixer) Unhold() { m.held.Store(false) }

// Held reports whether a live source is still kept silent.
func (m *Mixer) Held() bool { return m.held.Load() }

// holdFade is how long a released live source takes to come up from silence.
const holdFade = time.Second

func (m *Mixer) refillTarget() int {
	if n := m.live.Load(); n > 0 {
		return int(n)
	}
	if n := m.prebuffer.Load(); n > 0 {
		return int(n)
	}
	return prebufferBlocks
}

// SetSourceGain sets the fixed input gain of the source now playing, as stock's
// wifiAttenuation/auxAttenuation. It scales the source only: announcements
// keep their own level, and the multiroom tap stays untrimmed so a follower applies
// its own source's gain, not the leader's as well.
func (m *Mixer) SetSourceGain(g float64) {
	m.mu.Lock()
	m.srcTrim = g
	m.mu.Unlock()
}

// SetPrebuffer replaces the 2 s startup/underrun prebuffer, for a model whose network
// stalls for longer than that (the ST20's own BCO Wi-Fi). Takes effect on the next Run.
func (m *Mixer) SetPrebuffer(d time.Duration) {
	m.prebuffer.Store(int32(d / (blockFrames * time.Second / SampleRate)))
}

// Flush drops the jitter buffer — the bulk of the audio queued for the DAC — without
// closing the PCM device, since a McASP stop/start is audible as a crack. It is what
// makes a track change stop the previous track instead of letting it play out.
//
// What survives is deliberately small and bounded rather than drained: the block the
// feeder has in hand (20 ms), the source FIFO (BusPipeBytes, 85 ms), the sink pipe
// (SinkPipeBytes, 43 ms) and the DAC ring (SinkBufferMicros, 320 ms) — about 470 ms
// in total, against roughly 1.4 s with the stock aplay and pipe sizes.
//
// The feeder also drops what sits in the bus FIFO at that moment. Call after the
// old writer stopped (preset switch: ffmpeg killed first; Spotify: the synchronous
// sink-close hook uses FlushWait before allowing the new track to write), so those bytes are the
// old stream's tail. Kept, they played as a blip of the previous station just before
// the new one began.
//
// Padding the flush with silence to "push the old audio out" was also tried and
// removed. It cannot work: the PCM downstream of this point is already past the gain
// stage and drains at real time whatever we write next, so silence does not replace
// it — it queues behind it. The listener heard the same old-track tail and then an
// extra gap as long as the silence, which on the ST10's direct-DAC path was 1.3 s
// against buffers that path does not even have. Shortening the sink ring is the only
// thing that actually reduces this tail.
func (m *Mixer) Flush() {
	m.flushWakeMu.Lock()
	defer m.flushWakeMu.Unlock()
	m.flushGen.Add(1)
}

// FlushWait is a producer barrier: the caller must stop writing old PCM before
// calling it and may write the new stream only after it returns successfully.
// Unlike Flush, it wakes an idle FIFO reader and waits until it has discarded the
// old bytes. Otherwise that reader can discard the new track's first packet later.
func (m *Mixer) FlushWait(ctx context.Context) error {
	m.flushWaitMu.Lock()
	defer m.flushWaitMu.Unlock()
	bus := m.bus.Load()
	if bus == nil {
		return fmt.Errorf("audio bus is not running")
	}
	m.flushWakeMu.Lock()
	gen := m.flushGen.Add(1)
	err := bus.SetReadDeadline(time.Now())
	m.flushWakeMu.Unlock()
	if err != nil {
		return fmt.Errorf("wake audio bus: %w", err)
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for m.flushAck.Load() < gen {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}
