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
	h := newHub(func(context.Context) (snapshot, bool) { return snapshot{}, false }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var stops []func()
	for i := 0; i < maxSubscribers; i++ {
		_, stop, ok := h.subscribe()
		if !ok {
			t.Fatalf("subscriber %d refused below the cap", i)
		}
		stops = append(stops, stop)
	}
	if _, _, ok := h.subscribe(); ok {
		t.Fatal("subscriber past the cap was accepted")
	}
	stops[0]()
	ch, stop, ok := h.subscribe()
	if !ok || ch == nil {
		t.Fatal("a freed slot was not reusable")
	}
	stop()
}

// A client whose queue filled up must still end on the latest state, even when that
// state never changes again.
func TestHubSlowSubscriberEndsOnLatestState(t *testing.T) {
	h := newHub(func(context.Context) (snapshot, bool) { return snapshot{}, false }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ch, stop, _ := h.subscribe()
	defer stop()
	for len(ch) > 0 { // the replay, if any
		<-ch
	}
	for _, s := range []string{"1", "2", "3", "4", "5"} {
		h.broadcast([]byte(s))
	}
	var last string
	for len(ch) > 0 {
		last = string(<-ch)
	}
	if last != "5" {
		t.Fatalf("slow subscriber ended on %q, want the latest state 5", last)
	}
}

// A new subscriber's replay never arrives after a newer broadcast.
func TestHubReplayNeverFollowsANewerState(t *testing.T) {
	h := newHub(func(context.Context) (snapshot, bool) { return snapshot{}, false }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 2000; i++ {
		h.broadcast([]byte(strconv.Itoa(2 * i)))
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.broadcast([]byte(strconv.Itoa(2*i + 1)))
		}()
		ch, stop, _ := h.subscribe()
		wg.Wait()
		prev := -1
		for len(ch) > 0 {
			n, _ := strconv.Atoi(string(<-ch))
			if n < prev {
				t.Fatalf("round %d: state %d arrived after %d", i, n, prev)
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
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }

// stalledClient opens path on handler, reads up to the stream's retry line and then
// stops reading. It returns the client end, to close when the test is done.
func stalledClient(t *testing.T, handler http.Handler, path string) net.Conn {
	t.Helper()
	ln := newPipeListener()
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	client, server := net.Pipe()
	ln.conns <- server
	if _, err := io.WriteString(client, "GET "+path+" HTTP/1.1\r\nHost: soundtap\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() && !strings.HasPrefix(sc.Text(), "retry:") {
	}
	if sc.Err() != nil {
		t.Fatal(sc.Err())
	}
	return client // nothing reads it from here on
}

func shortWriteTimeout(t *testing.T) {
	old := sseWriteTimeout
	sseWriteTimeout = 100 * time.Millisecond
	t.Cleanup(func() { sseWriteTimeout = old })
}

// A client that stops reading without closing its connection gives its subscriber slot
// back once a write to it times out, not after TCP gives up some 15 minutes later.
func TestEventStreamFreesTheSlotOfAClientThatStopsReading(t *testing.T) {
	shortWriteTimeout(t)
	var s Server
	s.hub = newHub(func(context.Context) (snapshot, bool) { return snapshot{}, false }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	client := stalledClient(t, jsonRouterErrors(http.HandlerFunc(s.events)), "/api/events")
	defer client.Close()
	if n := s.hub.subCount(); n != 1 {
		t.Fatalf("%d subscribers, want 1", n)
	}
	s.hub.broadcast([]byte(`{"volume":12}`)) // the write that blocks
	eventually(t, "the stalled client to give its slot back", func() bool { return s.hub.subCount() == 0 })
}

func TestHouseholdStreamFreesTheSlotOfAClientThatStopsReading(t *testing.T) {
	shortWriteTimeout(t)
	var s Server
	s.hub = newHub(func(context.Context) (snapshot, bool) { return snapshot{}, false }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.household = household.New(slog.New(slog.NewTextHandler(io.Discard, nil)), 1, peerIdleTimeout, peerTransport,
		func(int) map[string]string { return nil },
		func() (<-chan []byte, func(), bool) { return s.hub.subscribe() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.household.Run(ctx)

	client := stalledClient(t, jsonRouterErrors(http.HandlerFunc(s.householdEvents)), "/api/household/events")
	defer client.Close()
	if _, ok := s.household.Subscribe(); ok {
		t.Fatal("a second browser got the only slot")
	}
	// The household follows this speaker's hub once a browser listens.
	eventually(t, "the household to follow the hub", func() bool { return s.hub.subCount() == 1 })
	s.hub.broadcast([]byte(`{"volume":12}`)) // the write that blocks
	eventually(t, "the stalled client to give its slot back", func() bool {
		sub, ok := s.household.Subscribe()
		if ok {
			s.household.Unsubscribe(sub)
		}
		return ok
	})
}
