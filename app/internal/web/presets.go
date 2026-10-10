// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/store"
)

// The speaker's source names.
const (
	sourceTuneIn  = "TUNEIN"
	sourceSpotify = "SPOTIFY"
)

// presets returns the speaker's presets. speakerstate is their one source of truth: the
// store under it is only where it persists them.
func (s *Server) presets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ps, err := s.backend.Presets(ctx)
	if err != nil {
		s.fail(w, "read presets", err)
		return
	}
	// A preset stored by a stock app carries the speaker's display icon, a file:// path a
	// browser cannot load.
	for i := range ps {
		if ps[i].Logo == speaker.InternetRadioIcon {
			ps[i].Logo = ""
		}
	}
	writeJSON(w, 200, ps)
}

// presetList is the speaker's presets in the backup and preset-move shape.
func (s *Server) presetList(ctx context.Context) ([]store.Preset, error) {
	ps, err := s.backend.Presets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Preset, 0, len(ps))
	for _, p := range ps {
		out = append(out, store.Preset{Slot: p.Slot, StationID: p.StationID, Source: p.Source,
			Type: p.Type, Location: p.Location, Name: p.Name, Logo: p.Logo})
	}
	return out, nil
}

// setPreset stores a NATIVE preset on the speaker (slot 1..6) via /storePreset, so
// it becomes a real preset button — not a disconnected local entry.
func (s *Server) setPreset(w http.ResponseWriter, r *http.Request) {
	slot, ok := slotOf(w, r)
	if !ok {
		return
	}
	var p store.Preset
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		s.badRequest(w, "valid stationId required", err)
		return
	}
	p.Slot = slot
	if err := speaker.CheckPreset(p.Name, p.Logo); err != nil {
		s.badRequest(w, err.Error(), nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if p.Source == sourceSpotify {
		// A Spotify URI is all a Spotify preset holds: no account data, no token.
		// The stock cloud mirror has no way to play it, so it is not mirrored.
		if !validSpotifyPreset(p.Location) {
			s.badRequest(w, "valid spotify uri required", nil)
			return
		}
		p.StationID, p.Type = "", "uri"
		if err := s.backend.StorePreset(ctx, slot, sourceSpotify, "uri", p.Location, p.Name, p.Logo); err != nil {
			s.fail(w, "save failed", err)
			return
		}
		s.log.Info("store preset", "slot", slot, "spotify", p.Location, "name", p.Name)
		s.hub.nudge()
		writeJSON(w, 200, p)
		return
	}
	if !validStation(r.Context(), p.StationID) {
		if speaker.OwnStream(p.StationID) {
			s.badRequest(w, "stream address is not usable", nil)
			return
		}
		s.badRequest(w, "valid stationId required", nil)
		return
	}
	if p.Logo == "" {
		lookup, done := context.WithTimeout(ctx, 3*time.Second)
		_, p.Logo = s.tunein.Describe(lookup, p.StationID) // no logo is fine: the row shows its name
		done()
	}
	loc := speaker.StationLocation(p.StationID)
	if err := s.backend.StorePreset(ctx, slot, sourceTuneIn, "stationurl", loc, p.Name, p.Logo); err != nil {
		s.fail(w, "save failed", err)
		return
	}
	s.log.Info("store preset", "slot", slot, "station", p.StationID, "name", p.Name)
	s.hub.nudge()
	writeJSON(w, 200, p)
}

// validSpotifyPreset accepts what a preset key can start: a track, album, artist or
// playlist URI, or Liked Songs.
func validSpotifyPreset(uri string) bool {
	_, _, ok := spotify.ParseURI(uri)
	return ok || uri == spotify.LikedContext
}

// delPreset clears a native preset slot on the speaker.
func (s *Server) delPreset(w http.ResponseWriter, r *http.Request) {
	slot, ok := slotOf(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := s.backend.RemovePreset(ctx, slot); err != nil {
		s.fail(w, "delete failed", err)
		return
	}
	s.hub.nudge()
	w.WriteHeader(http.StatusNoContent)
}

// playPreset plays the speaker's NATIVE preset for the slot by pressing the
// physical preset key (PRESET_1..6). The speaker stores its own presets (shown via
// GET /api/presets), so this plays exactly what the speaker has — no dependency on the
// local store.
func (s *Server) playPreset(w http.ResponseWriter, r *http.Request) {
	slot, ok := slotOf(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), writeDeadline)
	defer cancel()
	// An empty slot used to answer 200 {"playing":n} while the old source played on.
	if ps, err := s.backend.Presets(ctx); err == nil && !slices.ContainsFunc(ps, func(p speaker.Preset) bool { return p.Slot == slot }) {
		apiError(w, http.StatusNotFound, "preset is empty")
		return
	}
	s.snapshotRecent(ctx)
	if err := s.backend.PlayPreset(ctx, slot); err != nil {
		s.fail(w, "play failed", err)
		return
	}
	s.log.Info("play preset", "slot", slot)
	s.hub.nudge() // push the new playback state to browsers at once
	writeJSON(w, 200, map[string]int{"playing": slot})
}

func slotOf(w http.ResponseWriter, r *http.Request) (int, bool) {
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil || slot < 1 || slot > speaker.PresetSlots {
		apiError(w, http.StatusBadRequest, fmt.Sprintf("slot must be 1..%d", speaker.PresetSlots))
		return 0, false
	}
	return slot, true
}
