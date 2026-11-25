// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import "time"

// CodecVolume sets the codec's own volume, the way the stock firmware does it in the DSP.
// SetGain must not block: on the ST20 the codec is reached over bit-banged SPI.
type CodecVolume interface{ SetGain(gain float64) }

// SetCodecVolume moves the master volume into the codec; nil moves it back into the PCM.
// delay reports how long a block written now takes to reach the DAC. Only a GainWriter
// sink plays the PCM at full scale, so another sink keeps the volume in the PCM.
func (m *Mixer) SetCodecVolume(codec CodecVolume, delay func() time.Duration) {
	m.mu.Lock()
	m.codec, m.codecDelay = codec, delay
	m.boostSent, m.boostAt = 0, 0
	master := m.master
	m.mu.Unlock()

	if codec != nil {
		codec.SetGain(master)
	}
}

// codecLevel is what the codec plays at: master, or a louder announcement reaching the DAC.
func (m *Mixer) codecLevel() float64 { return max(m.master, m.boostAt) }

// codecStepLocked is stepInto with the master volume in the codec. It unlocks m.mu.
//
// The PCM carries everything relative: the duck, the hold gate, and an announcement
// against the source. An announcement louder than master needs the codec turned up while
// it plays, and the source turned down by the same ratio in the PCM; that codec change is
// scheduled for when the block reaches the DAC, so the two land together. A volume step
// has no PCM half and goes to the codec at once (SetVolume).
func (m *Mixer) codecStepLocked(dst, tapDst, src, ann []byte) ([]byte, bool) {
	level, boost := m.master, 0.0
	if len(ann) > 0 && m.annGain > level {
		level, boost = m.annGain, m.annGain
	}

	srcGain, annGain := 0.0, 0.0
	if level > 0 {
		srcGain = m.curGain * (1 - m.shut) * m.srcTrim * m.master / level
		annGain = m.annGain / level
	}

	codec, delay := m.codec, m.codecDelay
	changed := boost != m.boostSent
	m.boostSent = boost
	m.mu.Unlock()

	if changed {
		after := time.Duration(0)
		if delay != nil {
			after = delay()
		}

		time.AfterFunc(after, func() {
			m.mu.Lock()
			if m.codec != codec {
				m.mu.Unlock()

				return
			}

			m.boostAt = boost
			level := m.codecLevel()
			m.mu.Unlock()
			codec.SetGain(level)
		})
	}

	tapped := len(ann) > 0 && tapDst != nil
	if tapped {
		scaleInto(tapDst, src, 1)
	}

	if len(ann) == 0 {
		scaleInto(dst, src, srcGain)
	} else {
		mixInto(dst, src, ann, srcGain, annGain)
	}

	return dst, tapped
}
