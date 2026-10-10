// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// checkFavorite takes what the speaker can play again later, the same things a preset holds.
func checkFavorite(e favorites.Entry) error {
	if err := speaker.CheckPreset(e.Name, e.Art); err != nil {
		return err
	}
	switch e.Source {
	case sourceTuneIn:
		if validStationID(e.Location) || validOwnStream(e.Location) {
			return nil
		}
	case sourceSpotify:
		if validSpotifyPreset(e.Location) {
			return nil
		}
	}
	return fmt.Errorf("favorite %q cannot be played", e.Name)
}

// favoriteMaxBytes is the most one add request may carry; a backup sizes itself by it.
const favoriteMaxBytes = 8192

func (s *Server) getFavorites(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.favoritesStore.All())
}

func (s *Server) addFavorite(w http.ResponseWriter, r *http.Request) {
	var e favorites.Entry
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, favoriteMaxBytes)).Decode(&e); err != nil {
		s.badRequest(w, "valid favorite required", err)
		return
	}
	if err := checkFavorite(e); err != nil {
		s.badRequest(w, err.Error(), nil)
		return
	}
	if e.Source == sourceTuneIn && e.Art == "" && validStationID(e.Location) && s.tunein != nil {
		// TuneIn's old logo CDN, the UI's fallback, has none for newer stations.
		lookup, done := context.WithTimeout(r.Context(), 3*time.Second)
		_, logo := s.tunein.Describe(lookup, e.Location)
		done()
		e.Art = browserArt(logo)
	}
	err := s.favoritesStore.Add(e)
	if errors.Is(err, favorites.ErrFull) {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Source == "" {
		s.badRequest(w, "source required", err)
		return
	}
	if err := s.favoritesStore.Remove(body.Source, body.Location); err != nil {
		s.fail(w, "favorites not saved", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
