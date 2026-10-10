// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"net/http"
	"strings"
)

// jsonRouterErrors gives the errors the router itself writes under /api/ -- 404 for
// an unknown path, 405 for a method a route does not take -- the API's one error
// shape (ADR 0091). ServeMux and the UI's file server write those as text/plain,
// which the UI cannot read; handlers already answer with apiError.
func jsonRouterErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&plainErrorWriter{ResponseWriter: w}, r)
	})
}

// plainErrorWriter replaces a text/plain error response with the JSON one.
type plainErrorWriter struct {
	http.ResponseWriter
	replaced bool // the plain-text body that follows is dropped
}

func (p *plainErrorWriter) WriteHeader(code int) {
	if code < 400 || !strings.HasPrefix(p.Header().Get("Content-Type"), "text/plain") {
		p.ResponseWriter.WriteHeader(code)
		return
	}
	p.replaced = true
	apiError(p.ResponseWriter, code, strings.ToLower(http.StatusText(code)))
}

func (p *plainErrorWriter) Write(b []byte) (int, error) {
	if p.replaced {
		return len(b), nil
	}
	return p.ResponseWriter.Write(b)
}

// Flush keeps the SSE streams under /api/ working through the wrapper.
func (p *plainErrorWriter) Flush() { _ = p.FlushError() }

// FlushError hands a failed flush back to http.ResponseController, so an event stream
// learns that its client is gone.
func (p *plainErrorWriter) FlushError() error {
	return http.NewResponseController(p.ResponseWriter).Flush()
}

func (p *plainErrorWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }
