// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/spotify"
)

// errSpotifyCatalogue is a catalogue failure whose detail must not reach the browser.
var errSpotifyCatalogue = errors.New("catalogue /x: HTTP 500 secret-ish body")

func TestSpotifyImageCacheStaysBounded(t *testing.T) {
	t.Parallel()

	var cache imageCache

	blob := make([]byte, 300<<10)
	for i := range 40 {
		cache.put(imageEntry{id: strconv.Itoa(i), typ: "image/jpeg", data: blob})
	}

	if cache.size > imageCacheBytes {
		t.Fatalf("cache holds %d bytes, limit %d", cache.size, imageCacheBytes)
	}

	if _, ok := cache.get("39"); !ok {
		t.Fatal("newest image evicted")
	}

	if _, ok := cache.get("0"); ok {
		t.Fatal("oldest image kept past the limit")
	}
}

func TestSpotifyErrorCodes(t *testing.T) {
	t.Parallel()

	srv := new(Server)
	srv.log = quiet()

	for err, want := range map[error]string{
		spotify.ErrNotAuthenticated:                 "spotify_not_authenticated",
		spotify.ErrNotConnected:                     "spotify_not_connected",
		spotify.ErrUnavailable:                      "spotify_content_unavailable",
		fmt.Errorf("%w: boom", spotify.ErrPlayback): "spotify_playback_failed",
		errSpotifyCatalogue:                         "spotify_playback_failed",
	} {
		rec := httptest.NewRecorder()
		srv.spotifyError(rec, "t", err)

		if !strings.Contains(rec.Body.String(), `"error":"`+want+`"`) {
			t.Errorf("%v -> %s, want %s", err, rec.Body, want)
		}

		if strings.Contains(rec.Body.String(), "boom") || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("error detail reached the browser: %s", rec.Body)
		}
	}
}

func TestSpotifyTrackFollowsTheHook(t *testing.T) {
	t.Parallel()

	srv, _, _ := radioServer(t)
	post := func(body string) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/nowplaying", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		srv.setNowPlaying(httptest.NewRecorder(), req)
	}
	post(`{"source":"SPOTIFY","uri":"spotify:track:a"}`)

	if got := srv.spotifyTrack.Load(); got == nil || *got != "spotify:track:a" {
		t.Fatalf("track = %v", got)
	}

	post(`{"source":"AIRPLAY","uri":"spotify:track:b"}`)

	if got := srv.spotifyTrack.Load(); *got != "spotify:track:a" {
		t.Errorf("another source moved the track to %q", *got)
	}
}

func TestSpotifyRoutesWithoutReceiver(t *testing.T) {
	t.Parallel()

	s := new(Server)
	s.log = quiet()
	mux := &routeMux{ServeMux: http.NewServeMux(), patterns: nil}
	s.spotifyRoutes(mux)

	for _, path := range []string{
		"/api/spotify/search?q=x", "/api/spotify/albums/2noRn2Aes5aoNVsU6iWThc", "/api/spotify/liked",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))

		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "spotify_not_connected") {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/spotify/status", http.NoBody))

	if !strings.Contains(w.Body.String(), `"state":"unavailable"`) {
		t.Errorf("status: %s", w.Body)
	}
}
