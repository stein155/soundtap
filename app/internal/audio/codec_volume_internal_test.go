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
	t.Parallel()

	codec := &codecGains{mu: sync.Mutex{}, gains: nil}
	mixer := NewMixer(nil, true)
	mixer.SetVolume(50)
	mixer.SetCodecVolume(codec, nil)
	mixer.SetVolume(3)

	if got := codec.last(); got != stockVolumeGain(3) {
		t.Fatalf("codec gain = %g, want %g", got, stockVolumeGain(3))
	}

	block, gain, _ := mixer.stepInto(make([]byte, blockBytes), nil, pcm(blockFrames, 12000), true)
	if gain != 1 || sampleAt(block, 0) != 12000 {
		t.Fatalf("PCM = %d at gain %g, want 12000 at 1", sampleAt(block, 0), gain)
	}
	// A sink that cannot play the PCM at full scale keeps the volume in the PCM.
	block, _, _ = mixer.stepInto(make([]byte, blockBytes), nil, pcm(blockFrames, 12000), false)
	if sampleAt(block, 0) >= 12000 {
		t.Fatalf("non-deferred PCM = %d, want the master applied", sampleAt(block, 0))
	}
}

// A chime louder than the listening volume turns the codec up when it reaches the DAC,
// and the source is turned down by the same ratio in the PCM meanwhile.
func TestCodecVolumeBoostsForALouderAnnouncement(t *testing.T) {
	t.Parallel()

	codec := &codecGains{mu: sync.Mutex{}, gains: nil}
	mixer := NewMixer(nil, true)
	mixer.SetVolume(3)
	mixer.SetCodecVolume(codec, func() time.Duration { return 10 * time.Millisecond })
	mixer.Announce(bytes.NewReader(bytes.Repeat(pcm(1, 1000), blockFrames)), stockVolumeGain(40))
	// The clip is read on its own goroutine: mix only once its first block is there.
	queued := func() int {
		mixer.mu.Lock()
		defer mixer.mu.Unlock()

		return len(mixer.ann)
	}
	for wait := time.Now().Add(5 * time.Second); queued() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(wait) {
			t.Fatal("the clip never reached the mixer")
		}
	}

	block, _, _ := mixer.stepInto(make([]byte, blockBytes), nil, pcm(blockFrames, 10000), true)
	mixer.curGain = 1 // the duck is a separate ramp

	if got := codec.last(); got != stockVolumeGain(3) {
		t.Fatalf("codec turned up before the clip reached the DAC: %g", got)
	}

	want := 10000*mixer.curGain*stockVolumeGain(3)/stockVolumeGain(40) + 1000
	if got := float64(sampleAt(block, 0)); got > want+2 {
		t.Fatalf("mixed PCM = %g, want at most %g", got, want+2)
	}

	deadline := time.Now().Add(5 * time.Second)
	for codec.last() != stockVolumeGain(40) {
		if time.Now().After(deadline) {
			t.Fatalf("codec gain = %g, never boosted to %g", codec.last(), stockVolumeGain(40))
		}

		time.Sleep(time.Millisecond)
	}

	for mixer.Announcing() {
		mixer.stepInto(make([]byte, blockBytes), nil, nil, true)
	}

	mixer.stepInto(make([]byte, blockBytes), nil, nil, true)

	for codec.last() != stockVolumeGain(3) {
		if time.Now().After(deadline) {
			t.Fatalf("codec gain = %g after the clip, want %g", codec.last(), stockVolumeGain(3))
		}

		time.Sleep(time.Millisecond)
	}
}
