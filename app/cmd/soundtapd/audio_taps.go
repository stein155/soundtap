// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"encoding/binary"
	"fmt"
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

// newDelayWriter delays what it writes to w by blocks blocks, starting with none queued.
func newDelayWriter(w io.Writer, blocks *atomic.Int64) *delayWriter {
	return &delayWriter{
		w: w, blocks: blocks,
		fifo: nil, free: nil, zero: nil, buf: nil, ramp: nil, samples: nil,
		playing: false, fadeIn: false,
	}
}

type delayed struct {
	pcm  []byte
	gain float64
}

func (w *delayWriter) Write(pcm []byte) (int, error) { return w.WriteGain(pcm, 1) }

// WriteGain delays each block together with its gain: the gain belongs to the samples,
// not to the moment they leave.
//
// A change of the delay (multiroom on or off, or the chain meter's correction) is a jump
// in the stream: growing it puts silence in the middle of the music, shrinking it skips
// blocks. A jump cut off mid-waveform is a click on the speaker, so the block that is
// cut off is faded out and the first one after the jump faded in, a block (about 20 ms)
// each.
func (w *delayWriter) WriteGain(pcm []byte, gain float64) (int, error) {
	want := int(w.blocks.Load())
	if want == 0 && len(w.fifo) == 0 && !w.fadeIn {
		w.playing = true

		written, err := audio.WriteGain(w.w, pcm, gain, &w.buf)
		if err != nil {
			return written, fmt.Errorf("delay: %w", err)
		}

		return written, nil
	}

	w.push(pcm, gain)

	switch {
	case len(w.fifo) > want+1:
		// Shrunk: the oldest block fades out, the ones the shorter delay skips go.
		err := w.writeFaded(w.fifo[0], fadeToSilence)
		if err != nil {
			return 0, err
		}

		w.pop(len(w.fifo) - want)
		w.playing, w.fadeIn = true, true
	case len(w.fifo) == want+1:
		block := w.fifo[0]

		var err error
		if w.fadeIn {
			err = w.writeFaded(block, fadeFromSilence)
		} else {
			_, err = audio.WriteGain(w.w, block.pcm, block.gain, &w.buf)
		}

		if err != nil {
			return 0, fmt.Errorf("delay: %w", err)
		}

		w.pop(1)
		w.playing, w.fadeIn = true, false
	case w.playing:
		// Grown while playing: the block that would have played next fades out; it plays
		// again, whole, once the delay has filled.
		err := w.writeFaded(w.fifo[0], fadeToSilence)
		if err != nil {
			return 0, err
		}

		w.playing, w.fadeIn = false, true
	default:
		if len(w.zero) != len(pcm) {
			w.zero = make([]byte, len(pcm))
		}

		_, err := audio.WriteGain(w.w, w.zero, 1, &w.buf)
		if err != nil {
			return 0, fmt.Errorf("delay silence: %w", err)
		}
	}

	return len(pcm), nil
}

// push queues a copy of pcm. The converter in front emits 937 and 938 frame blocks in turn,
// so each block keeps its own length.
func (w *delayWriter) push(pcm []byte, gain float64) {
	var reused []byte
	if n := len(w.free); n > 0 {
		reused, w.free = w.free[n-1][:0], w.free[:n-1]
	}

	w.fifo = append(w.fifo, delayed{pcm: append(reused, pcm...), gain: gain})
}

// pop drops the n oldest blocks.
func (w *delayWriter) pop(n int) {
	for _, b := range w.fifo[:n] {
		w.free = append(w.free, b.pcm)
	}

	w.fifo = append(w.fifo[:0], w.fifo[n:]...)
}

// writeFaded writes block with a linear ramp from or to silence.
func (w *delayWriter) writeFaded(block delayed, direction fadeDirection) error {
	w.ramp = append(w.ramp[:0], block.pcm...)
	w.samples = fade(w.ramp, direction, w.samples)

	_, err := audio.WriteGain(w.w, w.ramp, block.gain, &w.buf)
	if err != nil {
		return fmt.Errorf("delay fade: %w", err)
	}

	return nil
}

// fadeDirection is which way fade ramps.
type fadeDirection int

const (
	fadeFromSilence fadeDirection = iota // from silence to full: the first frame is zero
	fadeToSilence                        // from full to silence: the last frame is zero
)

// fade ramps 48 kHz S16_LE stereo in place, frame by frame: from silence to full, or from
// full to silence, so the first or the last frame is zero. Bytes after the last whole
// frame are left alone. samples is scratch space, returned for reuse.
func fade(pcm []byte, direction fadeDirection, samples []int16) []int16 {
	const (
		channels       = 2
		bytesPerSample = 2 // S16_LE
	)

	frames := len(pcm) / (channels * bytesPerSample)
	if cap(samples) < frames*channels {
		samples = make([]int16, frames*channels)
	}

	samples = samples[:frames*channels]

	_, err := binary.Decode(pcm, binary.LittleEndian, samples)
	if err != nil {
		return samples // cannot happen: pcm holds at least frames whole frames
	}

	for frame := range frames {
		// 0 <= gain <= 1, so every scaled sample stays within int16.
		gain := float64(frame) / float64(max(frames-1, 1))
		if direction == fadeToSilence {
			gain = 1 - gain
		}

		for c := range channels {
			s := &samples[frame*channels+c]
			*s = int16(math.Round(float64(*s) * gain))
		}
	}

	_, err = binary.Encode(pcm, binary.LittleEndian, samples)
	if err != nil {
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

// newTapWriter writes to primary and, while on, taps every block into the pipe tapFD.
func newTapWriter(primary io.Writer, tapFD int, on *atomic.Bool) *tapWriter {
	return &tapWriter{primary: primary, tapFD: tapFD, on: on, buf: nil, quiet: 0}
}

// tapSilenceHold is how much digital silence still goes to snapserver. The Mixer feeds the
// DAC silence in standby, and with followers connected the tap sent all of it: a WLA
// leader in standby streamed ~430 KB/s over Wi-Fi and spent ~15 % CPU in snapserver, its
// followers ~6 % each (2026-10-30). With the pipe quiet snapserver sends nothing; the
// first block with sound goes out at once. Gaps between tracks are much shorter.
const tapSilenceHold = 5 * audio.BytesPerSecond

func (w *tapWriter) Write(pcm []byte) (int, error) { return w.WriteGain(pcm, 1) }

// WriteGain writes the block to the speaker's own output; the tap got it already.
func (w *tapWriter) WriteGain(pcm []byte, gain float64) (int, error) {
	written, err := audio.WriteGain(w.primary, pcm, gain, &w.buf)
	if err != nil {
		return written, fmt.Errorf("primary output: %w", err)
	}

	return written, nil
}

// Tap gets the block before bass (audio.Tapper); the Mixer calls it right before
// WriteGain with the same samples otherwise.
func (w *tapWriter) Tap(pcm []byte) {
	// While the ST300 shares its TV input, shareTV feeds the tap instead.
	if !w.on.Load() || tvSharing.Load() {
		return
	}

	if audio.AllZero(pcm) {
		if w.quiet += len(pcm); w.quiet > tapSilenceHold {
			return
		}
	} else {
		w.quiet = 0
	}

	writeTap(w.tapFD, pcm)
}

// writeTap writes one block to the non-blocking tap. A full pipe drops the block: the
// followers hear a hole.
func writeTap(fd int, p []byte) {
	if n, _ := syscall.Write(fd, p); n < len(p) {
		tapDropped.Add(uint64(len(p) - max(n, 0))) //nolint:gosec // n <= len(p), so the difference is not negative
	}
}

// tapDropped counts the bytes the snapserver tap could not take.
//
//nolint:gochecknoglobals // process-wide state: the Mixer's tap adds, the health report reads
var tapDropped atomic.Uint64
