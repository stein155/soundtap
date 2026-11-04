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
	for i := 0; i+1 < len(dst); i += 2 {
		var v float64
		if i+1 < len(src) {
			v += float64(int16(binary.LittleEndian.Uint16(src[i:]))) * srcGain
		}
		if i+1 < len(ann) {
			v += float64(int16(binary.LittleEndian.Uint16(ann[i:]))) * annGain
		}
		s := int64(math.Round(v))
		if s > math.MaxInt16 {
			s = math.MaxInt16
		} else if s < math.MinInt16 {
			s = math.MinInt16
		}
		binary.LittleEndian.PutUint16(dst[i:], uint16(int16(s)))
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

// fadePCM ramps a block from silence to its samples, or from the previous stereo
// sample to silence. It turns a source underrun into a clean short dropout instead
// of a discontinuity that the amplifier reproduces as a crack.
func fadePCM(b []byte, in bool, fromL, fromR int16) {
	frames := len(b) / frameBytes
	for frame := range frames {
		gain := float64(frame+1) / float64(frames)
		for channel := range Channels {
			i := frame*frameBytes + channel*bytesPerSamp
			v := int16(binary.LittleEndian.Uint16(b[i:]))
			if in {
				v = int16(math.Round(float64(v) * gain))
			} else {
				from := fromL
				if channel == 1 {
					from = fromR
				}
				v = int16(math.Round(float64(from) * (1 - gain)))
			}
			binary.LittleEndian.PutUint16(b[i:], uint16(v))
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
func AllZero(b []byte) bool {
	for len(b) >= 8 {
		if binary.LittleEndian.Uint64(b) != 0 {
			return false
		}
		b = b[8:]
	}
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
