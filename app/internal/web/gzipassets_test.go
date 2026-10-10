// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestGzipAssets(t *testing.T) {
	js := bytes.Repeat([]byte("console.log('soundtap');\n"), 400)
	files := fstest.MapFS{"assets/app.js": {Data: js}, "assets/logo.png": {Data: []byte("png")}, "index.html": {Data: []byte("<html>")}}
	g := &gzipAssets{fs: files, next: http.FileServer(http.FS(files))}
	get := func(p, enc string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		if enc != "" {
			r.Header.Set("Accept-Encoding", enc)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}

	w := get("/assets/app.js", "gzip, br")
	if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers %v", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); !bytes.Equal(got, js) || w.Body.Len() >= len(js) {
		t.Fatalf("round trip %d bytes, compressed %d", len(got), w.Body.Len())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	// Not accepted, refused with q=0, not text, outside /assets: the plain file.
	for _, c := range [][2]string{{"/assets/app.js", ""}, {"/assets/app.js", "gzip;q=0"}, {"/assets/logo.png", "gzip"}, {"/", "gzip"}} {
		if w := get(c[0], c[1]); w.Header().Get("Content-Encoding") != "" || w.Code != http.StatusOK {
			t.Fatalf("%v: encoding %q code %d", c, w.Header().Get("Content-Encoding"), w.Code)
		}
	}
}
