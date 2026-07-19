// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// SetTrackMeta updates the now-playing track/artist/cover for an external source that
// carries metadata (Spotify via librespot, later AirPlay). Ignored in standby, or when
// a source is named and no longer owns the amp, so a late metadata event can't paint
// over a source that already switched. Empty fields are left untouched.
func (s *Speaker) SetTrackMeta(ctx context.Context, source, track, artist, art string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lock()
	defer s.unlock()
	if source == "AIRPLAY" { //nolint:goconst // moved unchanged
		// Latch even if the source hasn't flipped to AIRPLAY yet, so applySourceLocked
		// can restore it the moment it does (F25 first-track race).
		s.pendTrack, s.pendArtist, s.pendArt, s.pendAlbum = track, artist, art, ""
	}
	if s.source == sourceStandby || (source != "" && s.source != source) {
		return nil
	}
	s.track, s.artist, s.album = track, artist, ""
	if art != "" {
		s.art = art
	}
	// Metadata says what plays, not that it plays: Spotify sends it on a track change
	// while paused too, and only a resume (SetPaused) lifts a pause.
	if s.playStatus != statePause {
		s.playStatus = statePlay
		s.unheard = false
	}
	return nil
}

// SetBluetoothTrack shows the connected phone, as the station, and what it says is playing.
// Unlike SetTrackMeta it leaves the play status alone: a phone keeps naming its track while
// stopped or paused.
func (s *Speaker) SetBluetoothTrack(phone, track, artist, album string) {
	s.lock()
	defer s.unlock()
	if s.source == sourceBT {
		s.station, s.track, s.artist, s.album = phone, track, artist, album
	}
}

// SetAlbum names the album of the track SetTrackMeta just set, which cleared the last one.
func (s *Speaker) SetAlbum(source, album string) {
	s.lock()
	defer s.unlock()
	if source == "AIRPLAY" {
		s.pendAlbum = album
	}
	if s.source == source {
		s.album = album
	}
}

// SetPaused records that source paused or resumed in its own daemon, after a command
// that the daemon already acknowledged (the web UI's Spotify pause/resume). It is the
// bookkeeping half of the PAUSE/PLAY keys without their transport hook: the command
// has been sent, and sending it a second time from here would only race the first.
// Ignored in standby or when source no longer owns the amp, like SetTrackMeta, and a
// resume only lifts a pause -- it never promotes BUFFERING_STATE to playing.
func (s *Speaker) SetPaused(source string, paused bool) {
	s.lock()
	if s.source == sourceStandby || s.source != source {
		s.unlock()
		return
	}
	changed := false
	switch {
	case paused && s.playStatus != statePause:
		// The position stops where the pause found it, not at the last report.
		if s.playStatus == statePlay {
			s.positionMs += int(time.Since(s.positionAt).Milliseconds())
		}
		s.playStatus, changed = statePause, true
	case !paused && s.playStatus == statePause:
		s.playStatus, changed = startStatus(s.source), true
		s.positionAt = time.Now()
	}
	notify := s.onPlayChange
	s.unlock()
	if changed && notify != nil {
		notify()
	}
}

// ReleaseSource is called when an external source stops (AirPlay/BT/Spotify session
// ends). It only acts if src still owns the amp; a late stop cannot clobber a newer source.
//
// Ending an external session goes to standby; it deliberately does not resume
// whatever that session interrupted. Restarting a radio station because someone's
// AirPlay ended is startling — the speaker starts playing on its own, in a room
// the user may have left — and it is what docs/hardware/hardware-test.md checks for.
func (s *Speaker) ReleaseSource(ctx context.Context, src string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lock()
	if s.source != src {
		s.unlock()
		return nil // stale stop — a newer source owns the amp now
	}
	was := s.source == sourceStandby // a "stop" for STANDBY itself is not a transition
	defer func() {
		standby := s.standbyHookLocked(was)
		s.unlock()
		if standby != nil {
			standby()
		}
	}()
	s.source = sourceStandby
	s.releasedAt = time.Now()
	s.lastSource = "" // interrupted source is gone; wake defaults to TuneIn
	s.playStatus = stateStop
	// Leave nothing behind claiming to be playing. /api/now is served from these
	// fields, so a released source used to keep showing the last track, its station
	// name and its cover art next to a speaker that is off.
	s.location, s.station, s.art = "", "", ""
	s.track, s.artist, s.album = "", "", ""
	s.pendTrack, s.pendArtist, s.pendArt, s.pendAlbum = "", "", "", ""
	return nil
}

// replayableLocked reports whether the current source is one soundtapd can start
// again by itself — a TuneIn station (it has the location) or AUX (always there).
// Every other source needs a phone to push to us, so it cannot be woken into.
func (s *Speaker) replayableLocked() bool {
	return (s.source == sourceTuneIn && s.location != "") || s.source == sourceAUX
}

func (s *Speaker) EnterStandby(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lock()
	var standby func()
	if s.source != sourceStandby {
		s.powerToggleLocked(false)
		standby = s.standbyHookLocked(false)
	}
	s.unlock()
	if standby != nil {
		standby()
	}
	return nil
}

func (s *Speaker) applySource(source, location, name, art string) {
	s.lock()
	defer s.unlock()
	s.applySourceLocked(source, location, name, art)
}

func (s *Speaker) applySourceLocked(source, location, name, art string) {
	s.playGen++ // supersede any in-flight buffering confirmation from the old source
	s.selectionGen++
	s.startAt = 0
	prev := s.source
	if prev == sourceStandby {
		s.boundWakeVolumeLocked()
	}
	s.source = orDefault(source, sourceTuneIn)
	if fn := s.onBluetoothSelect; fn != nil && s.source == sourceBT && prev != sourceBT {
		safego.Go("speakerstate.bluetoothSelect", func() { fn() })
	}
	s.lastSource = s.source
	s.location = location
	s.station = name
	s.art = art
	s.track, s.artist, s.album = "", "", ""
	if s.source == "AIRPLAY" && (s.pendTrack != "" || s.pendArtist != "") {
		// Apply metadata that shairport pushed just before this source flip (F25).
		s.track, s.artist, s.album = s.pendTrack, s.pendArtist, s.pendAlbum
		if s.pendArt != "" {
			s.art = s.pendArt
		}
	} else {
		s.pendTrack, s.pendArtist, s.pendArt, s.pendAlbum = "", "", "", "" // drop stale latch off AirPlay
	}
	s.playStatus = startStatus(s.source)
	s.unheard = s.playStatus == stateBuffering
}

func (s *Speaker) PlayNotification(ctx context.Context, n speaker.Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(n.URL) == "" {
		return fmt.Errorf("notification URL is required")
	}
	s.lock()
	// No floor: a chime at the listening volume must follow a listener below 10 too. The
	// web API sets its own floor for clips from outside.
	volume := s.volume
	if n.Volume > 0 {
		volume = clamp(n.Volume, 1, 100)
	}
	note := &Notification{
		URL:    n.URL,
		Volume: volume,
		Artist: n.Artist,
		Album:  n.Album,
		Track:  n.Track,
	}
	s.lastNotification = note
	s.notifyUntil = time.Now().Add(8 * time.Second)
	fn := s.onNotify
	s.unlock()
	if fn != nil {
		fn(note) // hand off to the audio arbiter to duck + play the clip
	}
	return nil
}

// OnNotify registers a callback fired for each accepted audio notification, so the
// audio arbiter can duck the current source and play the clip. nil-safe.
func (s *Speaker) OnNotify(fn func(*Notification)) {
	s.lock()
	s.onNotify = fn
	s.unlock()
}

// SetIP updates the speaker's LAN IP (reported by /api/info and used for multiroom
// discovery/grouping). Safe to call concurrently as the network comes and goes.
func (s *Speaker) SetIP(ip string) {
	s.lock()
	s.IP = ip
	s.unlock()
}

// GetIP returns the current LAN IP.
func (s *Speaker) GetIP() string {
	s.lock()
	defer s.unlock()
	return s.IP
}

func stationIDFromLocation(location string) string {
	location = strings.TrimSpace(location)
	if location == "" || speaker.OwnStream(location) {
		return location
	}
	if i := strings.LastIndex(location, "/"); i >= 0 {
		return location[i+1:]
	}
	return location
}
