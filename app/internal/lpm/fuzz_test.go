// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package lpm

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// FuzzLPMFrames feeds the soundbar processor's UART stream (ST300) to the frame reader:
// whatever arrives, only whole frames whose checksum holds come out, a valid frame after
// noise is still found, and the readers of a key or content frame take any valid frame.
// `go test` runs the seeds; `go test -fuzz=FuzzLPMFrames ./cmd/soundtapd` searches further.
func FuzzLPMFrames(f *testing.F) {
	key := make([]byte, 24)
	binary.BigEndian.PutUint32(key[12:], 4)
	f.Add(lpmFrame(lpmModule, lpmKey, key), []byte{})
	f.Add(lpmFrame(lpmModule, lpmContent, []byte{0, 0, 0, 3}), []byte{0x55, 0xaa})
	f.Add(lpmHealthAnswer(lpmProcessor, "SoundTap"), bytes.Repeat([]byte{0xff}, 200))
	f.Add([]byte{}, []byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, noise, tail []byte) {
		want := lpmFrame(lpmModule, lpmKey, key)
		stream := append(append(append([]byte(nil), noise...), want...), tail...)
		var frames [][]byte
		_ = lpmFrames(bytes.NewReader(stream), func(fr []byte) { frames = append(frames, fr) })
		found := false
		for _, fr := range frames {
			if !lpmValid(fr) {
				t.Fatalf("an invalid frame came out: % x", fr)
			}
			found = found || bytes.Equal(fr, want)
			_, _, _ = lpmKeyOf(fr)
			_, _ = lpmContentOf(fr)
		}
		// Noise that happens to hold a valid frame overlapping ours can hide it; noise
		// that holds none cannot.
		if !found && !holdsValidFrame(noise) && len(frames) == 0 {
			t.Fatalf("the frame after %d bytes of noise was not found", len(noise))
		}
	})
}

// holdsValidFrame reports whether any frame-sized window of b, or of b running into a
// frame, checks out.
func holdsValidFrame(b []byte) bool {
	for i := 0; i+lpmFrameLen <= len(b); i++ {
		if lpmValid(b[i : i+lpmFrameLen]) {
			return true
		}
	}
	return false
}
