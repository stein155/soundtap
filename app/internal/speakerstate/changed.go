// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

// Loops that follow the speaker's state (the player, source routing, amp ownership) used
// to poll it every 20 to 50 ms, in standby too: over 200 wakeups a second on a single
// core, each taking the lock (ADR 0056). They now wait on Changed instead.

// playKey is the state those loops act on. Anything else they read either changes
// with one of these fields or they poll for it themselves.
type playKey struct {
	source, playStatus, location, ip string
	volume, bass                     int
	muted                            bool
	selection, playGen               uint64
	transportAck                     int64 // the Wireless Link lamp blinks on a transport key
}

func (s *Speaker) stateKey() playKey {
	return playKey{
		source: s.source, playStatus: s.playStatus, location: s.location, ip: s.IP,
		volume: s.volume, bass: s.bassTarget, muted: s.muted,
		selection: s.selectionGen, playGen: s.playGen, transportAck: s.transportAckUntil.UnixNano(),
	}
}

func (s *Speaker) lock() {
	s.mu.Lock()
	s.lockedKey = s.stateKey()
}

func (s *Speaker) unlock() {
	moved := s.stateKey() != s.lockedKey
	s.mu.Unlock()

	if moved {
		s.sigMu.Lock()
		if s.changed != nil {
			close(s.changed)
			s.changed = nil
		}
		s.sigMu.Unlock()
	}
}

// Changed returns a channel that is closed at the next change of source, play status,
// location, volume, bass, selection, playback attempt or IP. Take it before reading the
// state, so a change between the read and the wait is not missed.
func (s *Speaker) Changed() <-chan struct{} {
	s.sigMu.Lock()
	defer s.sigMu.Unlock()

	if s.changed == nil {
		s.changed = make(chan struct{})
	}

	return s.changed
}
