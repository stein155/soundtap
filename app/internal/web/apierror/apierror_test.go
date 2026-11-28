// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package apierror_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// A body that ran past http.MaxBytesReader's limit is 413 too_large, wrapped or not;
// anything else, nil included, is 400 invalid_request with the caller's message.
func TestRefuseBody(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	_, tooLong := io.ReadAll(http.MaxBytesReader(rec, io.NopCloser(strings.NewReader("12345")), 2))

	for _, testCase := range []struct {
		err    error
		status int
		code   apierror.Code
	}{
		{tooLong, http.StatusRequestEntityTooLarge, apierror.TooLarge},
		{fmt.Errorf("decode: %w", tooLong), http.StatusRequestEntityTooLarge, apierror.TooLarge},
		{io.ErrUnexpectedEOF, http.StatusBadRequest, apierror.InvalidRequest},
		{nil, http.StatusBadRequest, apierror.InvalidRequest},
	} {
		rec := httptest.NewRecorder()
		apierror.RefuseBody(rec, "volume required", testCase.err)

		var got apierror.Body

		err := json.Unmarshal(rec.Body.Bytes(), &got)
		if err != nil || rec.Code != testCase.status || got.Code != testCase.code {
			t.Errorf("%v: %d %s, want %d %s", testCase.err, rec.Code, rec.Body, testCase.status, testCase.code)
		}

		if testCase.status == http.StatusBadRequest && got.Error != "volume required" {
			t.Errorf("%v: message %q", testCase.err, got.Error)
		}
	}
}

func TestWriteSendsMessageAndCode(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	apierror.Write(rec, http.StatusConflict, apierror.Busy, "another firmware operation is in progress")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q", ct)
	}

	var got map[string]string

	err := json.Unmarshal(rec.Body.Bytes(), &got)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"error": "another firmware operation is in progress", "code": "busy"}
	if len(got) != len(want) || got["error"] != want["error"] || got["code"] != want["code"] {
		t.Fatalf("body %v, want %v", got, want)
	}
}

// A call site that names no code still gives the client one it can act on.
func TestForStatus(t *testing.T) {
	t.Parallel()

	for status, want := range map[int]apierror.Code{
		http.StatusBadRequest:            apierror.InvalidRequest,
		http.StatusUnprocessableEntity:   apierror.InvalidRequest,
		http.StatusForbidden:             apierror.Forbidden,
		http.StatusNotFound:              apierror.NotFound,
		http.StatusMethodNotAllowed:      apierror.MethodNotAllowed,
		http.StatusRequestTimeout:        apierror.Timeout,
		http.StatusConflict:              apierror.Conflict,
		http.StatusPreconditionFailed:    apierror.PreconditionFailed,
		http.StatusRequestEntityTooLarge: apierror.TooLarge,
		http.StatusUnsupportedMediaType:  apierror.PackageRejected,
		http.StatusPreconditionRequired:  apierror.ConfirmRequired,
		http.StatusInternalServerError:   apierror.Internal,
		http.StatusNotImplemented:        apierror.Unsupported,
		http.StatusBadGateway:            apierror.UpstreamFailed,
		http.StatusServiceUnavailable:    apierror.Unavailable,
		http.StatusGatewayTimeout:        apierror.Timeout,
		http.StatusInsufficientStorage:   apierror.StorageFull,
	} {
		if got := apierror.ForStatus(status); got != want {
			t.Errorf("ForStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

// Every code a status maps to is one the API documents.
func TestForStatusOnlyGivesListedCodes(t *testing.T) {
	t.Parallel()

	for status := 400; status < 600; status++ {
		if c := apierror.ForStatus(status); !slices.Contains(apierror.Codes(), c) {
			t.Errorf("ForStatus(%d) = %q, not in Codes", status, c)
		}
	}
}
