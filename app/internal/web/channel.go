// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"encoding/json"
	"net/http"
	"slices"
)

// channels are what a speaker can play of a stereo source (audio.Channel): both, one of
// them as half of a stereo pair, or both mixed to mono.
var channels = []string{"stereo", "left", "right", "mono"}

func (s *Server) getChannel(w http.ResponseWriter, _ *http.Request) {
	c := s.settings.Get().Channel
	if c == "" {
		c = channels[0]
	}
	writeJSON(w, http.StatusOK, map[string]string{"channel": c})
}

func (s *Server) putChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || !slices.Contains(channels, body.Channel) {
		apiError(w, http.StatusBadRequest, "channel must be stereo, left, right or mono")
		return
	}
	if err := s.settings.SetChannel(body.Channel); err != nil {
		s.fail(w, "save channel failed", err)
		return
	}
	if s.channel != nil {
		s.channel(body.Channel)
	}
	writeJSON(w, http.StatusOK, map[string]string{"channel": body.Channel})
}
