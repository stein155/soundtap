// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"encoding/binary"
	"math"
)

// resampleTaps is the length of the interpolation filter. With a Kaiser window of beta 6
// it passes up to about 19.4 kHz and rejects images by about 60 dB from about 27.4 kHz.
// Group delay is 11.5 source frames, about 0.24 ms.
const resampleTaps = 24

// resampleHist is how many source frames a block needs from before its start.
const resampleHist = resampleTaps - 1

// The filter's coefficients are Q14: a row sums to q14One.
const (
	q14Bits = 14
	q14One  = 1 << q14Bits
	q14Half = 1 << (q14Bits - 1) // rounds a Q14 sum to the nearest sample
	// q46ToS32Shift takes a Q14 filter sum times a Q32 gain to S32: 14 + 32 - 16.
	q46ToS32Shift = 30
)

// An S32_LE sample is twice an S16_LE one.
const (
	s32Widen       = 2
	s32SampleBytes = s32Widen * bytesPerSamp
	s32FrameBytes  = s32Widen * frameBytes
)

// sampleWidth is the sample format a rateConverter writes.
type sampleWidth int

const (
	widthS16 sampleWidth = iota // S16_LE, the bus format
	widthS32                    // S32_LE, with the gain applied to the filter sum
)

// rateConverter resamples one continuous stereo stream through a polyphase windowed-sinc
// filter. Absolute source positions survive block boundaries, so a block size never
// changes the output.
//
// Linear interpolation was audible as lost treble and as distortion in everything at
// 46875 Hz: -2.9 dB and images at -22 dB in the top octave.
//
// Nothing here divides per sample. On the 32-bit ARM speakers a 64-bit division is a
// runtime call (runtime.int64div), and with three per frame it was 94% of the mixer's
// CPU: ~50% of an ST20 core with nothing playing, and underruns heard as ticks.
type rateConverter struct {
	outRate   int64
	base      int64 // absolute index of src[0]
	pos, frac int64 // output position: source frame pos plus frac/outRate
	histL     [resampleHist]int16
	histR     [resampleHist]int16
	filter    *resampleFilter
	left      []int16 // scratch, holding the history and then the block being converted
	right     []int16
}

// newRateConverter starts a converter from SampleRate to outRate.
func newRateConverter(outRate int64) *rateConverter {
	return &rateConverter{
		outRate: outRate,
		base:    0,
		pos:     0,
		frac:    0,
		histL:   [resampleHist]int16{},
		histR:   [resampleHist]int16{},
		filter:  nil,
		left:    nil,
		right:   nil,
	}
}

// resampleFilter holds one row of coefficients per phase. frac is always a multiple of
// unit, which makes frac/unit the row and spares a division per sample.
type resampleFilter struct {
	coefs      [][resampleTaps]int16 // Q14, each row sums to exactly 1<<14
	unit, step int32                 // frac per phase; phases to advance per output frame
}

// newResampleFilter designs the filter for outRate. The cutoff sits at outRate's Nyquist,
// so it also keeps what would otherwise fold back when going down in rate.
func newResampleFilter(outRate int64) *resampleFilter {
	gcd := func(a, b int64) int64 {
		for b != 0 {
			a, b = b, a%b
		}

		return a
	}(outRate, SampleRate)
	phases := int(outRate / gcd)
	filter := &resampleFilter{
		coefs: make([][resampleTaps]int16, phases),
		unit:  int32(gcd),
		step:  int32(SampleRate / gcd),
	}
	cutoff := float64(outRate) / SampleRate

	const beta = 6.0

	bessel := func(x float64) float64 { // the modified Bessel function I0
		sum, term := 1.0, 1.0
		for k := 1.0; term > 1e-12*sum; k++ {
			term *= x * x / (4 * k * k)
			sum += term
		}

		return sum
	}

	const half = resampleTaps / 2

	for phaseIndex := range phases {
		phase := float64(phaseIndex) / float64(phases)

		var row [resampleTaps]float64

		total := 0.0

		for tap := range row {
			// Tap k holds source frame pos-22+k, and the output sits at pos+phase-11.
			distance := float64(tap) - (half - 1) - phase

			w := 0.0
			if u := distance / half; u > -1 && u < 1 {
				w = bessel(beta*math.Sqrt(1-u*u)) / bessel(beta)
			}

			row[tap] = cutoff * sinc(cutoff*distance) * w
			total += row[tap]
		}
		// Round the running sum, not each tap, so the row keeps unity gain at DC.
		sum, emitted := 0.0, 0.0
		for k, c := range row {
			sum += c / total * q14One
			q := math.Round(sum - emitted)
			filter.coefs[phaseIndex][k] = int16(q)
			emitted += q
		}
	}

	return filter
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}

	return math.Sin(math.Pi*x) / (math.Pi * x)
}

// fir is written out so that one bounds check covers all the taps, which is most of the
// cost of playing audio on a speaker. Rows sum to 1<<14 and their magnitudes stay near
// twice that, so any S16 input leaves the sum well inside int32. At Q15 the worst
// input could overflow it.
func fir(row *[resampleTaps]int16, samples []int16) int32 {
	samples = samples[:resampleTaps:resampleTaps]

	return int32(row[0])*int32(samples[0]) + int32(row[1])*int32(samples[1]) + int32(row[2])*int32(samples[2]) +
		int32(row[3])*int32(samples[3]) + int32(row[4])*int32(samples[4]) + int32(row[5])*int32(samples[5]) +
		int32(row[6])*int32(samples[6]) + int32(row[7])*int32(samples[7]) + int32(row[8])*int32(samples[8]) +
		int32(row[9])*int32(samples[9]) + int32(row[10])*int32(samples[10]) + int32(row[11])*int32(samples[11]) +
		int32(row[12])*int32(samples[12]) + int32(row[13])*int32(samples[13]) + int32(row[14])*int32(samples[14]) +
		int32(row[15])*int32(samples[15]) + int32(row[16])*int32(samples[16]) + int32(row[17])*int32(samples[17]) +
		int32(row[18])*int32(samples[18]) + int32(row[19])*int32(samples[19]) + int32(row[20])*int32(samples[20]) +
		int32(row[21])*int32(samples[21]) + int32(row[22])*int32(samples[22]) + int32(row[23])*int32(samples[23])
}

// histZero reports whether the filter has nothing left ringing in it.
func (r *rateConverter) histZero() bool {
	var seen int16
	for i := range r.histL {
		seen |= r.histL[i] | r.histR[i]
	}

	return seen == 0
}

func (r *rateConverter) convert(dst, src []byte) []byte {
	return r.resample(dst, src, widthS16, 0)
}

// convertSilence is convert for a block the caller already found all zero, and reports
// whether the output is silence too.
func (r *rateConverter) convertSilence(dst, src []byte) ([]byte, bool) {
	if r.histZero() {
		return r.skipSilence(dst, r.base+int64(len(src)/frameBytes), widthS16), true
	}

	return r.interpolate(dst, src, widthS16, 0), false
}

// resample writes S16_LE, or at widthS32 S32_LE scaled by gainQ32 (gain * 2^32). The S32
// path applies the gain to the full filter sum, so it keeps the resolution that 16-bit
// rounding would throw away at low gain.
func (r *rateConverter) resample(dst, src []byte, width sampleWidth, gainQ32 int64) []byte {
	frames := len(src) / frameBytes

	end := r.base + int64(frames)
	if r.histZero() && AllZero(src) {
		return r.skipSilence(dst, end, width)
	}

	return r.interpolate(dst, src, width, gainQ32)
}

func (r *rateConverter) interpolate(dst, src []byte, width sampleWidth, gainQ32 int64) []byte {
	if r.filter == nil {
		r.filter = newResampleFilter(r.outRate)
	}

	frames := len(src) / frameBytes
	end := r.base + int64(frames)

	total := resampleHist + frames
	if len(r.left) < total {
		r.left, r.right = make([]int16, total), make([]int16, total)
	}

	left, right := r.left[:total], r.right[:total]
	copy(left, r.histL[:])
	copy(right, r.histR[:])

	for i := range frames {
		j := i * frameBytes
		left[resampleHist+i] = int16(binary.LittleEndian.Uint16(src[j:]))
		right[resampleHist+i] = int16(binary.LittleEndian.Uint16(src[j+bytesPerSamp:]))
	}

	coefs, step := r.filter.coefs, r.filter.step
	phases := int32(len(coefs))
	phase := int32(r.frac) / r.filter.unit
	pos, base := r.pos, r.base
	out := 0

	for pos+1 < end {
		// The window ends one frame past pos, as the loop bound promises, and pos never
		// falls more than one frame behind base.
		w := int(pos-base) + 1
		row := &coefs[phase]

		sumLeft, sumRight := fir(row, left[w:]), fir(row, right[w:])
		if width == widthS32 {
			binary.LittleEndian.PutUint32(dst[out:],
				uint32(saturate32(int64(sumLeft)*gainQ32>>q46ToS32Shift)))
			binary.LittleEndian.PutUint32(dst[out+s32SampleBytes:],
				uint32(saturate32(int64(sumRight)*gainQ32>>q46ToS32Shift)))
			out += s32FrameBytes
		} else {
			binary.LittleEndian.PutUint16(dst[out:], uint16(saturate16(sumLeft)))
			binary.LittleEndian.PutUint16(dst[out+bytesPerSamp:], uint16(saturate16(sumRight)))
			out += frameBytes
		}

		phase += step
		for phase >= phases {
			phase -= phases
			pos++
		}
	}

	r.pos, r.frac = pos, int64(phase)*int64(r.filter.unit)
	if frames > 0 {
		copy(r.histL[:], left[frames:])
		copy(r.histR[:], right[frames:])
		r.base = end
	}

	return dst[:out]
}

// The filter overshoots on material mastered up to full scale, and without these that
// would wrap into a full-scale click.
func saturate32(sum int64) int32 {
	if sum > math.MaxInt32 {
		return math.MaxInt32
	}

	if sum < math.MinInt32 {
		return math.MinInt32
	}

	return int32(sum)
}

// saturate16 takes a Q14 filter sum.
func saturate16(sum int32) int16 {
	sum = (sum + q14Half) >> q14Bits
	if sum > math.MaxInt16 {
		return math.MaxInt16
	}

	if sum < math.MinInt16 {
		return math.MinInt16
	}

	return int16(sum)
}

// skipSilence is resample for an all-zero block: the same number of output frames and
// the same position afterwards, without touching a sample. The mixer writes silence all
// through standby, and interpolating zeros was over half of soundtapd's standby CPU on
// the ST20. Two divisions per block, none per sample.
func (r *rateConverter) skipSilence(dst []byte, end int64, width sampleWidth) []byte {
	// An output is emitted while its position, in 1/outRate frames, is below end-1 frames.
	position := r.pos*r.outRate + r.frac

	frames := int64(0)
	if room := (end-1)*r.outRate - position; room > 0 {
		frames = (room + SampleRate - 1) / SampleRate
	}

	position += frames * SampleRate

	r.pos, r.frac = position/r.outRate, position%r.outRate
	if end > r.base {
		r.base = end // the history is already all zero: the caller checked
	}

	size := int(frames) * frameBytes
	if width == widthS32 {
		size *= s32Widen
	}

	clear(dst[:size])

	return dst[:size]
}
