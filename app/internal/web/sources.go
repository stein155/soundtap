// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
)

// What a source can be or report, as the API and the state stream name them.
const (
	sourceAUX         = "AUX"
	sourceMultiroom   = "MULTIROOM"
	sourceBluetooth   = "BLUETOOTH"
	sourceUPnP        = "UPNP"
	statusReady       = "READY"
	statusUnavailable = "UNAVAILABLE"
)

// dropBluetooth removes name from list when the speaker has no Bluetooth adapter.
func dropBluetooth(list []string, name string) []string {
	if !slices.Contains(list, name) || bluetoothPresent() {
		return list
	}

	return slices.DeleteFunc(slices.Clone(list), func(v string) bool { return v == name })
}

// availableSources are the user sources this speaker can offer right now.
func (s *Server) availableSources() []string { return dropBluetooth(s.userSources, sourceBluetooth) }

// validSource reports whether name is a source the daemon actually owns. Anything
// else would be stored verbatim in speaker state: the amp unmutes, the arbiter takes
// ownership on behalf of a source that has no daemon, and nothing ever releases it.
func validSource(name string) bool {
	switch name {
	case sourceTuneIn, sourceUPnP, "AIRPLAY", sourceBluetooth, sourceSpotify, sourceAUX, standbySource:
		return true
	}

	return false
}

func (s *Server) getUserSources(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.availableSources())
}

// setUserSource is LAN-facing control, separate from daemon-only /api/source.
func (s *Server) setUserSource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source string `json:"source"`
	}

	err := json.NewDecoder(io.LimitReader(r.Body, tinyBody)).Decode(&body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	allowed := false
	for _, source := range s.availableSources() {
		allowed = allowed || source == body.Source
	}

	if !allowed {
		apiError(w, http.StatusBadRequest, "source unavailable")

		return
	}

	s.sourcePick.Lock()
	defer s.sourcePick.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), backendCallTimeout)
	defer cancel()

	if s.sourceChanging != nil {
		s.sourceChanging(body.Source)
	}

	if s.pickInput != nil {
		err = s.pickInput(body.Source)
	} else {
		err = s.backend.Key(ctx, "AUX_INPUT")
		if err == nil && body.Source == sourceBluetooth {
			err = s.backend.Key(ctx, "SOURCE_TOGGLE")
		}
	}

	if err != nil {
		s.fail(w, "source failed", err)

		return
	}

	s.hub.nudge()
	writeJSON(w, http.StatusOK, map[string]string{"source": body.Source})
}

// setSource is called by shairport-sync (AirPlay) and bluealsa-aplay (Bluetooth)
// via airplay-source.sh / bt-source.sh to signal source start/stop.
// POST /api/source  {"source":"AIRPLAY"|sourceBluetooth|standbySource}
// When source is set, the TUNEIN player stops (it only plays when source==TUNEIN).
// When set to STANDBY the speaker returns to idle.
// flushedFor waits for source's queued audio to be dropped, when source owns the amp. It
// reports false when that cannot be done or said.
func (s *Server) flushedFor(ctx context.Context, source string) bool {
	playing, err := s.backend.NowPlaying(ctx)
	if err != nil {
		return false
	}

	if playing.Source != source {
		return true
	}

	return s.flushAudioWait != nil && s.flushAudioWait(ctx) == nil
}

func (s *Server) setSource(w http.ResponseWriter, r *http.Request) {
	// Loopback-only, like the other daemon hooks. Every caller is a local
	// *-source.sh; exposed to the LAN this endpoint let any host claim the amp under
	// an arbitrary source name, force standby by spoofing a stop, or kill the running
	// receivers in a loop through the flush branch.
	if !s.loopbackOnly(w, r) {
		return
	}

	var body struct {
		Source string `json:"source"`
		// "stop" => source released, "flush" => drop queued audio, "pause"/"play" => transport
		Event string `json:"event"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil || body.Source == "" {
		apiError(w, http.StatusBadRequest, "source required")

		return
	}

	if !validSource(body.Source) {
		apiError(w, http.StatusBadRequest, "unknown source")

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), backendCallTimeout)
	defer cancel()

	if body.Event == "flush_wait" {
		if !s.flushedFor(ctx, body.Source) {
			apiError(w, http.StatusServiceUnavailable, "audio_flush_failed")

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"source": body.Source, "event": body.Event})

		return
	}

	if s.sourceEvent(ctx, body.Source, body.Event) {
		writeJSON(w, http.StatusOK, map[string]string{"source": body.Source, "event": body.Event})

		return
	}

	if s.claimSource != nil && !s.claimSource(body.Source) {
		writeJSON(w, http.StatusOK, map[string]string{"source": body.Source, "event": "ignored"})

		return
	}

	s.snapshotRecent(ctx)

	if s.sourceChanging != nil {
		// A restarted Spotify sink reclaims the SAME source before writing PCM.
		// Its boundary already flushed synchronously. A second, asynchronous flush
		// here would discard the new track's opening after the producer resumes.
		np, err := s.backend.NowPlaying(ctx)
		if err != nil || np.Source != body.Source {
			s.sourceChanging(body.Source)
		}
	}

	if body.Source == standbySource {
		_ = s.backend.EnterStandby(ctx)
	} else {
		_ = s.backend.SetSource(ctx, body.Source)
	}

	s.hub.nudge()
	writeJSON(w, http.StatusOK, map[string]string{"source": body.Source})
}

// sourceEvent handles what a running source reports about itself; false means the request
// is a plain claim of the amp.
func (s *Server) sourceEvent(ctx context.Context, source, event string) bool {
	switch event {
	case "flush":
		// A flush only drops audio already queued for the DAC (a track change inside the
		// same source). It must NOT run the source-change handoff: that stops the daemons
		// owning the other sources, which turned every Spotify track boundary into a
		// SIGKILL of shairport-sync.
		// Only the source that owns the amp may flush: a paused background daemon (librespot
		// told to pause after another source took over) must not cut the new source's audio.
		np, err := s.backend.NowPlaying(ctx)
		if s.flushAudio != nil && (err != nil || np.Source == source) {
			s.flushAudio()
		}
	case "pause", "play":
		// A pause or resume in the source's own daemon (the Spotify app on a phone).
		if pb, ok := s.backend.(PauseRecorder); ok {
			pb.SetPaused(source, event == "pause")
		}

		s.hub.nudge()
	case "stop":
		// A source ending releases the amp: return to the interrupted source (or standby),
		// but only if it still owns it: a late stop must not clobber whoever took over.
		if s.stopSource != nil && !s.stopSource(source) {
			return true
		}

		s.snapshotRecent(ctx)
		_ = s.backend.ReleaseSource(ctx, source)
		s.hub.nudge()
	default:
		return false
	}

	return true
}
