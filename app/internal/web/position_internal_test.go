// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPositionComesFromTheHook(t *testing.T) {
	t.Parallel()

	srv, spk, _ := radioServer(t)

	err := spk.Select(t.Context(), sourceUPnP, "", "http://192.0.2.1/x.mp3", "Song", "", "")
	if err != nil {
		t.Fatal(err)
	}

	post := func(body string) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/position", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		srv.setPosition(w, req)

		if w.Code != 200 {
			t.Fatalf("post %s = %d", body, w.Code)
		}
	}

	read := func() (int, int, bool) {
		rec := httptest.NewRecorder()
		srv.position(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/position", nil))

		var got struct {
			PositionMs int  `json:"positionMs"`
			DurationMs int  `json:"durationMs"`
			Seekable   bool `json:"seekable"`
		}

		err := json.Unmarshal(rec.Body.Bytes(), &got)
		if err != nil {
			t.Fatal(err)
		}

		return got.PositionMs, got.DurationMs, got.Seekable
	}
	if _, dur, _ := read(); dur != 0 {
		t.Errorf("nothing reported yet: duration %d", dur)
	}

	post(`{"source":"SPOTIFY","positionMs":0,"durationMs":180000}`)

	if _, dur, _ := read(); dur != 0 {
		t.Errorf("another source's track: duration %d", dur)
	}

	post(`{"source":"UPNP","positionMs":1000,"durationMs":180000}`)
	post(`{"source":"UPNP","positionMs":2000}`)

	pos, dur, seekable := read()
	if pos < 2000 || dur != 180000 || seekable {
		t.Errorf("got %d of %d, seekable %v", pos, dur, seekable)
	}
}

func TestPositionHookIsLoopbackOnly(t *testing.T) {
	t.Parallel()

	srv, _, _ := radioServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/position", strings.NewReader(`{}`))
	req.RemoteAddr = "192.0.2.9:1234"
	w := httptest.NewRecorder()
	srv.setPosition(w, req)

	if w.Code == 200 {
		t.Error("a remote client set the position")
	}
}
