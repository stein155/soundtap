// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"encoding/binary"
	"math"
)

type biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
	z1, z2     float64
}

func (b *biquad) process(x float64) float64 {
	y := b.b0*x + b.z1
	b.z1 = b.b1*x - b.a1*y + b.z2
	b.z2 = b.b2*x - b.a2*y

	return y
}

// SetBass applies the ST10/ST20 bass range the way stock does: a first-order low shelf
// at 260 Hz whose depth is 1.22 dB per step, so -9 cuts the deep bass by about 11 dB.
// Measured on a stock ST20 (27.0.6) from APServer's PCM with a multi-tone signal at
// every step; the fit is within 0.3 dB RMS from 31 Hz to 4 kHz.
func (m *Mixer) SetBass(gain int) {
	if gain < stockBassMin {
		gain = stockBassMin
	} else if gain > 0 {
		gain = 0
	}

	m.bassMu.Lock()
	defer m.bassMu.Unlock()

	if gain == m.bass {
		return
	}

	m.bass = gain
	if gain == 0 {
		m.bassFilter = [Channels]biquad{}

		return
	}
	// Only the coefficients change: the filter keeps its state, which resetting on every
	// step turned into a click.
	coeff := lowShelf(stockBassDBPerStep*float64(gain), stockBassCorner)

	for channel := range m.bassFilter {
		f := &m.bassFilter[channel]
		f.b0, f.b1, f.b2, f.a1, f.a2 = coeff.b0, coeff.b1, coeff.b2, coeff.a1, coeff.a2
	}
}

const (
	stockBassMin       = -9 // the deepest cut SetBass takes, in steps
	stockBassDBPerStep = 1.22
	stockBassCorner    = 260.0
)

// lowShelf is a first-order shelf, bilinear-transformed: gainDB below frequency, 0 dB above.
func lowShelf(gainDB, frequency float64) biquad {
	a := dbToGain(gainDB)
	t := math.Tan(math.Pi * frequency / SampleRate)
	a0 := t + 1

	return biquad{b0: (t*a + 1) / a0, b1: (t*a - 1) / a0, b2: 0, a1: (t - 1) / a0, a2: 0, z1: 0, z2: 0}
}

// applyBass runs the bass shelf, and reports whether it changed pcm.
func (m *Mixer) applyBass(pcm []byte) bool {
	m.bassMu.Lock()
	defer m.bassMu.Unlock()

	if m.bass == 0 {
		return false
	}
	// Standby is silence with bass set: a float biquad per sample over zeros, decaying
	// a tail that rounds to nothing. Once the state is too small to reach one LSB the
	// filter's output on silence is silence.
	if settled(m.bassFilter[:]) && AllZero(pcm) {
		for c := range m.bassFilter {
			m.bassFilter[c].z1, m.bassFilter[c].z2 = 0, 0
		}

		return false
	}

	for i := 0; i+frameBytes-1 < len(pcm); i += frameBytes {
		for channel := range Channels {
			offset := i + channel*bytesPerSamp
			x := float64(int16(binary.LittleEndian.Uint16(pcm[offset:])))
			x = m.bassFilter[channel].process(x)

			sample := int64(math.Round(x))
			if sample > math.MaxInt16 {
				sample = math.MaxInt16
			} else if sample < math.MinInt16 {
				sample = math.MinInt16
			}

			binary.LittleEndian.PutUint16(pcm[offset:], uint16(int16(sample)))
		}
	}

	return true
}

// settled reports that every filter's state is below a quarter LSB.
func settled(fs []biquad) bool {
	for _, f := range fs {
		if math.Abs(f.z1) >= 0.25 || math.Abs(f.z2) >= 0.25 {
			return false
		}
	}

	return true
}
