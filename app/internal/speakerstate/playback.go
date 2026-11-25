// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// startStatus is the playStatus a freshly selected source starts in. TuneIn and UPnP
// go through a resolve + ffmpeg connect before a single sample exists, so they start
// in BUFFERING_STATE and only reach PLAY_STATE once PCM actually reaches the DAC
// (ConfirmPlaying). Every other source is push-driven: by the time it claims the amp
// its daemon is already writing the bus, so it is playing by definition.
func startStatus(source string) string {
	if source == sourceTuneIn || source == sourceUPnP {
		return stateBuffering
	}

	return statePlay
}

// PlaybackState returns the current source, location, volume, and bass for direct use
// by internal/player.
//
//nolint:revive // function-result-limit: internal/player reads this exported signature.
func (s *Speaker) PlaybackState() (string, string, int, int) {
	selection := s.selection()

	return selection.source, selection.location, selection.volume, selection.bass
}

// PlaybackSelection includes explicit reselects so the player restarts the same preset.
//
//nolint:revive // function-result-limit: internal/player reads this exported signature.
func (s *Speaker) PlaybackSelection() (string, string, int, int, uint64) {
	selection := s.selection()

	return selection.source, selection.location, selection.volume, selection.bass, selection.gen
}

// playbackSelection is what the player plays: source, location, the volume heard, bass,
// and the selection's generation.
type playbackSelection struct {
	source, location string
	volume, bass     int
	gen              uint64
}

func (s *Speaker) selection() playbackSelection {
	s.lock()
	defer s.unlock()

	return playbackSelection{
		source: s.source, location: s.location, volume: s.outputLocked(), bass: s.bassTarget, gen: s.selectionGen,
	}
}

// StartAt is how far into the location the current selection starts playing.
func (s *Speaker) StartAt() time.Duration {
	s.lock()
	defer s.unlock()

	return s.startAt
}

// BeginBuffering marks source as (re)buffering and returns the generation that names
// this playback attempt; pass it back to ConfirmPlaying or FailPlayback. Returns 0 —
// which never matches a live generation — when source no longer owns the amp, so a
// decoder that lost the race can neither claim nor fail the current stream.
func (s *Speaker) BeginBuffering(source string) uint64 {
	s.lock()
	defer s.unlock()

	if s.source == sourceStandby || s.source != source {
		return 0
	}

	s.playGen++
	s.playStatus = stateBuffering

	return s.playGen
}

// ConfirmPlaying promotes a buffering source to PLAY_STATE once real audio is flowing.
// Ignored unless gen is still the current playback attempt.
func (s *Speaker) ConfirmPlaying(gen uint64) { s.setPlayStatusFor(gen, statePlay) }

// FailPlayback drops a buffering source back to STOP_STATE when the stream never
// produced audio (no stream URL, ffmpeg refused to start, watchdog expired), so the
// UI shows "off" instead of spinning forever. Ignored unless gen is still current.
func (s *Speaker) FailPlayback(gen uint64) { s.setPlayStatusFor(gen, stateStop) }

// EndPlayback marks the current attempt finished: a UPnP track played to its end. Unlike
// FailPlayback it also ends an attempt already confirmed as playing. Ignored unless gen
// is still current, so a track ending after the user moved on changes nothing.
func (s *Speaker) EndPlayback(gen uint64) {
	s.lock()

	if s.playGen != gen || s.source == sourceStandby || s.playStatus == stateStop || s.playStatus == statePause {
		s.unlock()

		return
	}

	s.playStatus = stateStop
	notify := s.onPlayChange
	s.unlock()

	if notify != nil {
		notify()
	}
}

// FailCurrentPlayback drops whatever is buffering right now back to STOP_STATE without
// naming a generation, for the case where nothing will ever confirm playback: soundtapd
// started with -no-audio because the board brought up no PCM device at all. Without it
// the WLA sat on "buffering" forever after selecting a station, with no hint why.
func (s *Speaker) FailCurrentPlayback() {
	s.lock()
	gen := s.playGen
	s.unlock()
	s.setPlayStatusFor(gen, stateStop)
}

// StillBuffering reports whether gen is the current attempt and has not yet been
// confirmed or failed -- the only case in which ConfirmPlaying or FailPlayback would act.
func (s *Speaker) StillBuffering(gen uint64) bool {
	s.lock()
	defer s.unlock()

	return s.stillBufferingLocked(gen)
}

func (s *Speaker) stillBufferingLocked(gen uint64) bool {
	return s.playGen == gen && s.source != sourceStandby && s.playStatus == stateBuffering
}

func (s *Speaker) setPlayStatusFor(gen uint64, status string) {
	s.lock()

	if !s.stillBufferingLocked(gen) {
		s.unlock()

		return
	}

	s.playStatus = status
	if status == statePlay {
		s.unheard = false
	}

	if status == stateStop {
		s.failedGen = gen
	}

	notify := s.onPlayChange
	s.unlock()

	if notify != nil {
		notify()
	}
}

// SetLeaderLive shows a follower's MULTIROOM as playing while its leader answers and as
// buffering while it does not, so the UI, MQTT and controllers do not report a silent
// follower as playing during WatchLeader's grace period. Only PLAY_STATE and
// BUFFERING_STATE trade places; any other source or status is left alone.
//
//nolint:revive // flag-parameter: whether the leader answers is the state shown, not a mode.
func (s *Speaker) SetLeaderLive(live bool) {
	status := stateBuffering
	if live {
		status = statePlay
	}

	s.lock()

	if s.source != sourceMultiroom || s.playStatus == status ||
		(s.playStatus != statePlay && s.playStatus != stateBuffering) {
		s.unlock()

		return
	}

	s.playStatus = status
	if status == statePlay {
		s.unheard = false
	}

	notify := s.onPlayChange
	s.unlock()

	if notify != nil {
		notify()
	}
}

// SetLeaderNow shows what the leader plays on a follower's MULTIROOM, for everything that
// reads this speaker's own state: the display, MQTT and its UPnP renderer. Source
// stays MULTIROOM, the routing fact, and location stays empty: the follower plays the
// leader's stream, not that item.
//
//nolint:gocritic // hugeParam: the exported signature the multiroom follower calls with a copy.
func (s *Speaker) SetLeaderNow(leader speaker.NowPlaying) {
	s.lock()

	same := s.track == leader.Track && s.artist == leader.Artist && s.album == leader.Album &&
		s.station == leader.Station && s.art == leader.Art
	if s.source != sourceMultiroom || same {
		s.unlock()

		return
	}

	s.track, s.artist, s.album, s.station, s.art = leader.Track, leader.Artist, leader.Album, leader.Station, leader.Art
	notify := s.onPlayChange
	s.unlock()

	if notify != nil {
		notify()
	}
}

// SetPosition records where source is in its track, as its player just said. A negative
// duration keeps the last one: only Spotify's track change says how long a track is.
func (s *Speaker) SetPosition(source string, positionMs, durationMs int) {
	s.lock()
	defer s.unlock()

	if source != s.posSource {
		s.durationMs = 0
	}

	s.posSource, s.positionMs, s.positionAt = source, positionMs, time.Now()
	if durationMs >= 0 {
		s.durationMs = durationMs
	}
}

// Position says how far the current source is into its track, counted on from the last
// report while it plays. Duration zero means nothing is known: a stream has no end.
func (s *Speaker) Position() (string, int, int) {
	s.lock()
	defer s.unlock()

	if s.source == sourceStandby || s.source != s.posSource || s.durationMs == 0 {
		return s.source, 0, 0
	}

	positionMs := s.positionMs
	if s.playStatus == statePlay {
		positionMs += int(time.Since(s.positionAt).Milliseconds())
	}

	return s.source, min(positionMs, s.durationMs), s.durationMs
}
