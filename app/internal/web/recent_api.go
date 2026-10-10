// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
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
)

// snapshotRecent reads the current NowPlaying and, if something was actively
// playing, appends an entry to the recent list. Call just before any action
// that changes the source (stop, new source, source released).
func (s *Server) snapshotRecent(ctx context.Context) {
	if s.recentStore == nil {
		return
	}
	np, err := s.backend.NowPlaying(ctx)
	if err != nil || np == nil || np.Source == "" || np.Source == "STANDBY" {
		return
	}
	if np.Source == sourceBluetooth {
		return // a phone's name or song: nothing the speaker could start again
	}
	// Only record something that was actually heard: a URL that never starts buffers
	// again and again, and must not stay behind on Home.
	switch np.PlayStatus {
	case "PLAY_STATE", "PAUSE_STATE":
	default:
		return
	}
	if np.Unheard {
		return // paused before its first audio
	}
	name := np.Station
	if name == "" {
		name = np.Track
	}
	if name == "" {
		name = np.Artist
	}
	if name == "" {
		return // nothing identifiable to show
	}
	// What can be started again: a TuneIn station, or a Spotify list or track started
	// here. A UPnP controller's stream URL (Music Assistant's flow) is gone once it stops.
	loc := ""
	if np.Source == sourceTuneIn || (np.Source == "SPOTIFY" && validSpotifyPreset(np.Location)) {
		loc = np.Location
	}
	if err := s.recentStore.Record(recent.Entry{
		Source:    np.Source,
		Name:      name,
		Location:  loc,
		Art:       browserArt(np.Art),
		StoppedAt: time.Now(),
	}); err != nil && !errors.Is(err, atomicjson.ErrUnreadable) {
		// Not fatal to the source change the caller is about to make. An unreadable
		// file was logged once when the list opened and is in the support bundle, so
		// every source change does not repeat it.
		s.log.Warn("recent list not persisted", "err", err)
	}
}

// getRecent returns the recent list as JSON, newest first, up to 20 entries.
func (s *Server) getRecent(w http.ResponseWriter, _ *http.Request) {
	if s.recentStore == nil {
		writeJSON(w, 200, []recent.Entry{})
		return
	}
	all := s.recentStore.All()
	for i := range all {
		all[i].Art = browserArt(all[i].Art) // older entries may still hold the original address
	}
	s.fillRecentArt(all)
	writeJSON(w, 200, all)
}

// fillRecentArt looks up the logo of each TuneIn station on the list that has none (one
// started from the UI only carried the speaker's display icon) and stores it. The UI's
// fallback, TuneIn's old logo CDN, has no logo for newer stations. It runs in the
// background, once per station per boot: this answer goes out at once and the next read
// has the logo.
func (s *Server) fillRecentArt(all []recent.Entry) {
	if s.tunein == nil {
		return
	}
	for _, e := range all {
		if e.Source != sourceTuneIn || e.Art != "" || e.Location == "" {
			continue
		}
		if _, tried := s.recentArtTried.LoadOrStore(e.Location, true); tried {
			continue
		}
		safego.Go("recent.art", func() { s.lookUpRecentArt(e) })
	}
}

func (s *Server) lookUpRecentArt(e recent.Entry) {
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_, logo := s.tunein.Describe(ctx, path.Base(e.Location))
	if browserArt(logo) == "" {
		return
	}
	if err := s.recentStore.SetArt(e.Source, e.Location, logo); err != nil && !errors.Is(err, atomicjson.ErrUnreadable) {
		s.log.Warn("recent logo not saved", "station", e.Location, "err", err)
	}
}

// delRecent takes one entry off the recent list, named by its source and location.
func (s *Server) delRecent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source   string `json:"source"`
		Location string `json:"location"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Source == "" {
		s.badRequest(w, "source required", err)
		return
	}
	if s.recentStore != nil {
		if err := s.recentStore.Remove(body.Source, body.Location); err != nil {
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
