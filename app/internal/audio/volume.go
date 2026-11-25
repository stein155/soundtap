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
	} else if vol > maxVolume {
		vol = maxVolume
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

// stockVolumeGain's fit is three lines in dB over the volume step, with knees between them.
const (
	maxVolume = 100

	volumeKneeLow  = 10
	volumeKneeHigh = 50

	volumeLowOffsetDB = -89.325 // below volumeKneeLow
	volumeLowSlopeDB  = 2.45
	volumeMidOffsetDB = -75.24 // from volumeKneeLow to volumeKneeHigh
	volumeMidSlopeDB  = 0.885
	volumeTopOffsetDB = -62.5 // from volumeKneeHigh up
	volumeTopSlopeDB  = 0.625
)

// Decibels are twenty times the base-ten logarithm of an amplitude ratio.
const (
	decibelBase       = 10
	decibelsPerDecade = 20
)

// scaleInto's integer gain is Q15: q15One is unity, q15Half rounds.
const (
	q15One  = 32768
	q15Half = 16384
)

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

	if vol > maxVolume {
		vol = maxVolume
	}

	volume := float64(vol)

	decibels := volumeTopOffsetDB + volumeTopSlopeDB*volume
	if vol < volumeKneeLow {
		decibels = volumeLowOffsetDB + volumeLowSlopeDB*volume
	} else if vol < volumeKneeHigh {
		decibels = volumeMidOffsetDB + volumeMidSlopeDB*volume
	}

	return dbToGain(decibels)
}

// dbToGain turns decibels into an amplitude gain.
func dbToGain(db float64) float64 { return math.Pow(decibelBase, db/decibelsPerDecade) }

// scaleInto is the normal playback fast path. Integer scaling avoids two float
// conversions and math.Round per sample on the ST10's saturated single-core CPU.
func scaleInto(dst, src []byte, gain float64) {
	size := min(len(dst), len(src)) &^ 1
	if gain == 1 {
		copy(dst[:size], src[:size])
		clear(dst[size:])

		return
	}

	fixed := int32(math.Round(gain * q15One))
	if gain > 1 {
		// Only the WLA's AUX trim (1.22) goes above unity: full scale must clip, not wrap
		// to the opposite sign. Apart so the common path pays nothing for it.
		for i := 0; i < size; i += 2 {
			v := int64(int16(binary.LittleEndian.Uint16(src[i:]))) * int64(fixed) / q15One
			binary.LittleEndian.PutUint16(dst[i:], uint16(int16(max(math.MinInt16, min(math.MaxInt16, v)))))
		}

		clear(dst[size:])

		return
	}

	for offset := 0; offset < size; offset += 2 {
		scaled := int32(int16(binary.LittleEndian.Uint16(src[offset:]))) * fixed
		if scaled >= 0 {
			scaled += q15Half
		} else {
			scaled -= q15Half
		}

		binary.LittleEndian.PutUint16(dst[offset:], uint16(int16(scaled/q15One)))
	}

	clear(dst[size:])
}
