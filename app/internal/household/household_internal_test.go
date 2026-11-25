// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package household

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTest is a Household with no peers whose own hub never sends.
func newTest() *Household {
	return New(slog.Default(), 32, 15*time.Second, http.DefaultTransport,
		func(int) map[string]string { return nil },
		func() (<-chan []byte, func(), bool) { return make(chan []byte), func() {}, true })
}

// A subscriber that never leaves must not keep run busy after shutdown.
func TestHouseholdRunStopsOnCancel(t *testing.T) {
	t.Parallel()

	house := newTest()
	if _, ok := house.Subscribe(); !ok {
		t.Fatal("subscribe refused")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		house.Run(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run kept going after cancel")
	}
}

// A peer that stops sending without closing the connection is dropped after the idle
// timeout, so followPeer can reconnect instead of serving stale state forever.
func TestReadPeerIdleTimeout(t *testing.T) {
	t.Parallel()

	gone := make(chan struct{})

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: state\ndata: {\"volume\":12}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(gone)
	}))
	defer peer.Close()

	house := newTest()
	house.IdleTimeout = 150 * time.Millisecond
	done := make(chan error, 1)
	start := time.Now()

	go func() { done <- house.readPeer(context.Background(), "SN2", peer.URL+"/api/events") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("readPeer returned no error for a dropped stream")
		}

		if d := time.Since(start); d < house.IdleTimeout {
			t.Fatalf("returned after %v, before the idle timeout", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readPeer did not return after the idle timeout")
	}

	select {
	case <-gone:
	case <-time.After(2 * time.Second):
		t.Fatal("peer request context was not cancelled")
	}
}

// A peer that went away leaves nothing for the next browser to be told.
func TestPeerStateGoesWithItsStream(t *testing.T) {
	t.Parallel()

	house := newTest()
	house.publish(context.Background(), "peer", []byte(`{"volume":3}`))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		house.followPeer(ctx, "peer", "http://127.0.0.1:1") // refuses at once
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)

	for {
		house.mu.Lock()
		_, held := house.last["peer"]
		house.mu.Unlock()

		if !held {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("last state of a gone peer is still held")
		}

		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done
}

// A replaced peer stream, once cancelled, neither clears nor overwrites the state its
// replacement published.
func TestReplacedPeerStreamLeavesNewState(t *testing.T) {
	t.Parallel()

	house := newTest()
	old, cancelOld := context.WithCancel(context.Background())
	cancelOld()
	house.publish(context.Background(), "SN2", []byte(`{"volume":7}`))
	house.publish(old, "SN2", []byte(`{"volume":3}`))

	done := make(chan struct{})

	go func() {
		house.followPeer(old, "SN2", "http://127.0.0.1:1")
		close(done)
	}()

	<-done
	house.mu.Lock()
	got := string(house.last["SN2"])
	house.mu.Unlock()

	if !strings.Contains(got, `"volume":7`) {
		t.Fatalf("SN2 state = %q, want the replacement's volume 7 kept", got)
	}
}
