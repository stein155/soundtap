// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Dangerous routes need a press on the speaker; the rest, peer routes included, do not.
func TestNeedsPress(t *testing.T) {
	for _, c := range []struct {
		method, target string
		want           bool
	}{
		{"POST", "/api/factory-reset", true},
		{"POST", "/api/reboot", true},
		{"POST", "/api/wifi", true},
		{"POST", "/api/bluetooth/clear", true},
		{"POST", "/api/bluetooth/pair", false},
		{"POST", "/api/restore", true},
		{"POST", "/api/setup", true},
		{"POST", "/api/update", false}, // updateApp presses only for a picked tag
		{"POST", "/api/firmware?install=1", true},
		{"POST", "/api/core-update", true},
		{"POST", "/api/firmware", false},
		{"POST", "/api/soundtapd", true},
		{"DELETE", "/api/soundtapd", true},
		{"GET", "/api/debug", true},
		{"POST", "/api/wifi/scan", false},
		{"POST", "/api/volume", false},
		{"POST", "/api/multiroom/follow", false},
		{"GET", "/api/info", false},
	} {
		if got := needsPress(httptest.NewRequestWithContext(context.Background(), c.method, c.target, nil)); got != c.want {
			t.Errorf("%s %s: needsPress = %v, want %v", c.method, c.target, got, c.want)
		}
	}
}

func TestOnDeviceRefusesWithoutPress(t *testing.T) {
	s := &Server{}
	var gotToken string
	s.ReconfigureForTest(func(o *Options) {
		o.Panel.Confirm = func(_ context.Context, _, token string) error {
			gotToken = token
			return ErrNotConfirmed
		}
	})
	ran := false
	h := s.onDevice(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))
	r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/reboot", nil)
	r.Header.Set(DeviceTokenHeader, "abc")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if ran || w.Code != http.StatusForbidden {
		t.Fatalf("ran=%v code=%d, want a 403 and no handler", ran, w.Code)
	}
	if gotToken != "abc" {
		t.Fatalf("token = %q, want the header's", gotToken)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "POST", "/api/volume", nil))
	if !ran {
		t.Fatal("an ordinary write waited for a press")
	}
}

// The UI says why a request held for a press failed in its own language, so each way it
// fails carries its own code.
func TestPressRefusalsCarryTheirCode(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrNotConfirmed, http.StatusForbidden, "not_confirmed"},
		{ErrConfirmSuperseded, http.StatusConflict, "confirm_superseded"},
		{context.Canceled, http.StatusRequestTimeout, "timeout"},
	} {
		s := &Server{}
		s.ReconfigureForTest(func(o *Options) { o.Panel.Confirm = func(context.Context, string, string) error { return c.err } })
		w := httptest.NewRecorder()
		s.onDevice(http.NotFoundHandler()).ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "POST", "/api/reboot", nil))
		var body struct{ Error, Code string }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != c.status || body.Code != c.code || body.Error == "" {
			t.Errorf("%v: %d %+v, want %d with code %s and a message", c.err, w.Code, body, c.status, c.code)
		}
	}
}

// The Update button (latest stable) runs without a press; a picked release waits for one.
func TestUpdateTagNeedsPress(t *testing.T) {
	s := &Server{}
	asked := 0
	s.ReconfigureForTest(func(o *Options) {
		o.Panel.Confirm = func(context.Context, string, string) error { asked++; return ErrNotConfirmed }
	})
	w := httptest.NewRecorder()
	s.updateApp(w, httptest.NewRequestWithContext(context.Background(), "POST", "/api/update", strings.NewReader(`{"confirm":true,"tag":"v0.0.1"}`)))
	if asked != 1 || w.Code != http.StatusForbidden {
		t.Fatalf("picked tag: asked=%d code=%d, want one press asked and 403", asked, w.Code)
	}
}
