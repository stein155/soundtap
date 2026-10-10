// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package apierror

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestWriteSendsMessageAndCode(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, http.StatusConflict, Busy, "another firmware operation is in progress")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"error": "another firmware operation is in progress", "code": "busy"}
	if len(got) != len(want) || got["error"] != want["error"] || got["code"] != want["code"] {
		t.Fatalf("body %v, want %v", got, want)
	}
}

// A call site that names no code still gives the client one it can act on.
func TestForStatus(t *testing.T) {
	for status, want := range map[int]Code{
		http.StatusBadRequest:            InvalidRequest,
		http.StatusUnprocessableEntity:   InvalidRequest,
		http.StatusForbidden:             Forbidden,
		http.StatusNotFound:              NotFound,
		http.StatusMethodNotAllowed:      MethodNotAllowed,
		http.StatusRequestTimeout:        Timeout,
		http.StatusConflict:              Conflict,
		http.StatusPreconditionFailed:    PreconditionFailed,
		http.StatusRequestEntityTooLarge: TooLarge,
		http.StatusUnsupportedMediaType:  PackageRejected,
		http.StatusPreconditionRequired:  ConfirmRequired,
		http.StatusInternalServerError:   Internal,
		http.StatusNotImplemented:        Unsupported,
		http.StatusBadGateway:            UpstreamFailed,
		http.StatusServiceUnavailable:    Unavailable,
		http.StatusGatewayTimeout:        Timeout,
		http.StatusInsufficientStorage:   StorageFull,
	} {
		if got := ForStatus(status); got != want {
			t.Errorf("ForStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

// Every code a status maps to is one the API documents.
func TestForStatusOnlyGivesListedCodes(t *testing.T) {
	for status := 400; status < 600; status++ {
		if c := ForStatus(status); !slices.Contains(Codes, c) {
			t.Errorf("ForStatus(%d) = %q, not in Codes", status, c)
		}
	}
}
