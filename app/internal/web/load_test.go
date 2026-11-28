// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

// Light parallel load on the whole handler stack over real connections, as the bench run
// that found 10-20 s stalls did it: clients looping GET /api/health and the gzipped
// script. Every request must come back, well within the bound, and none with a 5xx. The
// server has no request bound of its own; one that answers 503 would have to do so at once,
// so a 503 also has to be fast.
func TestParallelClientsGetPromptAnswers(t *testing.T) {
	t.Parallel()

	const (
		clients  = 16
		duration = 2 * time.Second
		// Far above a healthy answer, which takes milliseconds here; generous for -race on a
		// shared runner, and well below the stalls the bench saw.
		bound     = 3 * time.Second
		fast503   = 250 * time.Millisecond
		scriptURL = "/assets/index-0123abcd.js"
	)

	srv, _, _ := newServerSrv(t)
	// A script of a couple of hundred KB, compressible as the real chunks are. It is not
	// compressed ahead, so the first requests for it wait for that, and only they do.
	var script strings.Builder
	for i := range 10000 {
		fmt.Fprintf(&script, "export const v%d = %q;\n", i, strings.Repeat("x", i%17))
	}

	srv.SetUIForTest(fstest.MapFS{"assets/index-0123abcd.js": {Data: []byte(script.String())}})

	server := httptest.NewUnstartedServer(srv.Handler())

	var conns atomic.Int32

	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}

	server.Start()
	t.Cleanup(server.Close)

	// One kept-alive connection per client, as a browser or a bench script keeps them: with
	// the default two idle connections per host the other clients dial anew for every
	// request and run the test host out of ports.
	transport, ok := server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("httptest client without an http.Transport")
	}

	transport = transport.Clone()
	transport.MaxIdleConnsPerHost = clients
	client := &http.Client{Transport: transport, Timeout: 2 * bound}

	t.Cleanup(transport.CloseIdleConnections)

	type result struct {
		path    string
		status  int
		elapsed time.Duration
		err     error
	}

	var (
		mu      sync.Mutex
		results []result
		wg      sync.WaitGroup
	)

	ctx, cancel := context.WithTimeout(t.Context(), duration)
	defer cancel()

	get := func(path string) result {
		start := time.Now()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, http.NoBody)
		if err != nil {
			return result{path, 0, 0, err}
		}

		req.Header.Set("Accept-Encoding", "gzip")

		resp, err := client.Do(req)
		if err != nil {
			return result{path, 0, time.Since(start), err}
		}

		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		return result{path, resp.StatusCode, time.Since(start), err}
	}

	for range clients {
		wg.Go(func() {
			var own []result

			for ctx.Err() == nil {
				own = append(own, get("/api/health"), get(scriptURL))
			}

			mu.Lock()
			defer mu.Unlock()

			results = append(results, own...)
		})
	}

	wg.Wait()

	if len(results) < 2*clients {
		t.Fatalf("only %d requests in %v", len(results), duration)
	}

	var slowest time.Duration

	for _, res := range results {
		slowest = max(slowest, res.elapsed)

		switch {
		case res.err != nil:
			t.Errorf("GET %s failed after %v: %v", res.path, res.elapsed, res.err)
		case res.elapsed > bound:
			t.Errorf("GET %s took %v, over %v", res.path, res.elapsed, bound)
		case res.status == http.StatusServiceUnavailable && res.elapsed > fast503:
			t.Errorf("GET %s answered 503 after %v; a refusal must be at once", res.path, res.elapsed)
		case res.status != http.StatusOK && res.status != http.StatusServiceUnavailable:
			t.Errorf("GET %s: %d", res.path, res.status)
		default:
			// A prompt 200, or a prompt 503 from a bound.
		}
	}

	// The answers keep their connections: a server that closed them would make every client
	// dial again, which on a speaker's Wi-Fi costs a handshake that can be lost.
	if n := int(conns.Load()); n > 2*clients {
		t.Errorf("%d connections for %d clients: answers do not keep their connection", n, clients)
	}

	t.Logf("%d requests from %d clients over %d connections in %v, slowest %v",
		len(results), clients, conns.Load(), duration, slowest)
}
