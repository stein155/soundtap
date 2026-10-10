// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"syscall"

	"github.com/stein155/soundtap/app/internal/web/apierror"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) //nolint:errchkjson // the client may be gone; nothing left to tell it
}

// writeOK answers a request that needs nothing back but its success.
func writeOK(w http.ResponseWriter) { writeJSON(w, 200, map[string]string{"status": "ok"}) }

// apiError is the API's one error shape (ADR 0091): {"error": "<message>", "code": "<code>"}
// as JSON, with a status that says whose fault it was -- 4xx the caller's, 5xx the
// speaker's -- and the code its status implies.
func apiError(w http.ResponseWriter, status int, msg string) {
	apierror.Write(w, status, apierror.ForStatus(status), msg)
}

// apiErrorCode is apiError with a code more precise than the status implies.
func apiErrorCode(w http.ResponseWriter, status int, code apierror.Code, msg string) {
	apierror.Write(w, status, code, msg)
}

// fail reports a failure on the speaker's side. The backend runs in this process, so
// that is a 500, not the 502 it used to be; a deadline is a 504. A peer or
// upstream that failed says 502 itself at the call site.
func (s *Server) fail(w http.ResponseWriter, msg string, err error) {
	code := http.StatusInternalServerError
	if err != nil {
		s.log.Warn(msg, "err", err.Error())
		if errors.Is(err, context.DeadlineExceeded) {
			code = http.StatusGatewayTimeout
		}
		// The one cause the caller can act on: free space, or the next try fails the same.
		if errors.Is(err, syscall.ENOSPC) {
			code = http.StatusInsufficientStorage
			msg += ": the speaker's storage is full"
		}
	}
	apiError(w, code, msg)
}

// badRequest reports a client error (malformed body, missing field) as a 400,
// so callers can tell their own mistakes apart from speaker/upstream failures.
func (s *Server) badRequest(w http.ResponseWriter, msg string, err error) {
	if err != nil {
		s.log.Warn(msg, "err", err.Error())
	}
	apiError(w, http.StatusBadRequest, msg)
}

// routePath is the route a request is for: its own path, or for /api/devices/{id}/<rest>
// the /<rest> it is proxied to.
func routePath(p string) string {
	rest, ok := strings.CutPrefix(p, "/api/devices/")
	if !ok {
		return p
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[i:]
	}
	return p
}
