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

// urlRecorder answers every request with an empty JSON object and remembers the URL it was sent.
type urlRecorder struct{ got []string }

func (u *urlRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	u.got = append(u.got, r.URL.String())

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    r,
	}, nil
}

// The TuneIn proxy forwards the path as the browser escaped it, so an encoded "?" or "#"
// stays part of the path instead of becoming a query or fragment upstream.
func TestTuneInProxyKeepsTheEscapedPath(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"/api/tunein/Browse.ashx%3Frender=json":     tuneInBase + "/Browse.ashx%3Frender=json",
		"/api/tunein/Browse.ashx%23x":               tuneInBase + "/Browse.ashx%23x",
		"/api/tunein/Browse.ashx?c=music&render=js": tuneInBase + "/Browse.ashx?c=music&render=js",
		"/api/tunein/Tune.ashx?id=s24896":           tuneInBase + "/Tune.ashx?id=s24896",
	} {
		upstream := &urlRecorder{got: nil}
		srv := new(Server)
		srv.proxy = &http.Client{Transport: upstream}
		rec := httptest.NewRecorder()
		srv.tuneinProxy(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

		if rec.Code != http.StatusOK || len(upstream.got) != 1 || upstream.got[0] != want {
			t.Errorf("%s: status %d, upstream %q, want 200 and %q", path, rec.Code, upstream.got, want)
		}
	}
}

// A ".." segment, plain or percent-encoded, is refused before anything is fetched.
func TestTuneInProxyRefusesDotDot(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/api/tunein/../x",
		"/api/tunein/a/%2e%2e/x",
		"/api/tunein/%2E%2E",
		"/api/tunein/.%2e/Browse.ashx",
		// An encoded slash hides the ".." inside one segment from a split on "/".
		"/api/tunein/a%2F..%2F..%2Fother",
		"/api/tunein/a%2f%2e%2e",
		"/api/tunein/a%5C..%5Cother",
		"/api/tunein/",
	} {
		upstream := &urlRecorder{got: nil}
		srv := new(Server)
		srv.proxy = &http.Client{Transport: upstream}
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tunein/x", nil)

		// httptest would clean a plain "..": set the path the way a raw request line carries it.
		err := req.URL.UnmarshalBinary([]byte(path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		srv.tuneinProxy(rec, req)

		if rec.Code != http.StatusBadRequest || len(upstream.got) != 0 {
			t.Errorf("%s: status %d, upstream %q, want 400 and no fetch", path, rec.Code, upstream.got)
		}
	}
}
