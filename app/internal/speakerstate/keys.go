// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// ApplyKey applies a key press (e.g. "POWER", "PRESET_3") directly.
func (s *Speaker) ApplyKey(key string) {
	s.applyKey(key)
}

// OnForwardKey registers the hook that sends a follower's keys to its leader.
func (s *Speaker) OnForwardKey(fn func(key string) bool) {
	s.lock()
	defer s.unlock()
	s.forwardKey = fn
}

// forwardsKey hands key to the leader while this speaker plays the group's stream. The
// hook runs outside the lock: it is a network call.
func (s *Speaker) forwardsKey(key string) bool {
	s.lock()
	fn, following := s.forwardKey, s.source == sourceMultiroom
	s.unlock()
	return fn != nil && following && fn(key)
}

// TransportAckWindow is how long TransportAck stays true: the length of the single
// blink stock plays, 250 ms dark and 250 ms lit.
const TransportAckWindow = 500 * time.Millisecond

// transportKeys are the ones stock acknowledges on the lamp. Volume and the presets
// are deliberately absent -- they have their own states, and on a box with two lamps
// those states do nothing.
var transportKeys = map[string]bool{
	keyPlay: true, keyPause: true, keyStop: true, keyPlayPause: true,
	"NEXT_TRACK": true, "PREV_TRACK": true, keyMute: true,
}

func (s *Speaker) applyKey(key string) {
	if s.forwardsKey(key) {
		return
	}
	if transportKeys[key] {
		s.lock()
		s.transportAckUntil = time.Now().Add(TransportAckWindow)
		s.unlock()
	}
	if key == "NEXT_TRACK" || key == "PREV_TRACK" {
		s.skipTrack(key)
		return
	}
	var notify func() // fired after the lock is released, never under it
	s.lock()
	volBefore, wasStandby := s.outputLocked(), s.source == sourceStandby
	defer func() {
		volChanged := s.volumeHookLocked(volBefore)
		standby := s.standbyHookLocked(wasStandby)
		s.unlock()
		if volChanged != nil {
			volChanged()
		}
		if notify != nil {
			notify()
		}
		if standby != nil {
			standby()
		}
	}()
	switch key {
	case keyMute, "VOLUME_UP", "VOLUME_DOWN":
		notify = s.volumeKeyLocked(key)
	case keyPause, keyPlay, keyPlayPause, keyStop:
		notify = s.transportKeyLocked(key)
	case "POWER":
		notify = s.powerKeyLocked()
	case "AUX_INPUT", sourceBT, "SOURCE_TOGGLE":
		s.sourceKeyLocked(key)
	default:
		notify = s.presetKeyLocked(key)
	}
}

// skipTrack forwards NEXT_TRACK/PREV_TRACK to the transport hook.
// Skips leave the lock: the hook shells out to the source's own control channel,
// and holding the state mutex across that would stall every reader.
func (s *Speaker) skipTrack(key string) {
	s.lock()
	fn, standby := s.transport, s.source == sourceStandby
	s.unlock()
	if fn != nil && !standby {
		_ = fn(key) // the hook logs; a source that cannot skip is not an error here
	}
}

// volumeKeyLocked handles MUTE and the volume buttons; the caller holds the lock.
func (s *Speaker) volumeKeyLocked(key string) func() {
	switch {
	case key == keyMute:
		s.muted = !s.muted
		// Unlike the volume ramp (which repeats while a button is held) this is one
		// discrete event, so pushing it costs nothing and the UI stays honest.
		return s.onPlayChange
	case s.muted:
		// Either button on a muted speaker only unmutes, back at the level it had: a
		// step from a level the user cannot hear would be a guess. The ramp repeats while
		// a button is held, so only the unmute, one discrete event, is pushed.
		s.muted = false
		return s.onPlayChange
	case key == "VOLUME_UP":
		s.volume = clamp(s.volume+1, 0, 100)
	default:
		s.volume = clamp(s.volume-1, 0, 100)
	}
	return nil
}

// transportKeyLocked handles PLAY/PAUSE/PLAY_PAUSE/STOP; the caller holds the lock.
func (s *Speaker) transportKeyLocked(key string) func() {
	if s.source == sourceStandby {
		return nil
	}
	pause := key == keyPause || key == keyStop || (key == keyPlayPause && s.playStatus != statePause)
	// Radio has nothing to pause: the player stops the stream on PAUSE_STATE and
	// tunes in again on play. Spotify and Bluetooth pause in their own daemon.
	// STOP is the same transition: a stop that only relabelled the state left the
	// stream playing and PLAY unable to undo it.
	if pause {
		s.playStatus = statePause
	} else if s.playStatus == statePause {
		s.playStatus = startStatus(s.source)
	} else if s.playStatus == stateStop && s.failedGen == s.playGen && startStatus(s.source) == stateBuffering {
		// The player gave up on a dropped stream: PLAY tunes in again, as a new selection.
		s.playStatus = stateBuffering
		s.selectionGen++
	}
	action := keyPlay
	if pause {
		action = keyPause
	}
	fn, changed := s.transport, s.onPlayChange
	return func() {
		if changed != nil {
			changed()
		}
		if fn != nil {
			s.queueTransport(func() { _ = fn(action) })
		}
	}
}

// powerKeyLocked handles POWER; the caller holds the lock.
func (s *Speaker) powerKeyLocked() func() {
	resume := s.powerToggleLocked(true) // same as the CLI `sys power`
	if resume == nil {
		return nil
	}
	changed := s.onPlayChange
	return func() {
		if changed != nil {
			changed()
		}
		resume()
	}
}

// sourceKeyLocked handles the AUX/Bluetooth source keys; the caller holds the lock.
func (s *Speaker) sourceKeyLocked(key string) {
	switch {
	case key == "AUX_INPUT":
		s.applySourceLocked(sourceAUX, "", sourceAUX, "")
	case key == sourceBT:
		s.applySourceLocked(sourceBT, "", sourceBT, "")
		s.playStatus = stateStop // until bt-source.sh reports A2DP, as below
	case s.source == sourceAUX: // SOURCE_TOGGLE
		s.applySourceLocked(sourceBT, "", sourceBT, "")
		// Picking Bluetooth from the front panel is not a daemon claiming the
		// amp — no phone need be connected yet — so the startStatus reasoning
		// ("push sources are playing by definition") does not apply here. Stay
		// out of PLAY_STATE until bt-source.sh reports a real A2DP connection,
		// which is what lets the panel show "discoverable, waiting for a
		// device" instead of pretending to play silence.
		s.playStatus = stateStop
	default:
		s.applySourceLocked(sourceAUX, "", sourceAUX, "")
	}
}

// presetKeyLocked handles PRESET_1..PRESET_6 and ignores any other key; the caller
// holds the lock.
func (s *Speaker) presetKeyLocked(key string) func() {
	// Play the station stored on that slot, like the firmware does when a preset
	// button is pressed.
	slot, ok := presetSlot(key)
	if !ok {
		return nil
	}
	p, exists := s.presets[slot]
	if !exists {
		// Empty slot: set a short window so the LED loop can blink amber
		// as visual feedback, matching the stock speaker's behaviour.
		s.emptyPresetUntil = time.Now().Add(2 * time.Second)
		return nil
	}
	s.emptyPresetUntil = time.Time{} // clear any pending empty-slot feedback
	if p.Source != sourceSpotify {
		s.applySourceLocked(p.Source, p.Location, p.Name, p.Art)
		return nil
	}
	fn := s.onSpotifyPreset
	if fn == nil {
		return nil
	}
	// Claim the amp now: the old source stops and the panel shows the
	// preset at once, instead of after librespot's ~2 s round trip.
	s.applySourceLocked(sourceSpotify, p.Location, p.Name, p.Art)
	s.playStatus = stateBuffering // until librespot reports the first track
	uri, changed := p.Location, s.onPlayChange
	return func() {
		if changed != nil {
			changed()
		}
		safego.Go("speakerstate.spotifyPreset", func() { fn(uri) })
	}
}

// presetSlot parses a "PRESET_<n>" key into its slot number.
func presetSlot(key string) (int, bool) {
	rest, ok := strings.CutPrefix(key, "PRESET_")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 || n > speaker.PresetSlots {
		return 0, false
	}
	return n, true
}

// queueTransport runs f, a play or pause for the source's own control, after the ones
// queued before it. A goroutine each ran a quick pause-then-play the other way round
// (the newest goroutine runs first), leaving the phone paused while the speaker said
// playing. Off the caller's goroutine still: the hook shells out or calls the network. A
// full queue means the source stopped answering; the press is dropped, not blocked on.
func (s *Speaker) queueTransport(f func()) {
	s.transportOnce.Do(func() {
		s.transportQ = make(chan func(), 16)
		safego.Go("speakerstate.transport", func() {
			for f := range s.transportQ {
				f()
			}
		})
	})
	select {
	case s.transportQ <- f:
	default:
	}
}
