// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// positioner is a backend that keeps where its track is.
type positioner interface {
	SetPosition(source string, positionMs, durationMs int)
	Position() (source string, positionMs, durationMs int)
}

// POST /api/position  {"source":"SPOTIFY","positionMs":1000,"durationMs":180000}
// Loopback-only: librespot's onevent hook. Without durationMs the last one stands.
func (s *Server) setPosition(w http.ResponseWriter, r *http.Request) {
	if !s.loopbackOnly(w, r) {
		return
	}
	body := struct {
		Source     string `json:"source"`
		PositionMs int    `json:"positionMs"`
		DurationMs int    `json:"durationMs"`
	}{DurationMs: -1}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil {
		s.badRequest(w, "bad body", err)
		return
	}
	if p, ok := s.backend.(positioner); ok {
		p.SetPosition(body.Source, body.PositionMs, body.DurationMs)
	}
	writeOK(w)
}

// GET /api/position says where the track is, for the time bar on the now-playing page. It
// stays out of /api/now: that one is pushed whenever it changes, and this changes always.
func (s *Server) position(w http.ResponseWriter, _ *http.Request) {
	var out struct {
		PositionMs int  `json:"positionMs"`
		DurationMs int  `json:"durationMs"`
		Seekable   bool `json:"seekable"`
	}
	if p, ok := s.backend.(positioner); ok {
		var source string
		source, out.PositionMs, out.DurationMs = p.Position()
		out.Seekable = s.seeker(source) != nil && out.DurationMs > 0
	}
	writeJSON(w, 200, out)
}

// seeker is how the track of source moves, or nil where it cannot.
func (s *Server) seeker(source string) func(context.Context, int) error {
	switch {
	case source == sourceSpotify && s.spotify != nil:
		return s.spotify.Seek
	case source == sourceUPnP && s.upnpSeek != nil:
		return func(ctx context.Context, ms int) error { return s.upnpSeek(ctx, time.Duration(ms)*time.Millisecond) }
	}
	return nil
}

// POST /api/seek {"positionMs":60000} moves the playing track, for the time bar: Spotify,
// or a track a UPnP controller sent.
func (s *Server) seek(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PositionMs int `json:"positionMs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.PositionMs < 0 {
		s.badRequest(w, "bad body", err)
		return
	}
	p, ok := s.backend.(positioner)
	if !ok {
		apiError(w, http.StatusConflict, "nothing to seek in")
		return
	}
	source, _, _ := p.Position()
	fn := s.seeker(source)
	if fn == nil {
		apiError(w, http.StatusConflict, "nothing to seek in")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := fn(ctx, body.PositionMs); err != nil {
		s.fail(w, "seek failed", err)
		return
	}
	writeOK(w)
}
