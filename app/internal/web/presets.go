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

	"github.com/stein155/soundtap/app/internal/safehttp"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/store"
)

const (
	// presetsTimeout bounds reading the presets.
	presetsTimeout = 5 * time.Second
	// presetWriteTimeout bounds storing or deleting a preset.
	presetWriteTimeout = 8 * time.Second
)

// The speaker's source names.
const (
	sourceTuneIn  = "TUNEIN"
	sourceSpotify = "SPOTIFY"
)

// presets returns the speaker's presets. speakerstate is their one source of truth: the
// store under it is only where it persists them.
func (s *Server) presets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), presetsTimeout)
	defer cancel()

	stored, err := s.backend.Presets(ctx)
	if err != nil {
		s.fail(w, "read presets", err)

		return
	}
	// A preset stored by a stock app carries the speaker's display icon, a file:// path a
	// browser cannot load.
	for i := range stored {
		if stored[i].Logo == speaker.InternetRadioIcon {
			stored[i].Logo = ""
		}
	}

	writeJSON(w, http.StatusOK, stored)
}

// presetList is the speaker's presets in the backup and preset-move shape.
func (s *Server) presetList(ctx context.Context) ([]store.Preset, error) {
	presets, err := s.backend.Presets(ctx)
	if err != nil {
		return nil, fmt.Errorf("presets: %w", err)
	}

	out := make([]store.Preset, 0, len(presets))
	for _, p := range presets {
		out = append(out, store.Preset{
			Slot: p.Slot, StationID: p.StationID, Source: p.Source,
			Type: p.Type, Location: p.Location, Name: p.Name, Logo: p.Logo,
		})
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

	var preset store.Preset

	err := json.NewDecoder(r.Body).Decode(&preset)
	if err != nil {
		s.badRequest(w, "valid stationId required", err)

		return
	}

	preset.Slot = slot

	err = speaker.CheckPreset(preset.Name, preset.Logo)
	if err != nil {
		s.badRequest(w, err.Error(), nil)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), presetWriteTimeout)
	defer cancel()

	if preset.Source == sourceSpotify {
		// A Spotify URI is all a Spotify preset holds: no account data, no token.
		// The stock cloud mirror has no way to play it, so it is not mirrored.
		if !validSpotifyPreset(preset.Location) {
			s.badRequest(w, "valid spotify uri required", nil)

			return
		}

		preset.StationID, preset.Type = "", "uri"

		storeErr := s.backend.StorePreset(ctx, slot, sourceSpotify, "uri", preset.Location, preset.Name, preset.Logo)
		if storeErr != nil {
			s.fail(w, "save failed", storeErr)

			return
		}

		s.log.Info("store preset", "slot", slot, "spotify", preset.Location, "name", preset.Name)
		s.hub.nudge()
		writeJSON(w, http.StatusOK, preset)

		return
	}

	if !validStation(r.Context(), preset.StationID) {
		if speaker.OwnStream(preset.StationID) {
			s.badRequest(w, "stream address is not usable", nil)

			return
		}

		s.badRequest(w, "valid stationId required", nil)

		return
	}

	if preset.Logo == "" {
		lookup, done := context.WithTimeout(ctx, logoLookupTimeout)
		_, preset.Logo = s.tunein.Describe(lookup, preset.StationID) // no logo is fine: the row shows its name

		done()
	}

	loc := speaker.StationLocation(preset.StationID)

	err = s.backend.StorePreset(ctx, slot, sourceTuneIn, "stationurl", loc, preset.Name, preset.Logo)
	if err != nil {
		s.fail(w, "save failed", err)

		return
	}

	s.log.Info("store preset", "slot", slot, "station", safehttp.LogURL(preset.StationID), "name", preset.Name)
	s.hub.nudge()
	writeJSON(w, http.StatusOK, preset)
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

	ctx, cancel := context.WithTimeout(r.Context(), presetWriteTimeout)
	defer cancel()

	err := s.backend.RemovePreset(ctx, slot)
	if err != nil {
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
	ps, err := s.backend.Presets(ctx)
	if err == nil && !slices.ContainsFunc(ps, func(p speaker.Preset) bool { return p.Slot == slot }) {
		apiError(w, http.StatusNotFound, "preset is empty")

		return
	}

	s.snapshotRecent(ctx)

	err = s.backend.PlayPreset(ctx, slot)
	if err != nil {
		s.fail(w, "play failed", err)

		return
	}

	s.log.Info("play preset", "slot", slot)
	s.hub.nudge() // push the new playback state to browsers at once
	writeJSON(w, http.StatusOK, map[string]int{"playing": slot})
}

func slotOf(w http.ResponseWriter, r *http.Request) (int, bool) {
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil || slot < 1 || slot > speaker.PresetSlots {
		apiError(w, http.StatusBadRequest, fmt.Sprintf("slot must be 1..%d", speaker.PresetSlots))

		return 0, false
	}

	return slot, true
}
