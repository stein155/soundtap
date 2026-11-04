// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"encoding/binary"
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
func WriteGain(w io.Writer, p []byte, gain float64, buf *[]byte) (int, error) {
	if g, ok := w.(GainWriter); ok {
		return g.WriteGain(p, gain)
	}
	if gain == 1 {
		return w.Write(p)
	}
	*buf = slices.Grow((*buf)[:0], len(p))[:len(p)]
	scaleInto(*buf, p, gain)
	return w.Write(*buf)
}

// S32Writer widens the Mixer's 48 kHz S16_LE to S32_LE at rate, applying the gain
// after widening and resampling in 64-bit so nothing is rounded to 16 bits on the way.
type S32Writer struct {
	w    io.Writer
	conv *rateConverter // nil at SampleRate
	buf  []byte
}

// NewS32Writer writes to w at rate, which must be at most SampleRate.
func NewS32Writer(w io.Writer, rate int) *S32Writer {
	s := &S32Writer{w: w}
	if rate > 0 && rate < SampleRate {
		s.conv = &rateConverter{outRate: int64(rate)}
	}
	return s
}

func (s *S32Writer) Write(p []byte) (int, error) { return s.WriteGain(p, 1) }

func (s *S32Writer) WriteGain(p []byte, gain float64) (int, error) {
	gainQ32 := int64(math.Round(min(max(gain, 0), 1) * (1 << 32)))
	// Downsampling never emits more frames than it reads, plus the one carried over.
	s.buf = slices.Grow(s.buf[:0], 2*len(p)+2*frameBytes)[:2*len(p)+2*frameBytes]
	out := s.buf[:0]
	if s.conv != nil {
		out = s.conv.resample(s.buf, p, true, gainQ32)
	} else {
		for i := 0; i+1 < len(p); i += 2 {
			v := int64(int16(binary.LittleEndian.Uint16(p[i:])))
			out = binary.LittleEndian.AppendUint32(out, uint32(int32(v*gainQ32>>16)))
		}
	}
	n, err := s.w.Write(out)
	if err != nil {
		return 0, err
	}
	if n != len(out) {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}
