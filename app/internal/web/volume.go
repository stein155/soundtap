// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// POST /api/nowplaying  {"source":sourceSpotify,"track":"…","artist":"…","art":"…"}
// Loopback-only: librespot's onevent hook pushes track metadata so /api/now shows the
// Spotify track/artist/cover, not just the bare SPOTIFY source.
func (s *Server) setNowPlaying(w http.ResponseWriter, r *http.Request) {
	if !s.loopbackOnly(w, r) {
		return
	}
	var body struct {
		Source string `json:"source"`
		Track  string `json:"track"`
		Artist string `json:"artist"`
		Album  string `json:"album"`
		Art    string `json:"art"`
		URI    string `json:"uri"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		s.badRequest(w, "bad body", err)
		return
	}
	if body.Source == sourceSpotify {
		s.spotifyTrack.Store(&body.URI)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	_ = s.backend.SetTrackMeta(ctx, body.Source, body.Track, body.Artist, body.Art)
	s.setAlbum(body.Source, body.Album)
	if f, ok := s.backend.(SpotifyFollower); ok && s.spotify != nil && body.Source == sourceSpotify {
		if uri, known := s.spotify.PlayingContext(ctx); known {
			f.FollowSpotify(uri)
		}
	}
	s.hub.nudge()
	writeJSON(w, 200, map[string]string{"track": body.Track, "artist": body.Artist})
}

// setAlbum passes a track's album on, for a backend that keeps one.
func (s *Server) setAlbum(source, album string) {
	if a, ok := s.backend.(interface{ SetAlbum(source, album string) }); ok {
		a.SetAlbum(source, album)
	}
}

func (s *Server) now(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	np, err := s.backend.NowPlaying(ctx)
	if err != nil {
		s.fail(w, "now failed", err)
		return
	}
	s.enrichNow(np)
	writeJSON(w, 200, np)
}

// enrichNow adds what the page shows beyond the speaker's own report: the station's art and
// whether next and previous have somewhere to go.
func (s *Server) enrichNow(np *speaker.NowPlaying) {
	s.np.Enrich(np)
	np.CanSkip = np.Source == sourceUPnP && s.upnpQueue != nil && s.upnpQueue.Holds(np.Location)
}

func (s *Server) getVolume(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	v, err := s.backend.Volume(ctx)
	if err != nil {
		s.fail(w, "volume failed", err)
		return
	}
	writeJSON(w, 200, map[string]any{"volume": v, "muted": muted(s.backend)})
}

// volumeMaxBody bounds a POST /api/volume body: {"volume":N,"source":"…","group":true}.
const volumeMaxBody = 1 << 10

// setVolume sets this speaker's volume. With "group": true -- the UI's group slider,
// never a per-room one -- and the owner's syncVolume preference on, the followers move
// by the same step (groupprefs.go). A group level may lie past 0..100: the leader stops
// at the bound, but the step it asks of the followers is the full one, so a leader at
// 100 can still bring a quieter follower up.
func (s *Server) setVolume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Volume *int   `json:"volume"`
		Source string `json:"source"` // sourceSpotify when librespot reports its own slider
		Group  bool   `json:"group"`  // move the followers too; see volumeSyncWanted
	}
	// A body without "volume" used to decode as 0 and mute the speaker.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, volumeMaxBody)).Decode(&body); err != nil || body.Volume == nil {
		s.badRequest(w, "volume required", err)
		return
	}
	asked := min(max(*body.Volume, -100), 200)
	volume := min(max(asked, 0), 100)
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	if body.Source == "SPOTIFY" && s.spotify != nil {
		s.spotify.NoteVolume(volume)
	}
	syncing := s.volumeSyncWanted(r, body.Group)
	// The read of the old level and the write of the new one are one step: two requests
	// of the same drag interleaving between them would both measure their step from the
	// same old level, and the followers would move by the wrong total.
	s.volumeMu.Lock()
	before := 0
	if syncing {
		v, err := s.backend.Volume(ctx)
		before, syncing = s.group.LeaderLevel(v), err == nil
	}
	err := s.backend.SetVolume(ctx, volume)
	if err == nil && syncing {
		s.group.NoteAsked(asked)
		if asked != before {
			s.group.SyncFollowerVolume(s.groupEnv(), asked-before)
		}
	}
	s.volumeMu.Unlock()
	if err != nil {
		s.fail(w, "set volume failed", err)
		return
	}
	s.hub.nudge() // push the new volume to other browsers at once
	writeJSON(w, 200, map[string]int{"volume": volume})
}
