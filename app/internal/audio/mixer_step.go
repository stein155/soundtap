// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import "time"

// step consumes one source block and produces one mixed output block. It pulls the
// announcement block itself (so the duck/EOF/restore state machine is synchronous
// and unit-testable). src may be short/nil (underrun -> silence).
func (m *Mixer) step(src []byte) []byte {
	out, _, _ := m.stepInto(make([]byte, blockBytes), nil, src, false)
	m.applyBass(out)

	return out
}

// stepInto is step writing into a caller-owned block. Run reuses one output buffer
// for the life of the pump: allocating two 3840-byte blocks 50 times a second was
// ~576 KB/s of garbage, and a GC assist landing in the mix loop is exactly what
// used to cost a block and gap the stream.
//
// With deferGain the block is mixed as loud as the loudest input allows and the
// remaining gain is returned for a GainWriter to apply in its wider format; without
// it the full gain is applied here and the returned gain is 1.
//
// The multiroom tap gets dst too, except while an announcement is mixed in: then, given
// a tapDst, stepInto writes the source alone into it at the gain it has without one and
// reports so, and a follower plays neither the leader's chime nor its duck.
func (m *Mixer) stepInto( //nolint:revive // deferGain is the sink's kind; where the gain goes runs through the mix
	dst, tapDst, src []byte, deferGain bool,
) ([]byte, float64, bool) {
	m.mu.Lock()

	ann := m.takeAnnouncementLocked()

	m.curGain = rampToward(m.curGain, m.target, gainStep)
	m.curMaster = glide(m.curMaster, m.master)

	shut := 0.0
	if m.held.Load() {
		shut = 1
	}
	// SetLive closes the gate at once only over silence. Over a source still playing
	// (a follower joining from radio), one block fades it out first, as an underrun does:
	// a gain step from sound to nothing is a click the open amp plays.
	if m.closing && !m.srcHeard {
		m.shut, m.closing = 1, false
	}

	m.shut = rampToward(m.shut, shut, float64(blockFrames)/(SampleRate*holdFade.Seconds()))
	srcGain, annGain := m.curGain*m.curMaster*(1-m.shut)*m.srcTrim, m.annGain
	fadeOut := m.closing

	m.srcHeard = srcGain > 0 && len(src) > 0 && !fadeOut
	if fadeOut {
		src = fadedOut(src)
	}

	if deferGain && m.codec != nil {
		out, tapped := m.codecStepLocked(dst, tapDst, src, ann)

		return out, 1, tapped
	}
	m.mu.Unlock()

	tapped := len(ann) > 0 && tapDst != nil
	if tapped {
		tapGain := m.curMaster
		if deferGain && tapGain > 0 {
			tapGain /= min(tapGain, 1)
		}

		scaleInto(tapDst, src, tapGain)
	}

	gain := 1.0

	if deferGain {
		gain, srcGain, annGain = deferredGains(srcGain, annGain, ann)
	}

	if len(ann) == 0 {
		scaleInto(dst, src, srcGain)
	} else {
		mixInto(dst, src, ann, srcGain, annGain)
	}

	return dst, gain, tapped
}

// deferredGains splits the gains for a GainWriter: the gain left for the sink, and the
// source's and the announcement's gains in the block.
//
// At volume 1 the master gain is -87 dB, which leaves a 16-bit sample about one LSB of
// the signal: a square-ish wave heard as crackle. Keep the block near full scale and
// let the sink scale it after widening. Only a clip in this block counts: annGain
// outlives its announcement, and a stale one pinned gain near 1 so the source was scaled
// down in 16 bits again -- the ST20 crackled at low volume after its first chime
// (2026-10-01).
func deferredGains(srcGain, annGain float64, ann []byte) (float64, float64, float64) {
	loudest := srcGain
	if len(ann) > 0 {
		loudest = max(srcGain, annGain)
	}

	gain := min(loudest, 1)
	if gain > 0 {
		srcGain /= gain
		annGain /= gain
	}

	return gain, srcGain, annGain
}

// takeAnnouncementLocked takes the active announcement's next block: nil when there is
// none, or none ready yet. m.mu is held.
func (m *Mixer) takeAnnouncementLocked() []byte {
	if m.ann == nil {
		return nil
	}

	var ann []byte
	// Wait briefly for the very first block, and never again. An announcement has to
	// be audible the moment it is asked for -- it is a doorbell, not a track -- but a
	// clip that stalls later must not take the device with it, so from the second
	// block on the mixer takes whatever is ready and moves on. sourceWait is the
	// same bound this file already uses for late source PCM.
	take := func(block []byte, ok bool) {
		if ok {
			ann = block
			m.annBegun = true
		} else { // clip drained -- start the restore ramp
			m.ann, m.annStop, m.annBegun = nil, nil, false
			m.target = 1.0
		}
	}

	if !m.annBegun {
		timer := time.NewTimer(sourceWait)
		defer timer.Stop()

		select {
		case block, ok := <-m.ann:
			take(block, ok)
		case <-timer.C:
		}
	} else {
		// Non-blocking, not a select against a closed channel: with both cases ready
		// Go picks one at random, and half the blocks of a ready clip came out silent.
		select {
		case block, ok := <-m.ann:
			take(block, ok)
		default:
		}
	}

	return ann
}
