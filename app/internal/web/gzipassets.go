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
	"slices"
	"strconv"
	"strings"
	"sync"
)

// gzipAssets serves the bundle's text assets gzipped to clients that accept it. The ST20's
// own Wi-Fi moves a few tens of KB/s, so the 500 KB script took the UI tens of seconds to
// load; gzipped it is about a third. The embedded files never change, so each is compressed
// once and kept: all of them in the background when the server starts (warm), or on the
// first request that comes sooner. One file's compression holds only the requests for that
// file: under one lock for the lot, a cached script waited behind a second or more of
// compressing another file on the speaker's single core.
type gzipAssets struct {
	fs   fs.FS
	next http.Handler

	mu    sync.Mutex // guards cache, not the compression
	cache map[string]*gzipped
}

// gzipped is one asset's compressed body, made once.
type gzipped struct {
	once sync.Once
	body []byte
	ok   bool
}

// newGzipAssets serves fsys's assets, gzipped where they can be, and next for the rest.
func newGzipAssets(fsys fs.FS, next http.Handler) *gzipAssets {
	return &gzipAssets{fs: fsys, next: next, mu: sync.Mutex{}, cache: map[string]*gzipped{}}
}

// gzipTypes are the asset extensions served gzipped.
func gzipTypes() []string { return []string{".js", ".css", ".svg", ".json", ".webmanifest", ".html"} }

func (g *gzipAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if !strings.HasPrefix(name, "assets/") || !slices.Contains(gzipTypes(), path.Ext(name)) {
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

// warm compresses every asset ahead of the first request for it.
func (g *gzipAssets) warm() {
	_ = fs.WalkDir(g.fs, "assets", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && slices.Contains(gzipTypes(), path.Ext(name)) {
			g.compressed(name)
		}

		return nil
	})
}

// compressed is name gzipped, compressed on its first call; false for a file that is not
// there or did not compress.
func (g *gzipAssets) compressed(name string) ([]byte, bool) {
	g.mu.Lock()
	entry, known := g.cache[name]
	g.mu.Unlock()

	if !known {
		// Only a file that exists gets an entry: a request for any name must not grow the map.
		fi, err := fs.Stat(g.fs, name)
		if err != nil || fi.IsDir() {
			return nil, false
		}

		g.mu.Lock()
		if g.cache == nil {
			g.cache = map[string]*gzipped{}
		}

		entry, known = g.cache[name]
		if !known {
			entry = &gzipped{once: sync.Once{}, body: nil, ok: false}
			g.cache[name] = entry
		}
		g.mu.Unlock()
	}

	entry.once.Do(func() { entry.body, entry.ok = gzipFile(g.fs, name) })

	return entry.body, entry.ok
}

// gzipFile is name read from fsys and gzipped at the best compression.
func gzipFile(fsys fs.FS, name string) ([]byte, bool) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, false
	}

	var buf bytes.Buffer

	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)

	_, err = zw.Write(raw)
	if err != nil || zw.Close() != nil {
		return nil, false
	}

	return buf.Bytes(), true
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip (a "gzip;q=0" refuses it).
func acceptsGzip(h string) bool {
	for part := range strings.SplitSeq(h, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(token), "gzip") && strings.TrimSpace(token) != "*" {
			continue
		}

		q := strings.TrimSpace(params)
		if v, ok := strings.CutPrefix(q, "q="); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err == nil && f == 0 {
				return false
			}
		}

		return true
	}

	return false
}
