// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"encoding/binary"
	"math"
	"testing"
)

// fullScale is n stereo frames at the edges of S16: left at +32767, right at -32768, then
// tail: bytes after the last whole frame.
func fullScale(n int, tail ...byte) []byte {
	pcm := make([]byte, 0, n*4+len(tail))
	for range n {
		pcm = binary.LittleEndian.AppendUint16(pcm, 0x7fff)
		pcm = binary.LittleEndian.AppendUint16(pcm, 0x8000)
	}
	return append(pcm, tail...)
}

func sample(pcm []byte, frame, channel int) int16 {
	var s int16
	if _, err := binary.Decode(pcm[frame*4+channel*2:], binary.LittleEndian, &s); err != nil {
		panic(err)
	}
	return s
}

func TestFadeRampsEachChannelFromOrToSilence(t *testing.T) {
	const frames = 5
	for _, in := range []bool{true, false} {
		pcm := fullScale(frames, 0xab, 0xcd)
		fade(pcm, in, nil)
		for i := range frames {
			g := float64(i) / (frames - 1)
			if !in {
				g = 1 - g
			}
			want := [2]int16{int16(math.Round(32767 * g)), int16(math.Round(-32768 * g))}
			if got := [2]int16{sample(pcm, i, 0), sample(pcm, i, 1)}; got != want {
				t.Errorf("in=%v frame %d: got %v, want %v", in, i, got, want)
			}
		}
		if pcm[len(pcm)-2] != 0xab || pcm[len(pcm)-1] != 0xcd {
			t.Errorf("in=%v: the half frame at the end changed: % x", in, pcm[len(pcm)-2:])
		}
	}
	// The ends: fade in starts at zero and ends untouched, fade out the other way round.
	in, out := fullScale(frames), fullScale(frames)
	fade(in, true, nil)
	fade(out, false, nil)
	if sample(in, 0, 1) != 0 || sample(in, frames-1, 1) != -32768 ||
		sample(out, 0, 0) != 32767 || sample(out, frames-1, 0) != 0 {
		t.Errorf("ends: in % x, out % x", in, out)
	}
}

func TestFadeOfOneFrameAndOfNothing(t *testing.T) {
	one := fullScale(1)
	fade(one, true, nil)
	if sample(one, 0, 0) != 0 || sample(one, 0, 1) != 0 {
		t.Errorf("one frame faded in: % x, want silence", one)
	}
	if got := fade([]byte{1, 2, 3}, true, nil); len(got) != 0 {
		t.Errorf("no whole frame: %d samples", len(got))
	}
}

func TestFadeReusesItsScratchSpace(t *testing.T) {
	pcm := fullScale(938)
	samples := fade(pcm, false, nil)
	if allocs := testing.AllocsPerRun(10, func() { samples = fade(pcm, false, samples) }); allocs != 0 {
		t.Errorf("fade allocated %v times with scratch space given", allocs)
	}
}
