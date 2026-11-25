// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// ConfirmWindow is how long a dangerous request waits for a key held on the speaker.
const ConfirmWindow = 60 * time.Second

// DeviceTokenHeader carries the one-time token from /run/soundtap/confirm-token. Whoever
// can read that file has root on the speaker already, so it stands in for the hold on the
// speaker: the bench scripts read it over ssh.
const DeviceTokenHeader = "X-SoundTap-Device-Token" //nolint:gosec // a header name, not a credential

// ErrNotConfirmed is what a DeviceConfirm returns when nobody held a key in time.
var ErrNotConfirmed = errors.New("not confirmed on the speaker")

// ErrConfirmSuperseded is what a waiting request gets when a newer one takes its place:
// one request waits for the hold at a time, and the newest wins.
var ErrConfirmSuperseded = errors.New("a newer request waits for the button instead")

// DeviceConfirm blocks until a key on the speaker is held, the token matches, a newer
// request replaces this one, the window closes or ctx ends. what is the request's method
// and path: the log and the ST20 panel name the action by it.
type DeviceConfirm func(ctx context.Context, what, token string) error

// needsPress reports whether a request changes what runs on the speaker, wipes it, takes
// it off the network or reads its memory: those need a key held on the speaker itself.
// Everything else, the peer routes among them, stays open on the home network. Every install
// upload needs the press, signed or not (ADR 0048): a release's .sig can be sent with any
// body, and the install writes the fallback slot before it can compare the digest.
func needsPress(r *http.Request) bool {
	path := r.URL.Path
	switch r.Method {
	case http.MethodGet, http.MethodHead: // the mux serves HEAD on a GET pattern
		return path == "/api/debug"
	case http.MethodPost:
		switch path {
		// POST /api/update and POST /api/core-update are not here: the speaker fetches the
		// release itself and installs it only when its .sig verifies, an older one included
		// (ADR 0048).
		// Clearing Bluetooth pairings, or forgetting one, is here too: anyone on the LAN
		// could otherwise unpair the owner's phone again and again, and pairing again needs
		// the speaker.
		case "/api/factory-reset", "/api/restore", "/api/reboot", "/api/wifi", "/api/setup",
			"/api/bluetooth/clear", bluetoothForgetPath, soundtapdUpdatePath:
			return true
		case firmwareUploadPath:
			// Without ?install=1 it only says what a package is.
			return r.URL.Query().Get("install") == "1"
		}
	case http.MethodDelete:
		return path == soundtapdUpdatePath
	default:
		// Anything else needs nothing here.
	}

	return false
}

// pressesInHandler reports whether a press route asks for the press from its own handler,
// once it has checked what it was asked: forgetting one Bluetooth device checks that the
// address is paired first, so a stale list does not send the owner to the speaker for a 404.
func pressesInHandler(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == bluetoothForgetPath
}

// bluetoothForgetPath forgets one paired Bluetooth device.
const bluetoothForgetPath = "/api/bluetooth/forget"

// onDevice holds a dangerous request until someone holds a key on the speaker. An install
// upload waits for it whether or not it carries a .sig (ADR 0048). A route that asks for
// the press itself (pressesInHandler) passes through.
func (s *Server) onDevice(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !needsPress(r) || pressesInHandler(r) || s.pressed(w, r) {
			next.ServeHTTP(w, r)
		}
	})
}

// pressed waits for the hold on the speaker; on false it has written the error.
func (s *Server) pressed(w http.ResponseWriter, r *http.Request) bool {
	if s.deviceConfirm == nil {
		return true
	}

	switch err := s.deviceConfirm(r.Context(), r.Method+" "+r.URL.Path, r.Header.Get(DeviceTokenHeader)); {
	case err == nil:
		return true
	case errors.Is(err, ErrConfirmSuperseded):
		apiErrorCode(w, http.StatusConflict, apierror.ConfirmSuperseded, err.Error())
	case errors.Is(err, ErrNotConfirmed):
		apiErrorCode(w, http.StatusForbidden, apierror.NotConfirmed, "hold a button on the speaker to confirm")
	default:
		// The caller went away while the speaker waited.
		apiError(w, http.StatusRequestTimeout, err.Error())
	}

	return false
}
