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
	var value int16

	_, err := binary.Decode(pcm[frame*4+channel*2:], binary.LittleEndian, &value)
	if err != nil {
		panic(err)
	}

	return value
}

func TestFadeRampsEachChannelFromOrToSilence(t *testing.T) {
	t.Parallel()

	const frames = 5
	for _, direction := range []fadeDirection{fadeFromSilence, fadeToSilence} {
		pcm := fullScale(frames, 0xab, 0xcd)
		fade(pcm, direction, nil)

		for frame := range frames {
			gain := float64(frame) / (frames - 1)
			if direction == fadeToSilence {
				gain = 1 - gain
			}

			want := [2]int16{int16(math.Round(32767 * gain)), int16(math.Round(-32768 * gain))}
			if got := [2]int16{sample(pcm, frame, 0), sample(pcm, frame, 1)}; got != want {
				t.Errorf("direction %v frame %d: got %v, want %v", direction, frame, got, want)
			}
		}

		if pcm[len(pcm)-2] != 0xab || pcm[len(pcm)-1] != 0xcd {
			t.Errorf("direction %v: the half frame at the end changed: % x", direction, pcm[len(pcm)-2:])
		}
	}
	// The ends: fade in starts at zero and ends untouched, fade out the other way round.
	fadedIn, fadedOut := fullScale(frames), fullScale(frames)
	fade(fadedIn, fadeFromSilence, nil)
	fade(fadedOut, fadeToSilence, nil)

	if sample(fadedIn, 0, 1) != 0 || sample(fadedIn, frames-1, 1) != -32768 ||
		sample(fadedOut, 0, 0) != 32767 || sample(fadedOut, frames-1, 0) != 0 {
		t.Errorf("ends: in % x, out % x", fadedIn, fadedOut)
	}
}

func TestFadeOfOneFrameAndOfNothing(t *testing.T) {
	t.Parallel()

	one := fullScale(1)
	fade(one, fadeFromSilence, nil)

	if sample(one, 0, 0) != 0 || sample(one, 0, 1) != 0 {
		t.Errorf("one frame faded in: % x, want silence", one)
	}

	if got := fade([]byte{1, 2, 3}, fadeFromSilence, nil); len(got) != 0 {
		t.Errorf("no whole frame: %d samples", len(got))
	}
}

//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test
func TestFadeReusesItsScratchSpace(t *testing.T) {
	pcm := fullScale(938)
	samples := fade(pcm, fadeToSilence, nil)

	if allocs := testing.AllocsPerRun(10, func() { samples = fade(pcm, fadeToSilence, samples) }); allocs != 0 {
		t.Errorf("fade allocated %v times with scratch space given", allocs)
	}
}
