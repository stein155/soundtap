// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// leaderKeys are what a grouped follower forwards to its leader: the transport.
// Volume stays per room and a preset leaves the group, as stock's slave does.
var leaderKeys = map[string]bool{
	"PLAY": true, "PAUSE": true, "PLAY_PAUSE": true, "STOP": true,
	"NEXT_TRACK": true, "PREV_TRACK": true,
}

// forwardKeyTimeout bounds a forwarded key: the button loop waits for it, and an
// unreachable leader must fall back to the local key quickly.
const forwardKeyTimeout = 1500 * time.Millisecond

// pairKeys are forwarded as well when the speaker is half of a stereo pair: the
// pair is one speaker, so power and presets act on both halves through the leader.
var pairKeys = map[string]bool{
	"POWER": true, "PRESET_1": true, "PRESET_2": true, "PRESET_3": true,
	"PRESET_4": true, "PRESET_5": true, "PRESET_6": true,
}

// paired reports whether this speaker plays one channel of a stereo pair. The channel
// setting is the pair's only per-speaker state (ADR 0107); the group is multiroom's.
func (s *Server) paired() bool {
	if s.settings == nil {
		return false
	}
	c := s.settings.Get().Channel
	return c == "left" || c == "right"
}

// keyAllowed reports whether key may travel between the halves of this group.
func (s *Server) keyAllowed(key string) bool {
	return leaderKeys[key] || pairKeys[key] && s.paired()
}

// ForwardsKey reports whether a follower forwards key to its leader.
func (s *Server) ForwardsKey(key string) bool { return s.keyAllowed(key) }

// postKey applies a transport key a follower forwarded. POST /api/key {"key":"NEXT_TRACK"}
func (s *Server) postKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !s.keyAllowed(body.Key) {
		apiError(w, http.StatusBadRequest, "transport key required (power and presets only in a stereo pair)")
		return
	}
	if err := s.backend.Key(r.Context(), body.Key); err != nil {
		s.fail(w, "key failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": body.Key})
}

// ForwardKey sends a transport key to the leader this speaker follows. An error means
// there is no leader or it did not take the key, and the caller acts locally.
func (s *Server) ForwardKey(ctx context.Context, key string) error {
	host, _ := s.follow()
	if host == "" {
		return errors.New("not following a leader")
	}
	ctx, cancel := context.WithTimeout(ctx, forwardKeyTimeout)
	defer cancel()
	b, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "http", Host: host, Path: "/api/key"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := peerClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("leader %s answered %s", host, resp.Status)
	}
	return nil
}
