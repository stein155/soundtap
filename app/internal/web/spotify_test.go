// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/spotify"
)

func TestSpotifyImageCacheStaysBounded(t *testing.T) {
	var c imageCache
	blob := make([]byte, 300<<10)
	for i := 0; i < 40; i++ {
		c.put(imageEntry{id: fmt.Sprint(i), typ: "image/jpeg", data: blob})
	}
	if c.size > imageCacheBytes {
		t.Fatalf("cache holds %d bytes, limit %d", c.size, imageCacheBytes)
	}
	if _, ok := c.get("39"); !ok {
		t.Fatal("newest image evicted")
	}
	if _, ok := c.get("0"); ok {
		t.Fatal("oldest image kept past the limit")
	}
}

func TestSpotifyErrorCodes(t *testing.T) {
	s := &Server{log: quiet()}
	for err, want := range map[error]string{
		spotify.ErrNotAuthenticated:                          "spotify_not_authenticated",
		spotify.ErrNotConnected:                              "spotify_not_connected",
		spotify.ErrUnavailable:                               "spotify_content_unavailable",
		fmt.Errorf("%w: boom", spotify.ErrPlayback):          "spotify_playback_failed",
		errors.New("catalogue /x: HTTP 500 secret-ish body"): "spotify_playback_failed",
	} {
		w := httptest.NewRecorder()
		s.spotifyError(w, "t", err)
		if !strings.Contains(w.Body.String(), `"error":"`+want+`"`) {
			t.Errorf("%v -> %s, want %s", err, w.Body, want)
		}
		if strings.Contains(w.Body.String(), "boom") || strings.Contains(w.Body.String(), "secret") {
			t.Errorf("error detail reached the browser: %s", w.Body)
		}
	}
}

func TestSpotifyTrackFollowsTheHook(t *testing.T) {
	s, _, _ := radioServer(t)
	post := func(body string) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/nowplaying", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		s.setNowPlaying(httptest.NewRecorder(), req)
	}
	post(`{"source":"SPOTIFY","uri":"spotify:track:a"}`)
	if got := s.spotifyTrack.Load(); got == nil || *got != "spotify:track:a" {
		t.Fatalf("track = %v", got)
	}
	post(`{"source":"AIRPLAY","uri":"spotify:track:b"}`)
	if got := s.spotifyTrack.Load(); *got != "spotify:track:a" {
		t.Errorf("another source moved the track to %q", *got)
	}
}

func TestSpotifyRoutesWithoutReceiver(t *testing.T) {
	s := &Server{log: quiet()}
	mux := &routeMux{ServeMux: http.NewServeMux()}
	s.spotifyRoutes(mux)
	for _, path := range []string{"/api/spotify/search?q=x", "/api/spotify/albums/2noRn2Aes5aoNVsU6iWThc", "/api/spotify/liked"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "spotify_not_connected") {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/spotify/status", nil))
	if !strings.Contains(w.Body.String(), `"state":"unavailable"`) {
		t.Errorf("status: %s", w.Body)
	}
}
