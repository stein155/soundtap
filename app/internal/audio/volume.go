// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"encoding/binary"
	"math"
)

// SetVolume sets the master volume (0..100) applied to the whole source bus — so every
// source (TuneIn, AirPlay, Bluetooth, Spotify) gets uniform, live volume, not just the
// ones with their own gain. Announcements keep their own level.
func (m *Mixer) SetVolume(vol int) {
	if vol < 0 {
		vol = 0
	} else if vol > 100 {
		vol = 100
	}
	m.mu.Lock()
	m.master = stockVolumeGain(vol)
	if !m.volumeSet {
		m.curMaster, m.volumeSet = m.master, true
	}
	cv, level := m.codec, m.codecLevel()
	m.mu.Unlock()
	if cv != nil {
		cv.SetGain(level)
	}
}

// masterGlide is the share of the remaining distance curMaster covers per block: about
// 95 % in ten blocks (~200 ms), and every step is a fraction of the one before.
// per-block gain steps, not per-sample interpolation; scaleInto stays the
// integer fast path the ST10's CPU needs. Interpolate inside the block if steps are heard.
const masterGlide = 0.25

func glide(cur, target float64) float64 {
	next := cur + (target-cur)*masterGlide
	// Snap once the rest is below a 16-bit step at full scale.
	if math.Abs(target-next) < 1.0/65536 {
		return target
	}
	return next
}

// VolumeGain is the gain of a volume step, for a level that has to match the music's.
func VolumeGain(vol int) float64 { return stockVolumeGain(vol) }

// stockVolumeGain reproduces Bose APServer's 101-entry DSP coefficient table. Volume
// percentages are perceived-loudness steps, not linear PCM amplitude.
//
// The table is not a guess: it sits in APServer's .rodata at 0x2a52d0, 101 entries in
// Q22 with 4194304 as unity, and this piecewise fit is within 0.01 dB of it everywhere.
// Its two knees really are at 10 and 50 -- not at the thresholdLow/thresholdHigh in
// BoseApp's per-product XML, which are product-behaviour breakpoints and not this curve.
// TestStockVolumeCurveMatchesAPServerTable pins the fit to entries read out of that array.
func stockVolumeGain(vol int) float64 {
	if vol <= 0 {
		return 0
	}
	if vol > 100 {
		vol = 100
	}
	v := float64(vol)
	db := -62.5 + 0.625*v
	if vol < 10 {
		db = -89.325 + 2.45*v
	} else if vol < 50 {
		db = -75.24 + 0.885*v
	}
	return math.Pow(10, db/20)
}

// scaleInto is the normal playback fast path. Integer scaling avoids two float
// conversions and math.Round per sample on the ST10's saturated single-core CPU.
func scaleInto(dst, src []byte, gain float64) {
	n := min(len(dst), len(src)) &^ 1
	if gain == 1 {
		copy(dst[:n], src[:n])
		clear(dst[n:])
		return
	}
	fixed := int32(math.Round(gain * 32768))
	if gain > 1 {
		// Only the WLA's AUX trim (1.22) goes above unity: full scale must clip, not wrap
		// to the opposite sign. Apart so the common path pays nothing for it.
		for i := 0; i < n; i += 2 {
			v := int64(int16(binary.LittleEndian.Uint16(src[i:]))) * int64(fixed) / 32768
			binary.LittleEndian.PutUint16(dst[i:], uint16(int16(max(math.MinInt16, min(math.MaxInt16, v)))))
		}
		clear(dst[n:])
		return
	}
	for i := 0; i < n; i += 2 {
		v := int32(int16(binary.LittleEndian.Uint16(src[i:]))) * fixed
		if v >= 0 {
			v += 16384
		} else {
			v -= 16384
		}
		binary.LittleEndian.PutUint16(dst[i:], uint16(int16(v/32768)))
	}
	clear(dst[n:])
}
