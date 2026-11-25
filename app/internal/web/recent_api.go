// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// recentArtTimeout bounds looking up the art of a recent item.
const recentArtTimeout = 5 * time.Second

// heardAgain reports whether playing is something the recent list keeps: a source the
// speaker can start again, that was actually heard.
func heardAgain(playing *speaker.NowPlaying) bool {
	if playing == nil || playing.Source == "" || playing.Source == "STANDBY" {
		return false
	}

	if playing.Source == sourceBluetooth {
		return false // a phone's name or song: nothing the speaker could start again
	}
	// Only record something that was actually heard: a URL that never starts buffers
	// again and again, and must not stay behind on Home.
	if playing.PlayStatus != "PLAY_STATE" && playing.PlayStatus != "PAUSE_STATE" {
		return false
	}

	return !playing.Unheard // paused before its first audio
}

// snapshotRecent reads the current NowPlaying and, if something was actively
// playing, appends an entry to the recent list. Call just before any action
// that changes the source (stop, new source, source released).
func (s *Server) snapshotRecent(ctx context.Context) {
	if s.recentStore == nil {
		return
	}

	playing, err := s.backend.NowPlaying(ctx)
	if err != nil || !heardAgain(playing) {
		return
	}

	name := cmp.Or(playing.Station, playing.Track, playing.Artist)
	if name == "" {
		return // nothing identifiable to show
	}
	// What can be started again: a TuneIn station, or a Spotify list or track started
	// here. A UPnP controller's stream URL (Music Assistant's flow) is gone once it stops.
	loc := ""
	if playing.Source == sourceTuneIn || (playing.Source == "SPOTIFY" && validSpotifyPreset(playing.Location)) {
		loc = playing.Location
	}

	err = s.recentStore.Record(recent.Entry{
		Source:    playing.Source,
		Name:      name,
		Location:  loc,
		Art:       browserArt(playing.Art),
		StoppedAt: time.Now(),
	})
	if err != nil && !errors.Is(err, atomicjson.ErrUnreadable) {
		// Not fatal to the source change the caller is about to make. An unreadable
		// file was logged once when the list opened and is in the support bundle, so
		// every source change does not repeat it.
		s.log.Warn("recent list not persisted", "err", err)
	}
}

// getRecent returns the recent list as JSON, newest first, up to 20 entries.
func (s *Server) getRecent(w http.ResponseWriter, r *http.Request) {
	if s.recentStore == nil {
		writeJSON(w, http.StatusOK, []recent.Entry{})

		return
	}

	all := s.recentStore.All()
	for i := range all {
		all[i].Art = browserArt(all[i].Art) // older entries may still hold the original address
	}

	s.fillRecentArt(r.Context(), all)
	writeJSON(w, http.StatusOK, all)
}

// fillRecentArt looks up the logo of each TuneIn station on the list that has none (one
// started from the UI only carried the speaker's display icon) and stores it. The UI's
// fallback, TuneIn's old logo CDN, has no logo for newer stations. It runs in the
// background, once per station per boot: this answer goes out at once and the next read
// has the logo.
func (s *Server) fillRecentArt(ctx context.Context, all []recent.Entry) {
	if s.tunein == nil {
		return
	}

	for _, entry := range all {
		if entry.Source != sourceTuneIn || entry.Art != "" || entry.Location == "" {
			continue
		}

		if _, tried := s.recentArtTried.LoadOrStore(entry.Location, true); tried {
			continue
		}

		// The lookup outlives this request: its answer is for the next read.
		lookupCtx := context.WithoutCancel(ctx)

		safego.Go("recent.art", func() { s.lookUpRecentArt(lookupCtx, &entry) })
	}
}

func (s *Server) lookUpRecentArt(ctx context.Context, entry *recent.Entry) {
	ctx, done := context.WithTimeout(ctx, recentArtTimeout)
	defer done()

	_, logo := s.tunein.Describe(ctx, path.Base(entry.Location))
	if browserArt(logo) == "" {
		return
	}

	err := s.recentStore.SetArt(entry.Source, entry.Location, logo)
	if err != nil && !errors.Is(err, atomicjson.ErrUnreadable) {
		s.log.Warn("recent logo not saved", "station", entry.Location, "err", err)
	}
}

// delRecent takes one entry off the recent list, named by its source and location.
func (s *Server) delRecent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source   string `json:"source"`
		Location string `json:"location"`
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, smallBody)).Decode(&body)
	if err != nil || body.Source == "" {
		s.badRequest(w, "source required", err)

		return
	}

	if s.recentStore != nil {
		err := s.recentStore.Remove(body.Source, body.Location)
		if err != nil {
			s.fail(w, "recent list not saved", err)

			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// browserArt keeps artwork a browser can load. A station started from the UI carries the
// speaker's own display icon (speaker.InternetRadioIcon, a file:// path): stored as-is,
// every recent tile asked /api/logo for it and got a 400 instead of the station's logo
// . Empty lets the UI fall back to the TuneIn logo for the station id.
func browserArt(art string) string {
	if strings.HasPrefix(art, "http://") || strings.HasPrefix(art, "https://") {
		return art
	}

	return ""
}
