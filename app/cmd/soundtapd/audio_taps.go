// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"encoding/binary"
	"io"
	"math"
	"sync/atomic"
	"syscall"

	"github.com/stein155/soundtap/app/internal/audio"
)

type delayWriter struct {
	w       io.Writer
	blocks  *atomic.Int64
	fifo    []delayed // oldest first
	free    [][]byte  // the buffers of blocks already played, for reuse
	zero    []byte
	buf     []byte
	ramp    []byte
	samples []int16 // fade's scratch space
	// playing: the last block written was audio, not the silence of a filling delay.
	// fadeIn: the stream was cut off; the next audio block starts from zero.
	playing, fadeIn bool
}

type delayed struct {
	pcm  []byte
	gain float64
}

func (w *delayWriter) Write(p []byte) (int, error) { return w.WriteGain(p, 1) }

// WriteGain delays each block together with its gain: the gain belongs to the samples,
// not to the moment they leave.
//
// A change of the delay (multiroom on or off, or the chain meter's correction) is a jump
// in the stream: growing it puts silence in the middle of the music, shrinking it skips
// blocks. A jump cut off mid-waveform is a click on the speaker, so the block that is
// cut off is faded out and the first one after the jump faded in, a block (about 20 ms)
// each.
func (w *delayWriter) WriteGain(p []byte, gain float64) (int, error) {
	want := int(w.blocks.Load())
	if want == 0 && len(w.fifo) == 0 && !w.fadeIn {
		w.playing = true
		return audio.WriteGain(w.w, p, gain, &w.buf)
	}
	w.push(p, gain)
	switch {
	case len(w.fifo) > want+1:
		// Shrunk: the oldest block fades out, the ones the shorter delay skips go.
		if err := w.writeFaded(w.fifo[0], false); err != nil {
			return 0, err
		}
		w.pop(len(w.fifo) - want)
		w.playing, w.fadeIn = true, true
	case len(w.fifo) == want+1:
		b := w.fifo[0]
		var err error
		if w.fadeIn {
			err = w.writeFaded(b, true)
		} else {
			_, err = audio.WriteGain(w.w, b.pcm, b.gain, &w.buf)
		}
		if err != nil {
			return 0, err
		}
		w.pop(1)
		w.playing, w.fadeIn = true, false
	case w.playing:
		// Grown while playing: the block that would have played next fades out; it plays
		// again, whole, once the delay has filled.
		if err := w.writeFaded(w.fifo[0], false); err != nil {
			return 0, err
		}
		w.playing, w.fadeIn = false, true
	default:
		if len(w.zero) != len(p) {
			w.zero = make([]byte, len(p))
		}
		if _, err := audio.WriteGain(w.w, w.zero, 1, &w.buf); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// push queues a copy of p. The converter in front emits 937 and 938 frame blocks in turn,
// so each block keeps its own length.
func (w *delayWriter) push(p []byte, gain float64) {
	var b []byte
	if n := len(w.free); n > 0 {
		b, w.free = w.free[n-1][:0], w.free[:n-1]
	}
	w.fifo = append(w.fifo, delayed{pcm: append(b, p...), gain: gain})
}

// pop drops the n oldest blocks.
func (w *delayWriter) pop(n int) {
	for _, b := range w.fifo[:n] {
		w.free = append(w.free, b.pcm)
	}
	w.fifo = append(w.fifo[:0], w.fifo[n:]...)
}

// writeFaded writes b with a linear ramp from silence (in) or to silence (out).
func (w *delayWriter) writeFaded(b delayed, in bool) error {
	w.ramp = append(w.ramp[:0], b.pcm...)
	w.samples = fade(w.ramp, in, w.samples)
	_, err := audio.WriteGain(w.w, w.ramp, b.gain, &w.buf)
	return err
}

// fade ramps 48 kHz S16_LE stereo in place, frame by frame: from silence to full (in), or
// from full to silence (out), so the first or the last frame is zero. Bytes after the last
// whole frame are left alone. samples is scratch space, returned for reuse.
func fade(pcm []byte, in bool, samples []int16) []int16 {
	const channels = 2
	n := len(pcm) / (channels * 2)
	if cap(samples) < n*channels {
		samples = make([]int16, n*channels)
	}
	samples = samples[:n*channels]
	if _, err := binary.Decode(pcm, binary.LittleEndian, samples); err != nil {
		return samples // cannot happen: pcm holds at least n whole frames
	}
	for i := range n {
		// 0 <= g <= 1, so every scaled sample stays within int16.
		g := float64(i) / float64(max(n-1, 1))
		if !in {
			g = 1 - g
		}
		for c := range channels {
			s := &samples[i*channels+c]
			*s = int16(math.Round(float64(*s) * g))
		}
	}
	if _, err := binary.Encode(pcm, binary.LittleEndian, samples); err != nil {
		return samples // cannot happen: samples came from pcm
	}
	return samples
}

// tapWriter never lets a slow Snapcast pipe stall the physical DAC. One PCM block is
// below PIPE_BUF, so the non-blocking tap write is either complete or safely dropped.
// The tap carries the mix before this speaker's volume and bass: every follower applies
// its own.
// With the leader's gain baked in, a follower scaled it a second time -- the WLA and the
// ST10 both at 10 left the ST10 at about 1%, silent (2026-09-29).
// With no follower the tap stays quiet: feeding it cost snapserver 13-15% CPU and its
// anchor client 4-5% on a speaker playing alone, which Spotify needed (2026-09-29).
type tapWriter struct {
	primary io.Writer
	tapFD   int
	on      *atomic.Bool
	buf     []byte
	quiet   int // bytes of digital silence in a row; only the Mixer's goroutine touches it
}

// tapSilenceHold is how much digital silence still goes to snapserver. The Mixer feeds the
// DAC silence in standby, and with followers connected the tap sent all of it: a WLA
// leader in standby streamed ~430 KB/s over Wi-Fi and spent ~15 % CPU in snapserver, its
// followers ~6 % each (2026-10-30). With the pipe quiet snapserver sends nothing; the
// first block with sound goes out at once. Gaps between tracks are much shorter.
const tapSilenceHold = 5 * audio.BytesPerSecond

func (w *tapWriter) Write(p []byte) (int, error) { return w.WriteGain(p, 1) }

func (w *tapWriter) WriteGain(p []byte, gain float64) (int, error) {
	return audio.WriteGain(w.primary, p, gain, &w.buf)
}

// Tap gets the block before bass (audio.Tapper); the Mixer calls it right before
// WriteGain with the same samples otherwise.
func (w *tapWriter) Tap(p []byte) {
	// While the ST300 shares its TV input, shareTV feeds the tap instead.
	if !w.on.Load() || tvSharing.Load() {
		return
	}
	if audio.AllZero(p) {
		if w.quiet += len(p); w.quiet > tapSilenceHold {
			return
		}
	} else {
		w.quiet = 0
	}
	writeTap(w.tapFD, p)
}

// writeTap writes one block to the non-blocking tap. A full pipe drops the block: the
// followers hear a hole.
func writeTap(fd int, p []byte) {
	if n, _ := syscall.Write(fd, p); n < len(p) {
		tapDropped.Add(uint64(len(p) - max(n, 0))) //nolint:gosec // n <= len(p), so the difference is not negative
	}
}

// tapDropped counts the bytes the snapserver tap could not take.
var tapDropped atomic.Uint64
