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
// Everything else, the peer routes among them, stays open on the home network.
func needsPress(r *http.Request) bool {
	p := r.URL.Path
	switch r.Method {
	case http.MethodGet, http.MethodHead: // the mux serves HEAD on a GET pattern
		return p == "/api/debug"
	case http.MethodPost:
		switch p {
		// POST /api/update is not here: updateApp asks for the press only when a caller
		// picks a release, since the latest stable one is signed and cannot go back.
		// Clearing Bluetooth pairings is here too: anyone on the LAN could otherwise unpair
		// the owner's phone again and again, and pairing again needs the speaker.
		case "/api/factory-reset", "/api/restore", "/api/reboot", "/api/wifi", "/api/setup",
			"/api/bluetooth/clear", soundtapdUpdatePath, coreUpdatePath:
			return true
		case firmwareUploadPath:
			// Without ?install=1 it only says what a package is.
			return r.URL.Query().Get("install") == "1"
		}
	case http.MethodPut:
		// The owner key decides which signed packages this speaker installs (ADR 0048).
		return p == ownerKeyPath
	case http.MethodDelete:
		return p == soundtapdUpdatePath || p == ownerKeyPath
	}
	return false
}

// onDevice holds a dangerous request until someone holds a key on the speaker.
func (s *Server) onDevice(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !needsPress(r) || s.pressed(w, r) {
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
