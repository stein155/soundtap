// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// logoLookupTimeout bounds looking up a station's logo while saving it.
const logoLookupTimeout = 3 * time.Second

// errNotPlayable is a favorite whose source this speaker cannot play.
var errNotPlayable = errors.New("cannot be played")

// checkFavorite takes what the speaker can play again later, the same things a preset holds.
func checkFavorite(entry favorites.Entry) error {
	err := speaker.CheckPreset(entry.Name, entry.Art)
	if err != nil {
		return err //nolint:wrapcheck // its text is the 400's message
	}

	switch entry.Source {
	case sourceTuneIn:
		if validStationID(entry.Location) || validOwnStream(entry.Location) {
			return nil
		}
	case sourceSpotify:
		if validSpotifyPreset(entry.Location) {
			return nil
		}
	default:
		// Anything else needs nothing here.
	}

	return fmt.Errorf("favorite %q %w", entry.Name, errNotPlayable)
}

// favoriteMaxBytes is the most one add request may carry; a backup sizes itself by it.
const favoriteMaxBytes = 8192

func (s *Server) getFavorites(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.favoritesStore.All())
}

func (s *Server) addFavorite(w http.ResponseWriter, r *http.Request) {
	var entry favorites.Entry

	err := decodeJSON(w, r, favoriteMaxBytes, &entry)
	if err != nil {
		s.badRequest(w, "valid favorite required", err)

		return
	}

	err = checkFavorite(entry)
	if err != nil {
		s.badRequest(w, err.Error(), nil)

		return
	}

	if entry.Source == sourceTuneIn && entry.Art == "" && validStationID(entry.Location) && s.tunein != nil {
		// TuneIn's old logo CDN, the UI's fallback, has none for newer stations.
		lookup, done := context.WithTimeout(r.Context(), logoLookupTimeout)
		_, logo := s.tunein.Describe(lookup, entry.Location)

		done()

		entry.Art = browserArt(logo)
	}

	err = s.favoritesStore.Add(entry)
	if err != nil {
		s.fail(w, "favorites not saved", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) delFavorite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source   string `json:"source"`
		Location string `json:"location"`
	}

	err := decodeJSON(w, r, smallBody, &body)
	if err != nil || body.Source == "" {
		s.badRequest(w, "source required", err)

		return
	}

	err = s.favoritesStore.Remove(body.Source, body.Location)
	if err != nil {
		s.fail(w, "favorites not saved", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
