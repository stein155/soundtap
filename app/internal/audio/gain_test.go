// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// At volume 1 the 16-bit path left a full-scale sine about one LSB tall: three
// distinct sample values, heard as crackle on the ST10. The deferred path must keep
// the waveform and land at the same level.
func TestLowVolumeKeepsResolutionInS32(t *testing.T) {
	src := make([]byte, blockBytes)
	for f := 0; f < blockFrames; f++ {
		v := uint16(int16(30000 * math.Sin(2*math.Pi*1000*float64(f)/SampleRate)))
		binary.LittleEndian.PutUint16(src[f*frameBytes:], v)
		binary.LittleEndian.PutUint16(src[f*frameBytes+bytesPerSamp:], v)
	}
	m := NewMixer(nil, true)
	m.SetVolume(1)
	want := 30000 * stockVolumeGain(1) * 65536

	old, _, _ := m.stepInto(make([]byte, blockBytes), nil, src, false)
	levels := map[int16]bool{}
	for i := 0; i < len(old); i += 2 {
		levels[int16(binary.LittleEndian.Uint16(old[i:]))] = true
	}
	if len(levels) > 5 {
		t.Fatalf("16-bit path has %d levels; the premise of this test changed", len(levels))
	}

	block, gain, _ := m.stepInto(make([]byte, blockBytes), nil, src, true)
	var out bytes.Buffer
	if n, err := NewS32Writer(&out, 46875).WriteGain(block, gain); err != nil || n != len(block) {
		t.Fatalf("WriteGain = %d, %v", n, err)
	}
	peak := 0.0
	wide := map[int32]bool{}
	b := out.Bytes()
	for i := 0; i+3 < len(b); i += 4 {
		s := int32(binary.LittleEndian.Uint32(b[i:]))
		wide[s] = true
		peak = max(peak, math.Abs(float64(s)))
	}
	if len(wide) < 200 {
		t.Fatalf("S32 path has only %d levels", len(wide))
	}
	if peak < want*0.98 || peak > want*1.02 {
		t.Fatalf("peak = %.0f, want about %.0f", peak, want)
	}
	if frames := len(b) / 8; frames < 935 || frames > 938 {
		t.Fatalf("frames = %d, want about 937 at 46875 Hz", frames)
	}
}

// A finished announcement must not keep the deferred gain pinned to its clip level,
// or every later low-volume block is scaled down in 16 bits again.
func TestLowVolumeAfterAnnouncementStaysDeferred(t *testing.T) {
	m := NewMixer(nil, true)
	m.SetVolume(1)
	m.Announce(bytes.NewReader(nil), 1.0)
	for m.Announcing() {
		m.stepInto(make([]byte, blockBytes), nil, nil, true)
	}
	m.curGain = 1.0 // skip the restore ramp
	_, gain, _ := m.stepInto(make([]byte, blockBytes), nil, nil, true)
	if want := stockVolumeGain(1); math.Abs(gain-want) > want*1e-6 {
		t.Fatalf("deferred gain = %g after announcement, want %g", gain, want)
	}
}

// A sink that cannot take the gain gets the same S16 the Mixer used to write.
func TestWriteGainFallsBackToS16(t *testing.T) {
	var out bytes.Buffer
	var buf []byte
	if _, err := WriteGain(&out, pcm(2, 10000), 0.5, &buf); err != nil {
		t.Fatal(err)
	}
	if got := sampleAt(out.Bytes(), 0); got != 5000 {
		t.Fatalf("sample = %d, want 5000", got)
	}
}
