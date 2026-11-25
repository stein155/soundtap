// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import "time"

// The mainline kernel has no Bose IR driver. The receiver is a plain gpio-ir-receiver
// on gpio1_10, rc-core owns it, and /dev/lirc0 hands out the raw edge timings in
// LIRC_MODE_MODE2: one uint32 per edge, the kind in the top byte and microseconds in
// the low 24 bits. rc-core has no decoder for Bose's protocol, so it is decoded here.
//
// What a Bose frame is, as far as ir.ko's own decoder shows: a header, then sixteen
// bits in which the second byte is the exact complement of the first. Each bit is a
// short mark followed by a short or a long space. Nothing below depends on the
// absolute timings -- a long space is one more than twice as long as its own mark --
// because those have never been measured on this board, and a relative test survives
// a receiver that stretches or shortens every edge by the same amount.
//
// Polarity does not matter either. The frame is read as a run of durations, never by
// pulse/space labels, so a receiver line wired the other way round yields the same
// numbers shifted by nothing.
const (
	lircKindMask    = 0xff000000
	lircValueMask   = 0x00ffffff
	lircKindSpace   = 0x00000000
	lircKindPulse   = 0x01000000
	lircKindTimeout = 0x03000000

	// lircGapUS ends a frame: longer than any edge inside one, shorter than the pause
	// between two retransmissions.
	lircGapUS = 5000
	// lircMaxDurations bounds what noise can make the framer hold on to.
	lircMaxDurations = 128
	// lircReleaseAfter is how long after the last frame the key counts as let go.
	// ir.ko did the same with its 14-jiffy state timer; irSynth's grace absorbs a
	// release that turns out to be premature.
	lircReleaseAfter = 200 * time.Millisecond
)

// decodeBoseFrame returns the first byte of a frame, bits in the order they arrived,
// least significant first. Which of that byte, its complement or their bit reversals
// is the code ir.map calls it is resolved against the map -- see irSynth.resolve.
func decodeBoseFrame(d []uint32) (byte, bool) {
	// The frame ends on the stop mark, or on the last bit's space when the receiver
	// swallowed the stop mark into the gap; try both alignments.
	for _, end := range []int{len(d) - 1, len(d)} {
		if end < 32 {
			continue
		}

		var bits uint16

		for i := range 16 {
			mark, space := d[end-32+2*i], d[end-32+2*i+1]
			if mark == 0 || space == 0 {
				bits = 0

				break
			}

			if space > 2*mark {
				bits |= 1 << i
			}
		}

		a, b := byte(bits), byte(bits>>8)
		if a^b == 0xff {
			return a, true
		}
	}

	return 0, false
}

// lircFramer turns MODE2 samples into the press/release stream irSynth expects from
// ir.ko: a code per decoded frame and irReleaseCode once frames stop arriving.
type lircFramer struct {
	durs      []uint32
	queue     []byte
	down      bool
	lastFrame time.Time
}

func (f *lircFramer) sample(s uint32, now time.Time) {
	kind, v := s&lircKindMask, s&lircValueMask
	switch {
	case kind == lircKindTimeout, (kind == lircKindPulse || kind == lircKindSpace) && v > lircGapUS:
		f.flush(now)
	case kind == lircKindPulse || kind == lircKindSpace:
		if len(f.durs) == lircMaxDurations {
			f.durs = f.durs[1:]
		}

		f.durs = append(f.durs, v)
	}
}

// flush decodes whatever durations are pending as one frame.
func (f *lircFramer) flush(now time.Time) {
	if len(f.durs) == 0 {
		return
	}

	if c, ok := decodeBoseFrame(f.durs); ok {
		f.queue = append(f.queue, c)
		f.down, f.lastFrame = true, now
	}

	f.durs = f.durs[:0]
}

// idle is called when the line has been quiet for a while: it closes a pending frame
// and, once frames have stopped for long enough, reports the release.
func (f *lircFramer) idle(now time.Time) {
	f.flush(now)

	if f.down && now.Sub(f.lastFrame) >= lircReleaseAfter {
		f.down = false
		f.queue = append(f.queue, irReleaseCode)
	}
}

func (f *lircFramer) pop() (byte, bool) {
	if len(f.queue) == 0 {
		return 0, false
	}

	c := f.queue[0]
	f.queue = f.queue[1:]

	return c, true
}

// irTransforms are the four readings of one decoded byte, in the order they are
// tried. The first that names a key in ir.map is kept for the rest of the run.
var irTransforms = []struct {
	name string
	fn   func(byte) byte
}{
	{"as-is", func(c byte) byte { return c }},
	{"reversed", reverseBits},
	{"complement", func(c byte) byte { return ^c }},
	{"reversed-complement", func(c byte) byte { return reverseBits(^c) }},
}

func reverseBits(c byte) byte {
	var r byte
	for range 8 {
		r = r<<1 | c&1
		c >>= 1
	}

	return r
}
