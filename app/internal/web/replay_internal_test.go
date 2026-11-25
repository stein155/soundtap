// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplayActsOnceAndAnswersTheRepeat(t *testing.T) {
	t.Parallel()

	var ran atomic.Int32

	release := make(chan struct{})

	var c replayCache

	handler := c.replay(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ran.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	send := func(method, path, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody)
		if id != "" {
			r.Header.Set(RequestHeader, id)
		}

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		return w
	}

	// The repeat arrives while the first is still running: it waits, then gets its answer.
	var wg sync.WaitGroup

	got := make([]*httptest.ResponseRecorder, 2)
	for i := range got {
		wg.Go(func() { got[i] = send("POST", "/api/play/1", "a1") })
	}

	for ran.Load() == 0 {
		runtime.Gosched() // until the first one is inside the handler
	}

	close(release)
	wg.Wait()

	if n := ran.Load(); n != 1 {
		t.Fatalf("handler ran %d times for one action", n)
	}

	for _, w := range got {
		if w.Code != http.StatusAccepted || w.Body.String() != `{"ok":true}` ||
			w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("answer %d %q %v", w.Code, w.Body, w.Header())
		}
	}

	// Another id, another path, or no id at all is a new action.
	send("POST", "/api/play/1", "a2")
	send("POST", "/api/play/2", "a1")
	send("POST", "/api/play/1", "")

	if n := ran.Load(); n != 4 {
		t.Fatalf("handler ran %d times, want 4", n)
	}
}

// A 401 is not replayed: the same action sent again after pairing must reach the handler.
func TestReplayDoesNotKeepUnauthorized(t *testing.T) {
	t.Parallel()

	var c replayCache

	calls := 0
	handler := c.replay(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	codes := make([]int, 0, 2)

	for range 2 {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/volume", http.NoBody)
		r.Header.Set(RequestHeader, "action-1")

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		codes = append(codes, w.Code)
	}

	if codes[0] != http.StatusUnauthorized || codes[1] != http.StatusNoContent {
		t.Fatalf("codes = %v, want [401 204]", codes)
	}
}

// A failed forward through the device proxy did nothing here; the retry with the same id
// must reach the peer, whose own replay stops a duplicate. A local 502 is kept.
func TestReplayForwardsAgainAfterAProxyFailure(t *testing.T) {
	t.Parallel()

	for path, want := range map[string][]int{
		"/api/devices/SN1/api/volume": {http.StatusBadGateway, http.StatusNoContent},
		"/api/volume":                 {http.StatusBadGateway, http.StatusBadGateway},
	} {
		var c replayCache

		calls := 0
		handler := c.replay(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusBadGateway)

				return
			}

			w.WriteHeader(http.StatusNoContent)
		}))

		codes := make([]int, 0, 2)

		for range 2 {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
			r.Header.Set(RequestHeader, "action-1")

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			codes = append(codes, w.Code)
		}

		if codes[0] != want[0] || codes[1] != want[1] {
			t.Fatalf("%s: codes = %v, want %v", path, codes, want)
		}
	}
}

// A handler that panics did nothing and answered nothing: the repeat of that request must
// run the handler again, not be told 200 with an empty body.
func TestReplayDoesNotRecordAPanickedHandler(t *testing.T) {
	t.Parallel()

	var (
		ran atomic.Int32
		c   replayCache
	)

	handler := c.replay(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ran.Add(1) == 1 {
			panic("boom")
		}

		w.WriteHeader(http.StatusAccepted)
	}))
	send := func() *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/volume", http.NoBody)
		r.Header.Set(RequestHeader, "p1")

		rec := httptest.NewRecorder()

		func() {
			defer func() { _ = recover() }() // net/http recovers per connection

			handler.ServeHTTP(rec, r)
		}()

		return rec
	}
	send()

	if w := send(); ran.Load() != 2 || w.Code != http.StatusAccepted {
		t.Fatalf("repeat after a panic: handler ran %d times, answer %d; want it run again and 202", ran.Load(), w.Code)
	}
}

// Unique request ids cannot grow the cache past its bound; the newest answer is kept.
func TestReplayCacheStaysBounded(t *testing.T) {
	t.Parallel()

	var cache replayCache

	h := cache.replay(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))

	for i := range 3 * replayMaxEntries {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/volume", nil)
		r.Header.Set(RequestHeader, strconv.Itoa(i))
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	if n := len(cache.m); n > replayMaxEntries {
		t.Fatalf("cache holds %d entries, want at most %d", n, replayMaxEntries)
	}

	if _, ok := cache.m["POST /api/volume "+strconv.Itoa(3*replayMaxEntries-1)]; !ok {
		t.Fatal("the newest answer was evicted")
	}
}

// A write waiting a whole minute for its key press must not run again for a repeat.
func TestReplayKeepsARunningWriteOverItsLifetime(t *testing.T) {
	t.Parallel()

	running := &replayed{
		done: make(chan struct{}), at: time.Now().Add(-time.Hour), status: 0, header: nil, body: nil, whole: false,
	}
	if running.expired(time.Now()) {
		t.Fatal("a running write expired")
	}

	close(running.done)

	if !running.expired(time.Now()) {
		t.Fatal("an old answer did not expire")
	}

	running.at = time.Now()
	if running.expired(time.Now()) {
		t.Fatal("a fresh answer expired")
	}
}
