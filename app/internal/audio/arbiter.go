// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"io"
	"sync"
	"time"
)

// Arbiter is the single audio owner: exclusive source ownership plus the Mixer's
// announcement duck/restore. One per soundtapd.
type Arbiter struct {
	mix *Mixer

	mu      sync.Mutex
	owner   string           // current exclusive source, "" when idle/standby
	preempt func(old string) // hardware hook: stop the previous source's daemon
}

// NewArbiter wraps mix. preempt (nil-safe) is called with the outgoing source name
// whenever ownership changes hands, so hardware can stop the old daemon writing the
// bus. In tests/QEMU pass nil.
func NewArbiter(mix *Mixer, preempt func(old string)) *Arbiter {
	return &Arbiter{mix: mix, mu: sync.Mutex{}, owner: "", preempt: preempt}
}

// Acquire makes src the exclusive owner of the audio bus. If a different source held
// it, preempt(old) fires. Idempotent for the current owner.
func (a *Arbiter) Acquire(src string) {
	a.mu.Lock()
	old := a.owner
	a.owner = src
	pre := a.preempt
	a.mu.Unlock()

	if old != "" && old != src && pre != nil {
		pre(old)
	}
}

// Release drops ownership if src still holds it (STANDBY / source stopped).
func (a *Arbiter) Release(src string) {
	a.mu.Lock()
	if a.owner == src {
		a.owner = ""
	}
	a.mu.Unlock()
}

// Owner returns the current exclusive source ("" when idle).
func (a *Arbiter) Owner() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.owner
}

// Announce ducks whatever is on the bus and plays clip over it. gain 0 => full.
func (a *Arbiter) Announce(clip io.Reader, gain float64) { a.mix.Announce(clip, gain) }

// SetVolume sets the master volume (0..100) on the bus.
func (a *Arbiter) SetVolume(vol int) { a.mix.SetVolume(vol) }

// SetBass sets the low-shelf gain (-9..0 dB) on the bus.
func (a *Arbiter) SetBass(gain int) { a.mix.SetBass(gain) }

// SetLive passes Mixer.SetLive through.
func (a *Arbiter) SetLive(buffer time.Duration) { a.mix.SetLive(buffer) }

// SetSourceGain passes Mixer.SetSourceGain through.
func (a *Arbiter) SetSourceGain(g float64) { a.mix.SetSourceGain(g) }

// Unhold passes Mixer.Unhold through.
func (a *Arbiter) Unhold() { a.mix.Unhold() }

// Held passes Mixer.Held through.
func (a *Arbiter) Held() bool { return a.mix.Held() }

// Announcing reports whether a clip is currently mixed in (so callers can keep the amp
// unmuted for an announcement even when no source owns the bus).
func (a *Arbiter) Announcing() bool { return a.mix.Announcing() }
