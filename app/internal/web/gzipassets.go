// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// gzipAssets serves the bundle's text assets gzipped to clients that accept it. The ST20's
// own Wi-Fi moves a few tens of KB/s, so the 500 KB script took the UI tens of seconds to
// load; gzipped it is about a third. The embedded files never change, so each is compressed
// once, on first request, and kept.
type gzipAssets struct {
	fs   fs.FS
	next http.Handler

	mu    sync.Mutex
	cache map[string][]byte
}

var gzipTypes = map[string]bool{".js": true, ".css": true, ".svg": true, ".json": true, ".webmanifest": true, ".html": true}

func (g *gzipAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if !strings.HasPrefix(name, "assets/") || !gzipTypes[path.Ext(name)] {
		g.next.ServeHTTP(w, r)
		return
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.Header.Get("Range") != "" ||
		!acceptsGzip(r.Header.Get("Accept-Encoding")) {
		g.next.ServeHTTP(w, r)
		return
	}
	body, ok := g.compressed(name)
	if !ok {
		g.next.ServeHTTP(w, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func (g *gzipAssets) compressed(name string) ([]byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if b, ok := g.cache[name]; ok {
		return b, true
	}
	raw, err := fs.ReadFile(g.fs, name)
	if err != nil {
		return nil, false
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil || zw.Close() != nil {
		return nil, false
	}
	if g.cache == nil {
		g.cache = map[string][]byte{}
	}
	g.cache[name] = buf.Bytes()
	return buf.Bytes(), true
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip (a "gzip;q=0" refuses it).
func acceptsGzip(h string) bool {
	for _, part := range strings.Split(h, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(token), "gzip") && strings.TrimSpace(token) != "*" {
			continue
		}
		q := strings.TrimSpace(params)
		if v, ok := strings.CutPrefix(q, "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f == 0 {
				return false
			}
		}
		return true
	}
	return false
}
