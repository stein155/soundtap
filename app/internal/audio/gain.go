// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"slices"
)

// GainWriter is a sink that applies the Mixer's gain itself. The Mixer then hands it
// blocks kept near full scale plus the gain still to apply, so a sink with a wider
// sample format keeps low-volume audio above the 16-bit noise floor.
type GainWriter interface {
	WriteGain(p []byte, gain float64) (int, error)
}

// Tapper is a sink that also feeds a multiroom tap. The Mixer hands it each output block
// before this speaker's bass, so followers apply their own bass rather than the leader's
// on top of it.
type Tapper interface {
	Tap(p []byte)
}

// WriteGain passes p and gain on to w. A GainWriter applies the gain itself; any other
// writer gets p scaled to S16 in *buf first, which is what the Mixer would have done.
func WriteGain(writer io.Writer, block []byte, gain float64, buf *[]byte) (int, error) {
	if g, ok := writer.(GainWriter); ok {
		written, err := g.WriteGain(block, gain)
		if err != nil {
			return written, fmt.Errorf("write with gain: %w", err)
		}

		return written, nil
	}

	if gain == 1 {
		written, err := writer.Write(block)
		if err != nil {
			return written, fmt.Errorf("write: %w", err)
		}

		return written, nil
	}

	*buf = slices.Grow((*buf)[:0], len(block))[:len(block)]
	scaleInto(*buf, block, gain)

	written, err := writer.Write(*buf)
	if err != nil {
		return written, fmt.Errorf("write scaled: %w", err)
	}

	return written, nil
}

// S32Writer widens the Mixer's 48 kHz S16_LE to S32_LE at rate, applying the gain
// after widening and resampling in 64-bit so nothing is rounded to 16 bits on the way.
type S32Writer struct {
	w    io.Writer
	conv *rateConverter // nil at SampleRate
	buf  []byte
}

// A Q32 gain: q32One is unity. A sample times a Q32 gain is Q48 of S16, which
// q48ToS32Shift takes to S32: 32 - 16.
const (
	q32One        = 1 << 32
	q48ToS32Shift = 16
)

// NewS32Writer writes to w at rate, which must be at most SampleRate.
func NewS32Writer(w io.Writer, rate int) *S32Writer {
	s := &S32Writer{w: w, conv: nil, buf: nil}
	if rate > 0 && rate < SampleRate {
		s.conv = newRateConverter(int64(rate))
	}

	return s
}

func (s *S32Writer) Write(p []byte) (int, error) { return s.WriteGain(p, 1) }

func (s *S32Writer) WriteGain(block []byte, gain float64) (int, error) {
	gainQ32 := int64(math.Round(min(max(gain, 0), 1) * q32One))
	// Downsampling never emits more frames than it reads, plus the one carried over.
	size := s32Widen*len(block) + s32FrameBytes
	s.buf = slices.Grow(s.buf[:0], size)[:size]

	out := s.buf[:0]
	if s.conv != nil {
		out = s.conv.resample(s.buf, block, widthS32, gainQ32)
	} else {
		for i := 0; i+1 < len(block); i += bytesPerSamp {
			v := int64(int16(binary.LittleEndian.Uint16(block[i:])))
			out = binary.LittleEndian.AppendUint32(out, uint32(int32(v*gainQ32>>q48ToS32Shift)))
		}
	}

	n, err := s.w.Write(out)
	if err != nil {
		return 0, fmt.Errorf("write S32: %w", err)
	}

	if n != len(out) {
		return 0, io.ErrShortWrite
	}

	return len(block), nil
}
