// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type svgStub struct{}

func (svgStub) RoundTrip(r *http.Request) (*http.Response, error) {
	h := http.Header{"Content-Type": {"image/svg+xml"}}
	body := `<svg xmlns="http://www.w3.org/2000/svg"><script>fetch("/api/power")</script></svg>`

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

type typeStub string

func (s typeStub) RoundTrip(r *http.Request) (*http.Response, error) {
	header := http.Header{}
	if s != "" {
		header.Set("Content-Type", string(s))
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("body")),
		Request:    r,
	}, nil
}

// The logo proxy relays images only, so it is no open relay for any public page.
func TestLogoRelayPassesOnlyImages(t *testing.T) {
	t.Parallel()

	for contentType, want := range map[string]int{
		"image/jpeg": http.StatusOK, "image/png": http.StatusOK,
		"text/html": http.StatusBadGateway, "application/json": http.StatusBadGateway, "": http.StatusBadGateway,
	} {
		s := new(Server)
		s.proxy = &http.Client{Transport: typeStub(contentType)}
		rec := httptest.NewRecorder()
		s.relay(
			rec,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/logo", nil),
			"https://cdn.example/x",
			"image/png",
			logoMaxBytes,
			"",
		)

		if rec.Code != want {
			t.Errorf("upstream %q: status %d, want %d", contentType, rec.Code, want)
		}
	}
}

// A proxied SVG keeps its type (logos can be SVG) but must not run on the speaker's
// origin when someone opens the proxy URL directly.
func TestRelaySandboxesProxiedAnswer(t *testing.T) {
	t.Parallel()

	s := new(Server)
	s.proxy = &http.Client{Transport: svgStub{}}
	rec := httptest.NewRecorder()
	s.relay(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/logo", nil),
		"https://cdn.example/x.svg",
		"image/png",
		logoMaxBytes,
		"",
	)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("Content-Security-Policy = %q, want a sandbox with default-src 'none'", csp)
	}
}
