// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/fstest"
)

func TestGzipAssets(t *testing.T) {
	t.Parallel()

	script := bytes.Repeat([]byte("console.log('soundtap');\n"), 400)
	files := fstest.MapFS{
		"assets/app.js":   {Data: script},
		"assets/logo.png": {Data: []byte("png")},
		"index.html":      {Data: []byte("<html>")},
	}
	assets := &gzipAssets{fs: files, next: http.FileServer(http.FS(files)), mu: sync.Mutex{}, cache: nil}
	get := func(p, enc string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, p, http.NoBody)
		if enc != "" {
			r.Header.Set("Accept-Encoding", enc)
		}

		w := httptest.NewRecorder()
		assets.ServeHTTP(w, r)

		return w
	}

	rec := get("/assets/app.js", "gzip, br")
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers %v", rec.Header())
	}

	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := io.ReadAll(zr); !bytes.Equal(got, script) || rec.Body.Len() >= len(script) {
		t.Fatalf("round trip %d bytes, compressed %d", len(got), rec.Body.Len())
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	// Not accepted, refused with q=0, not text, outside /assets: the plain file.
	for _, testCase := range [][2]string{
		{"/assets/app.js", ""},
		{"/assets/app.js", "gzip;q=0"},
		{"/assets/logo.png", "gzip"},
		{"/", "gzip"},
	} {
		if w := get(testCase[0], testCase[1]); w.Header().Get("Content-Encoding") != "" || w.Code != http.StatusOK {
			t.Fatalf("%v: encoding %q code %d", testCase, w.Header().Get("Content-Encoding"), w.Code)
		}
	}
}
