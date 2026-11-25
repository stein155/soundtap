// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/household"
)

// A client that reconnects in a loop must not be able to grow the hub without
// bound: this box has 64 MB of RAM.
func TestHubCapsSubscribers(t *testing.T) {
	t.Parallel()

	hub := newHub(func(context.Context) (snapshot, bool) {
		return snapshot{Now: nil, Volume: nil, Muted: false, Presets: "", Followers: nil, Devices: ""}, false
	}, slog.New(slog.DiscardHandler))

	stops := make([]func(), 0, maxSubscribers)

	for i := range maxSubscribers {
		_, stop, ok := hub.subscribe()
		if !ok {
			t.Fatalf("subscriber %d refused below the cap", i)
		}

		stops = append(stops, stop)
	}

	if _, _, ok := hub.subscribe(); ok {
		t.Fatal("subscriber past the cap was accepted")
	}

	stops[0]()

	ch, stop, ok := hub.subscribe()
	if !ok || ch == nil {
		t.Fatal("a freed slot was not reusable")
	}

	stop()
}

// A client whose queue filled up must still end on the latest state, even when that
// state never changes again.
func TestHubSlowSubscriberEndsOnLatestState(t *testing.T) {
	t.Parallel()

	hub := newHub(func(context.Context) (snapshot, bool) {
		return snapshot{Now: nil, Volume: nil, Muted: false, Presets: "", Followers: nil, Devices: ""}, false
	}, slog.New(slog.DiscardHandler))

	frames, stop, _ := hub.subscribe()
	defer stop()

	for len(frames) > 0 { // the replay, if any
		<-frames
	}

	for _, s := range []string{"1", "2", "3", "4", "5"} {
		hub.broadcast([]byte(s))
	}

	var last string
	for len(frames) > 0 {
		last = string(<-frames)
	}

	if last != "5" {
		t.Fatalf("slow subscriber ended on %q, want the latest state 5", last)
	}
}

// A new subscriber's replay never arrives after a newer broadcast.
func TestHubReplayNeverFollowsANewerState(t *testing.T) {
	t.Parallel()

	hub := newHub(func(context.Context) (snapshot, bool) {
		return snapshot{Now: nil, Volume: nil, Muted: false, Presets: "", Followers: nil, Devices: ""}, false
	}, slog.New(slog.DiscardHandler))
	for index := range 2000 {
		hub.broadcast([]byte(strconv.Itoa(2 * index)))

		var wg sync.WaitGroup

		wg.Go(func() {
			hub.broadcast([]byte(strconv.Itoa(2*index + 1)))
		})

		frames, stop, _ := hub.subscribe()

		wg.Wait()

		prev := -1

		for len(frames) > 0 {
			n, _ := strconv.Atoi(string(<-frames))
			if n < prev {
				t.Fatalf("round %d: state %d arrived after %d", index, n, prev)
			}

			prev = n
		}

		stop()
	}
}

// pipeListener serves HTTP over net.Pipe, whose writes block until the other end reads:
// a client that stops reading is a phone that dropped off Wi-Fi without closing TCP.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{}), once: sync.Once{}}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })

	return nil
}
func (*pipeListener) Addr() net.Addr { return new(net.TCPAddr) }

// stalledClient opens path on handler, reads up to the stream's retry line and then
// stops reading. It returns the client end, to close when the test is done.
func stalledClient(t *testing.T, handler http.Handler, path string) net.Conn {
	t.Helper()

	listener := newPipeListener()

	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(listener) }()

	t.Cleanup(func() { _ = srv.Close() })

	client, server := net.Pipe()
	listener.conns <- server

	_, err := io.WriteString(client, "GET "+path+" HTTP/1.1\r\nHost: soundtap\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}

	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))

	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "retry:") {
			break
		}
	}

	if scanner.Err() != nil {
		t.Fatal(scanner.Err())
	}

	return client // nothing reads it from here on
}

func shortWriteTimeout(t *testing.T) {
	t.Helper()

	old := sseWriteTimeout
	sseWriteTimeout = 100 * time.Millisecond

	t.Cleanup(func() { sseWriteTimeout = old })
}

// A client that stops reading without closing its connection gives its subscriber slot
// back once a write to it times out, not after TCP gives up some 15 minutes later.
//
//nolint:paralleltest // it shortens the package-level SSE write timeout
func TestEventStreamFreesTheSlotOfAClientThatStopsReading(t *testing.T) {
	shortWriteTimeout(t)

	var srv Server

	srv.hub = newHub(func(context.Context) (snapshot, bool) {
		return snapshot{Now: nil, Volume: nil, Muted: false, Presets: "", Followers: nil, Devices: ""}, false
	}, slog.New(slog.DiscardHandler))

	client := stalledClient(t, jsonRouterErrors(http.HandlerFunc(srv.events)), "/api/events")
	defer client.Close()

	if n := srv.hub.subCount(); n != 1 {
		t.Fatalf("%d subscribers, want 1", n)
	}

	srv.hub.broadcast([]byte(`{"volume":12}`)) // the write that blocks
	eventually(t, "the stalled client to give its slot back", func() bool { return srv.hub.subCount() == 0 })
}

//nolint:paralleltest // it shortens the package-level SSE write timeout
func TestHouseholdStreamFreesTheSlotOfAClientThatStopsReading(t *testing.T) {
	shortWriteTimeout(t)

	var srv Server

	srv.hub = newHub(func(context.Context) (snapshot, bool) {
		return snapshot{Now: nil, Volume: nil, Muted: false, Presets: "", Followers: nil, Devices: ""}, false
	}, slog.New(slog.DiscardHandler))
	srv.household = household.New(slog.New(slog.DiscardHandler), 1, peerIdleTimeout, peerTransport,
		func(int) map[string]string { return nil },
		func() (<-chan []byte, func(), bool) { return srv.hub.subscribe() })

	ctx := t.Context()

	go srv.household.Run(ctx)

	client := stalledClient(t, jsonRouterErrors(http.HandlerFunc(srv.householdEvents)), "/api/household/events")
	defer client.Close()

	if _, ok := srv.household.Subscribe(); ok {
		t.Fatal("a second browser got the only slot")
	}
	// The household follows this speaker's hub once a browser listens.
	eventually(t, "the household to follow the hub", func() bool { return srv.hub.subCount() == 1 })
	srv.hub.broadcast([]byte(`{"volume":12}`)) // the write that blocks
	eventually(t, "the stalled client to give its slot back", func() bool {
		sub, ok := srv.household.Subscribe()
		if ok {
			srv.household.Unsubscribe(sub)
		}

		return ok
	})
}
