// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

type codecGains struct {
	mu    sync.Mutex
	gains []float64
}

func (c *codecGains) SetGain(g float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gains = append(c.gains, g)
}

func (c *codecGains) last() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gains[len(c.gains)-1]
}

// With the volume in the codec a step goes there at once and the PCM stays at full
// scale, the way stock does it (ADR 0012).
func TestCodecVolumeKeepsPCMAtFullScale(t *testing.T) {
	cv := &codecGains{}
	m := NewMixer(nil, true)
	m.SetVolume(50)
	m.SetCodecVolume(cv, nil)
	m.SetVolume(3)
	if got := cv.last(); got != stockVolumeGain(3) {
		t.Fatalf("codec gain = %g, want %g", got, stockVolumeGain(3))
	}
	block, gain, _ := m.stepInto(make([]byte, blockBytes), nil, pcm(blockFrames, 12000), true)
	if gain != 1 || sampleAt(block, 0) != 12000 {
		t.Fatalf("PCM = %d at gain %g, want 12000 at 1", sampleAt(block, 0), gain)
	}
	// A sink that cannot play the PCM at full scale keeps the volume in the PCM.
	if block, _, _ := m.stepInto(make([]byte, blockBytes), nil, pcm(blockFrames, 12000), false); sampleAt(block, 0) >= 12000 {
		t.Fatalf("non-deferred PCM = %d, want the master applied", sampleAt(block, 0))
	}
}

// A chime louder than the listening volume turns the codec up when it reaches the DAC,
// and the source is turned down by the same ratio in the PCM meanwhile.
func TestCodecVolumeBoostsForALouderAnnouncement(t *testing.T) {
	cv := &codecGains{}
	m := NewMixer(nil, true)
	m.SetVolume(3)
	m.SetCodecVolume(cv, func() time.Duration { return 10 * time.Millisecond })
	m.Announce(bytes.NewReader(bytes.Repeat(pcm(1, 1000), blockFrames)), stockVolumeGain(40))
	// The clip is read on its own goroutine: mix only once its first block is there.
	queued := func() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.ann) }
	for wait := time.Now().Add(5 * time.Second); queued() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(wait) {
			t.Fatal("the clip never reached the mixer")
		}
	}
	block, _, _ := m.stepInto(make([]byte, blockBytes), nil, pcm(blockFrames, 10000), true)
	m.curGain = 1 // the duck is a separate ramp
	if got := cv.last(); got != stockVolumeGain(3) {
		t.Fatalf("codec turned up before the clip reached the DAC: %g", got)
	}
	want := 10000*m.curGain*stockVolumeGain(3)/stockVolumeGain(40) + 1000
	if got := float64(sampleAt(block, 0)); got > want+2 {
		t.Fatalf("mixed PCM = %g, want at most %g", got, want+2)
	}
	deadline := time.Now().Add(5 * time.Second)
	for cv.last() != stockVolumeGain(40) {
		if time.Now().After(deadline) {
			t.Fatalf("codec gain = %g, never boosted to %g", cv.last(), stockVolumeGain(40))
		}
		time.Sleep(time.Millisecond)
	}
	for m.Announcing() {
		m.stepInto(make([]byte, blockBytes), nil, nil, true)
	}
	m.stepInto(make([]byte, blockBytes), nil, nil, true)
	for cv.last() != stockVolumeGain(3) {
		if time.Now().After(deadline) {
			t.Fatalf("codec gain = %g after the clip, want %g", cv.last(), stockVolumeGain(3))
		}
		time.Sleep(time.Millisecond)
	}
}
