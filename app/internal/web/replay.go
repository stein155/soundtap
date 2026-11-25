// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RequestHeader names one user action. The UI sends a write again under the same id when
// the first try died on the network: iOS reuses kept-alive connections the server
// may already have closed, and Safari does not retry a POST itself. The speaker may have
// acted on the first try, so the second gets the first one's answer instead of acting
// twice.
const RequestHeader = "X-SoundTap-Request"

// At most replayMaxEntries answers of replayMaxBody each: 1 MiB, whatever a caller sends
// . A write's answer is small; a longer one is not kept, and its repeat gets 409.
const (
	replayFor        = time.Minute
	replayMaxBody    = 8 << 10
	replayMaxEntries = 128
)

type replayed struct {
	done   chan struct{}
	at     time.Time
	status int
	header http.Header
	body   []byte
	whole  bool
}

// expired reports an answer kept long enough. A write still running never expires: a
// repeat has to wait for it, not start it a second time, however long a key press takes
// . Callers hold replayCache.mu, which guards at.
func (e *replayed) expired(now time.Time) bool {
	select {
	case <-e.done:
		return now.Sub(e.at) > replayFor
	default:
		return false
	}
}

type replayCache struct {
	mu sync.Mutex
	m  map[string]*replayed
}

// replay answers a repeated write from the first one's recorded response. The key holds
// method and path too: a proxied write reaches this speaker's own routes a second time
// under another path, and must not wait on itself.
// claim finds the entry for key or makes one. It reports whether the entry was seen
// before, and false for tracked when the cache is full of writes still running.
func (c *replayCache) claim(key string, now time.Time) (*replayed, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.m == nil {
		c.m = map[string]*replayed{}
	}

	for k, e := range c.m {
		if e.expired(now) {
			delete(c.m, k)
		}
	}

	entry, seen := c.m[key]
	if seen {
		return entry, true, true
	}

	if !c.makeRoom() {
		return nil, false, false
	}

	entry = &replayed{done: make(chan struct{}), at: now, status: 0, header: nil, body: nil, whole: false}
	c.m[key] = entry

	return entry, false, true
}

// serve answers a repeat with the first answer, once that is done.
func (e *replayed) serve(w http.ResponseWriter, r *http.Request) {
	select {
	case <-e.done:
	case <-r.Context().Done():
		return
	}

	if !e.whole {
		apiError(w, http.StatusConflict, "request already handled")

		return
	}

	maps.Copy(w.Header(), e.header)

	w.WriteHeader(e.status)
	_, _ = w.Write(e.body)
}

func (c *replayCache) replay(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(RequestHeader)
		if requestID == "" || r.Method == http.MethodGet || r.Method == http.MethodHead || len(requestID) > 64 {
			next.ServeHTTP(w, r)

			return
		}

		key := r.Method + " " + r.URL.Path + " " + requestID
		now := time.Now()

		entry, seen, tracked := c.claim(key, now)
		if !tracked {
			// Full of writes still running: serve this one without a replay entry.
			next.ServeHTTP(w, r)

			return
		}

		if seen {
			entry.serve(w, r)

			return
		}

		rec := &recorder{ResponseWriter: w, status: http.StatusOK, buf: bytes.Buffer{}, over: false}
		returned := false

		defer func() {
			entry.status, entry.header, entry.body = rec.status, w.Header().Clone(), rec.buf.Bytes()

			entry.whole = !rec.over && returned
			if rec.status == http.StatusUnauthorized || !returned || proxyFailed(r, rec.status) {
				// A handler that panicked answered nothing; a repeat must run it again.
				// A refusal did nothing to repeat, and pairing may have fixed it: the
				// retry must reach the handler again, not get the old 401.
				c.mu.Lock()
				delete(c.m, key)
				c.mu.Unlock()
			}

			c.mu.Lock()
			entry.at = time.Now() // kept for a minute from its answer
			c.mu.Unlock()
			close(entry.done)
		}()

		next.ServeHTTP(rec, r)

		returned = true
	})
}

// makeRoom drops the oldest finished entry while the cache is full; c.mu is held. It
// reports whether there is room. Running entries stay: their repeats wait on them.
func (c *replayCache) makeRoom() bool {
	for len(c.m) >= replayMaxEntries {
		oldest := ""

		for k, e := range c.m {
			select {
			case <-e.done:
				if oldest == "" || e.at.Before(c.m[oldest].at) {
					oldest = k
				}
			default:
			}
		}

		if oldest == "" {
			return false
		}

		delete(c.m, oldest)
	}

	return true
}

// proxyFailed: the device proxy could not finish a forward (the peer was unreachable or the
// browser went away mid-way). Nothing happened here, and the peer keeps its own replay, so
// the same request id must be forwarded again rather than answered with the old 502.
func proxyFailed(r *http.Request, status int) bool {
	return status == http.StatusBadGateway && strings.HasPrefix(r.URL.Path, "/api/devices/")
}

// recorder passes a response through and keeps a copy of it, up to replayMaxBody.
type recorder struct {
	http.ResponseWriter

	status int
	buf    bytes.Buffer
	over   bool
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(data []byte) (int, error) {
	if !r.over {
		if r.buf.Len()+len(data) > replayMaxBody {
			r.over, r.buf = true, bytes.Buffer{}
		} else {
			r.buf.Write(data)
		}
	}

	return r.ResponseWriter.Write(data) //nolint:wrapcheck // a ResponseWriter wrapper is transparent
}

// Unwrap lets http.ResponseController reach Flush and deadlines on the real writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
