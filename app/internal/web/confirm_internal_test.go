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
	t.Parallel()

	for _, testCase := range []struct {
		method, target string
		want           bool
	}{
		{"POST", "/api/factory-reset", true},
		{"POST", "/api/reboot", true},
		{"POST", "/api/wifi", true},
		{"POST", "/api/bluetooth/clear", true},
		{"POST", "/api/bluetooth/forget", true},
		{"POST", "/api/bluetooth/pair", false},
		{"POST", "/api/restore", true},
		{"POST", "/api/setup", true},
		{"POST", "/api/update", false}, // updateApp presses only for a picked tag
		{"POST", "/api/firmware?install=1", true},
		{"POST", "/api/core-update", false}, // a fetched, signed release: no press (ADR 0048)
		{"POST", "/api/firmware", false},
		{"POST", "/api/soundtapd", true},
		{"DELETE", "/api/soundtapd", true},
		{"GET", "/api/debug", true},
		{"POST", "/api/wifi/scan", false},
		{"POST", "/api/volume", false},
		{"POST", "/api/multiroom/follow", false},
		{"GET", "/api/info", false},
	} {
		got := needsPress(httptest.NewRequestWithContext(context.Background(), testCase.method, testCase.target, nil))
		if got != testCase.want {
			t.Errorf("%s %s: needsPress = %v, want %v", testCase.method, testCase.target, got, testCase.want)
		}
	}
}

func TestOnDeviceRefusesWithoutPress(t *testing.T) {
	t.Parallel()

	srv := new(Server)

	var gotToken string

	srv.ReconfigureForTest(func(o *Options) {
		o.Panel.Confirm = func(_ context.Context, _, token string) error {
			gotToken = token

			return ErrNotConfirmed
		}
	})

	ran := false
	handler := srv.onDevice(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))
	r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/reboot", nil)
	r.Header.Set(DeviceTokenHeader, "abc")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if ran || rec.Code != http.StatusForbidden {
		t.Fatalf("ran=%v code=%d, want a 403 and no handler", ran, rec.Code)
	}

	if gotToken != "abc" {
		t.Fatalf("token = %q, want the header's", gotToken)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "POST", "/api/volume", nil))

	if !ran {
		t.Fatal("an ordinary write waited for a press")
	}
}

// The UI says why a request held for a press failed in its own language, so each way it
// fails carries its own code.
func TestPressRefusalsCarryTheirCode(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrNotConfirmed, http.StatusForbidden, "not_confirmed"},
		{ErrConfirmSuperseded, http.StatusConflict, "confirm_superseded"},
		{context.Canceled, http.StatusRequestTimeout, "timeout"},
	} {
		srv := new(Server)
		srv.ReconfigureForTest(func(o *Options) {
			o.Panel.Confirm = func(context.Context, string, string) error { return testCase.err }
		})

		rec := httptest.NewRecorder()
		srv.onDevice(http.NotFoundHandler()).ServeHTTP(
			rec,
			httptest.NewRequestWithContext(context.Background(), "POST", "/api/reboot", nil),
		)

		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}

		err := json.Unmarshal(rec.Body.Bytes(), &body)
		if err != nil {
			t.Fatal(err)
		}

		if rec.Code != testCase.status || body.Code != testCase.code || body.Error == "" {
			t.Errorf(
				"%v: %d %+v, want %d with code %s and a message",
				testCase.err,
				rec.Code,
				body,
				testCase.status,
				testCase.code,
			)
		}
	}
}

// ADR 0048: a release is signed, so neither the Update button nor a picked release, an
// older one included, waits for a press.
func TestUpdateTagNeedsNoPress(t *testing.T) {
	t.Parallel()

	srv := new(Server)
	asked := 0

	srv.ReconfigureForTest(func(o *Options) {
		o.Panel.Confirm = func(context.Context, string, string) error {
			asked++

			return ErrNotConfirmed
		}
	})

	rec := httptest.NewRecorder()
	srv.updateApp(
		rec,
		httptest.NewRequestWithContext(
			context.Background(),
			"POST",
			"/api/update",
			strings.NewReader(`{"confirm":true,"tag":"v0.0.1"}`),
		),
	)

	if asked != 0 || rec.Code == http.StatusForbidden {
		t.Fatalf("picked tag: asked=%d code=%d, want no press asked", asked, rec.Code)
	}
}

// ADR 0048: every install upload waits for the press, one with a .sig included: a release's
// .sig can be replayed with another body, and the install writes the fallback slot before it
// compares the digest.
func TestASignedUploadWaitsForThePress(t *testing.T) {
	t.Parallel()

	srv := new(Server)
	asked := 0

	srv.ReconfigureForTest(func(o *Options) {
		o.Panel.Confirm = func(context.Context, string, string) error {
			asked++

			return ErrNotConfirmed
		}
	})

	for _, testCase := range []struct{ method, target, sig string }{
		{"POST", "/api/firmware?install=1", "c2lnbmVk"},
		{"POST", "/api/soundtapd", "c2lnbmVk"},
		{"POST", "/api/firmware?install=1", ""},
		{"POST", "/api/soundtapd", ""},
		{"DELETE", "/api/soundtapd", "c2lnbmVk"},
	} {
		asked = 0
		ran := false
		handler := srv.onDevice(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))

		r := httptest.NewRequestWithContext(context.Background(), testCase.method, testCase.target, nil)
		if testCase.sig != "" {
			r.Header.Set("X-SoundTap-Signature", testCase.sig)
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)

		if asked != 1 || ran || rec.Code != http.StatusForbidden {
			t.Errorf(
				"%s %s sig %q: asked %d, ran %v, code %d; want the press asked and a 403",
				testCase.method,
				testCase.target,
				testCase.sig,
				asked,
				ran,
				rec.Code,
			)
		}
	}
}
