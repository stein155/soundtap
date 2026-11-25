// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"encoding/binary"
	"math"
	"slices"
)

// mixInto writes src*srcGain + ann*annGain into dst, per S16_LE sample, saturating
// at the int16 limits. src/ann may be shorter than dst (a short final block or an
// underrun) — missing samples count as silence. dst length must be even.
func mixInto(dst, src, ann []byte, srcGain, annGain float64) {
	for offset := 0; offset+1 < len(dst); offset += 2 {
		var mixed float64
		if offset+1 < len(src) {
			mixed += float64(int16(binary.LittleEndian.Uint16(src[offset:]))) * srcGain
		}

		if offset+1 < len(ann) {
			mixed += float64(int16(binary.LittleEndian.Uint16(ann[offset:]))) * annGain
		}

		sample := int64(math.Round(mixed))
		if sample > math.MaxInt16 {
			sample = math.MaxInt16
		} else if sample < math.MinInt16 {
			sample = math.MinInt16
		}

		binary.LittleEndian.PutUint16(dst[offset:], uint16(int16(sample)))
	}
}

// rampToward moves cur one step toward target without overshooting.
func rampToward(cur, target, step float64) float64 {
	switch {
	case cur < target:
		if cur+step > target {
			return target
		}

		return cur + step
	case cur > target:
		if cur-step < target {
			return target
		}

		return cur - step
	default:
		return cur
	}
}

// nonzeroPCM runs on every block, three times, in standby too: a byte at a time it was
// a quarter of soundtapd's standby CPU on the ST10. allZero reads eight at once.
func nonzeroPCM(b []byte) bool { return !AllZero(b) }

// fadeIn ramps a block from silence to its samples, so a source resuming after an
// underrun starts cleanly instead of with a discontinuity the amplifier reproduces as a
// crack.
func fadeIn(block []byte) {
	frames := len(block) / frameBytes
	for frame := range frames {
		gain := float64(frame+1) / float64(frames)
		for channel := range Channels {
			offset := frame*frameBytes + channel*bytesPerSamp

			sample := int16(binary.LittleEndian.Uint16(block[offset:]))
			sample = int16(math.Round(float64(sample) * gain))

			binary.LittleEndian.PutUint16(block[offset:], uint16(sample))
		}
	}
}

// fadeOut fills a block with a ramp from the previous stereo sample to silence. It turns
// a source underrun into a clean short dropout instead of a crack.
func fadeOut(block []byte, fromL, fromR int16) {
	frames := len(block) / frameBytes
	for frame := range frames {
		gain := float64(frame+1) / float64(frames)
		for channel := range Channels {
			offset := frame*frameBytes + channel*bytesPerSamp

			from := fromL
			if channel == 1 {
				from = fromR
			}

			sample := int16(math.Round(float64(from) * (1 - gain)))

			binary.LittleEndian.PutUint16(block[offset:], uint16(sample))
		}
	}
}

// fadedOut is a copy of b ramped from its own samples down to silence.
func fadedOut(b []byte) []byte {
	out := slices.Clone(b)

	frames := len(out) / frameBytes
	for i := 0; i+bytesPerSamp <= frames*frameBytes; i += bytesPerSamp {
		gain := 1 - float64(i/frameBytes+1)/float64(frames)
		v := float64(int16(binary.LittleEndian.Uint16(out[i:])))
		binary.LittleEndian.PutUint16(out[i:], uint16(int16(math.Round(v*gain))))
	}

	return out
}

// AllZero reports whether a PCM block is digital silence.
func AllZero(block []byte) bool {
	for len(block) >= 8 {
		if binary.LittleEndian.Uint64(block) != 0 {
			return false
		}

		block = block[8:]
	}

	for _, c := range block {
		if c != 0 {
			return false
		}
	}

	return true
}
