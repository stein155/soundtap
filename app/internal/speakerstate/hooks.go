// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import "github.com/stein155/soundtap/app/internal/speaker"

// OnPlayChange registers a hook fired outside the lock for UI-visible state changes,
// so the SSE hub can push them immediately instead of waiting for its next poll.
func (s *Speaker) OnPlayChange(fn func()) {
	s.lock()
	s.onPlayChange = fn
	s.unlock()
}

// OnSpotifyPreset registers the hook that plays a SPOTIFY preset. It runs on its
// own goroutine, since starting Spotify is a network round trip.
func (s *Speaker) OnSpotifyPreset(fn func(uri string)) {
	s.lock()
	s.onSpotifyPreset = fn
	s.unlock()
}

// AuxName registers where the name of the AUX input is read, each time it plays.
func (s *Speaker) AuxName(fn func() string) {
	s.lock()
	s.auxName = fn
	s.unlock()
}

// OnBluetoothSelect registers the hook that runs each time Bluetooth becomes the source.
// Stock opens pairing there when no device is paired yet.
func (s *Speaker) OnBluetoothSelect(fn func()) {
	s.lock()
	s.onBluetoothSelect = fn
	s.unlock()
}

// OnBluetoothPaired registers the check a wake makes before waking into Bluetooth: with
// nothing paired it wakes into TuneIn instead. It runs under the speaker lock, so it
// must be quick and must not call back into the speaker.
func (s *Speaker) OnBluetoothPaired(fn func() bool) {
	s.lock()
	s.btPaired = fn
	s.unlock()
}

// OnWakeStation registers the lookup POWER uses for a station to wake into when
// standby left nothing to replay. It runs under the speaker lock, so it must not
// call back into the speaker.
func (s *Speaker) OnWakeStation(fn func() (location, name, art string, ok bool)) {
	s.lock()
	s.wakeStation = fn
	s.unlock()
}

// OnVolume registers a hook fired outside the lock, with the new level, whenever the
// volume changes — from the keypad, the IR remote, the API or a mute toggle. The audio
// arbiter uses it so a press reaches the mixer's gain immediately. Hooks add up and
// run in the order they were registered.
func (s *Speaker) OnVolume(fn func(int)) {
	s.lock()
	s.onVolume = append(s.onVolume, fn)
	s.unlock()
}

// outputLocked is the volume actually heard: 0 while muted. The volume hooks and the
// gain follow it. Caller holds s.mu.
func (s *Speaker) outputLocked() int {
	if s.muted {
		return 0
	}
	return s.volume
}

// Muted reports whether the output is silenced; Volume stays the level it returns to.
func (s *Speaker) Muted() bool {
	s.lock()
	defer s.unlock()
	return s.muted
}

// MarkMuted records that something other than this daemon silenced the output, the soundbar's
// processor. The processor already did the muting, so no volume hook fires: one would send it
// level 0. The next SetVolume ends it.
func (s *Speaker) MarkMuted() {
	s.lock()
	changed := !s.muted
	s.muted = true
	notify := s.onPlayChange
	s.unlock()
	if changed && notify != nil {
		notify()
	}
}

// volumeHookLocked returns the notification to fire once the lock is dropped, or nil
// when the volume did not change. Call with s.mu held, invoke the result after
// unlocking: hooks must never run under the state mutex.
func (s *Speaker) volumeHookLocked(before int) func() {
	if s.outputLocked() == before || len(s.onVolume) == 0 {
		return nil
	}
	s.volumeSeq++
	hooks, level, seq := s.onVolume, s.outputLocked(), s.volumeSeq
	return func() {
		s.volumeHookMu.Lock()
		defer s.volumeHookMu.Unlock()
		if seq < s.volumeDelivered {
			return
		}
		s.volumeDelivered = seq
		for _, fn := range hooks {
			fn(level)
		}
	}
}

// OnStandby registers a hook fired outside the lock each time the speaker enters
// standby from a source. Hooks add up and run in the order they were registered.
func (s *Speaker) OnStandby(fn func()) {
	s.lock()
	s.onStandby = append(s.onStandby, fn)
	s.unlock()
}

// standbyHookLocked returns the notification to fire once the lock is dropped, or nil
// when the speaker did not just enter standby. Call with s.mu held, invoke the result
// after unlocking, like volumeHookLocked.
func (s *Speaker) standbyHookLocked(wasStandby bool) func() {
	if wasStandby || s.source != sourceStandby || len(s.onStandby) == 0 {
		return nil
	}
	hooks := s.onStandby
	return func() {
		for _, fn := range hooks {
			fn()
		}
	}
}

// OnPresetSwap registers durable storage for a swap of two slots, as OnPresetChange
// does for one slot.
func (s *Speaker) OnPresetSwap(fn func(a, b int) error) {
	s.lock()
	s.presetSwap = fn
	s.unlock()
}

// OnPresetChange registers durable storage for TuneIn presets. Register after
// hydrating presets so startup does not rewrite the file it just loaded.
func (s *Speaker) OnPresetChange(fn func(slot int, preset *speaker.Preset) error) {
	s.lock()
	s.presetChange = fn
	s.unlock()
}
