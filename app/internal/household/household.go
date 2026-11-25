// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package household merges the live state of every speaker in the household into one
// stream, so the home screen needs one connection instead of polling each room through
// the device proxy. The serving speaker follows its own event hub and, while at least
// one browser listens, each online peer's /api/events. Peers are read on the speaker,
// not in the browser: over HTTP/1.1 a browser holds at most six connections per origin,
// and a stream per room would starve every other request. See ADR 0056.
package household

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

const (
	// reconcile is how often the set of followed peers is matched against the device
	// registry, and how fast the streams stop after the last browser leaves.
	reconcile = 10 * time.Second
	// retry is the pause before a dropped peer stream is opened again.
	retry = 3 * time.Second
	// maxPeerStreams bounds the upstream streams; the registry is filled from
	// unauthenticated multicast, and each stream costs a goroutine and a read buffer.
	maxPeerStreams = 16
	// maxPeerEvent bounds one line of a peer's stream.
	maxPeerEvent = 64 << 10
	// peerReadBuffer is the initial read buffer of a peer stream; it grows to maxPeerEvent.
	peerReadBuffer = 4096
)

var (
	// errPeerStatus is a peer stream that answered with anything but 200.
	errPeerStatus = errors.New("status")
	// errPeerClosed is a peer stream that the peer ended.
	errPeerClosed = errors.New("closed")
)

// Sub is one browser. Updates coalesce per speaker: a slow client gets the latest
// state of every room, never a queue in which one room's update pushed out another's.
type Sub struct {
	pending map[string][]byte
	ready   chan struct{}
}

// Ready is signalled when Take has something.
func (sub *Sub) Ready() <-chan struct{} { return sub.ready }

// Household is the merged stream.
//
// peers returns the base URL of each peer worth following, by id, at most limit of
// them. self subscribes to this speaker's own event hub: its channel, the unsubscribe
// function, and false when the hub is full.
type Household struct {
	log       *slog.Logger
	maxSubs   int
	peers     func(limit int) map[string]string
	self      func() (<-chan []byte, func(), bool)
	transport http.RoundTripper

	mu   sync.Mutex
	subs map[*Sub]struct{}
	last map[string][]byte // latest payload per speaker key, replayed to new subscribers
	wake chan struct{}

	// IdleTimeout is how long a peer's stream may stay silent, before its first byte or
	// between lines, before it is dropped and reopened. A field so a test can shorten it
	// without racing other tests' peers.
	IdleTimeout time.Duration
}

// New returns a Household that accepts at most maxSubs browsers.
func New(log *slog.Logger, maxSubs int, idle time.Duration, transport http.RoundTripper,
	peers func(limit int) map[string]string, self func() (<-chan []byte, func(), bool),
) *Household {
	return &Household{
		log:       log,
		maxSubs:   maxSubs,
		peers:     peers,
		self:      self,
		transport: transport,
		subs:      map[*Sub]struct{}{},
		mu:        sync.Mutex{},
		last:      map[string][]byte{},
		wake:      make(chan struct{}, 1),

		IdleTimeout: idle,
	}
}

// wireEvent is what the browser receives: Self marks the serving speaker, whose id the page
// knows only from /api/devices.
type wireEvent struct {
	ID    string          `json:"id,omitempty"`
	Self  bool            `json:"self,omitempty"`
	State json.RawMessage `json:"state"`
}

// Subscribe registers a browser with every known state already pending. The second
// result is false when maxSubs streams are already open.
func (h *Household) Subscribe() (*Sub, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.subs) >= h.maxSubs {
		return nil, false
	}

	sub := &Sub{pending: map[string][]byte{}, ready: make(chan struct{}, 1)}
	maps.Copy(sub.pending, h.last)

	if len(sub.pending) > 0 {
		sub.ready <- struct{}{}
	}

	h.subs[sub] = struct{}{}
	h.poke()

	return sub, true
}

// Unsubscribe drops a browser.
func (h *Household) Unsubscribe(sub *Sub) {
	h.mu.Lock()
	delete(h.subs, sub)
	h.mu.Unlock()
	h.poke()
}

// Take empties a subscriber's pending updates.
func (h *Household) Take(sub *Sub) [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([][]byte, 0, len(sub.pending))
	for k, b := range sub.pending {
		out = append(out, b)

		delete(sub.pending, k)
	}

	return out
}

// Run follows this speaker and its peers while anyone listens, and nothing otherwise.
func (h *Household) Run(ctx context.Context) {
	for {
		for h.subCount() == 0 {
			select {
			case <-ctx.Done():
				return
			case <-h.wake:
			}
		}

		h.follow(ctx)

		if ctx.Err() != nil {
			return
		}
	}
}

// publish records one speaker's state and hands it to every subscriber. key is the peer
// id, or "" for this speaker.
// publish caches and forwards state for key while ctx is live. The check is under h.mu,
// and a replaced peer stream is cancelled before its entry is cleared under h.mu, so a
// cancelled stream can never write over its replacement's state.
func (h *Household) publish(ctx context.Context, key string, state []byte) {
	if !json.Valid(state) {
		return // a peer's bytes go into our stream verbatim; never let them break it
	}

	payload, err := json.Marshal(wireEvent{ID: key, Self: key == "", State: state})
	if err != nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if ctx.Err() != nil {
		return
	}

	if bytes.Equal(h.last[key], payload) {
		return
	}

	h.last[key] = payload
	for sub := range h.subs {
		sub.pending[key] = payload
		select {
		case sub.ready <- struct{}{}:
		default:
		}
	}
}

func (h *Household) poke() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *Household) subCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return len(h.subs)
}

// follow runs the upstream streams until the last subscriber leaves or ctx ends. The
// state cache is dropped on the way out: nobody is told about changes while no one
// listens, so what it holds would be stale for the next browser.
func (h *Household) follow(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)

	var wg sync.WaitGroup

	defer func() {
		cancel()
		wg.Wait()
		h.mu.Lock()
		clear(h.last)
		h.mu.Unlock()
	}()

	wg.Add(1)
	safego.Go("household.followSelf", func() {
		defer wg.Done()

		h.followSelf(ctx)
	})

	type stream struct {
		base   string
		cancel context.CancelFunc
	}

	peers := map[string]stream{}

	tick := time.NewTicker(reconcile)
	defer tick.Stop()

	for {
		want := h.peers(maxPeerStreams)
		for peerID, p := range peers {
			if want[peerID] == p.base {
				continue
			}

			p.cancel()
			delete(peers, peerID)
			h.mu.Lock()
			delete(h.last, peerID)
			h.mu.Unlock()
		}

		for peerID, base := range want {
			if _, ok := peers[peerID]; ok {
				continue
			}

			pctx, pcancel := context.WithCancel(ctx)
			peers[peerID] = stream{base, pcancel}

			wg.Add(1)
			safego.Go("household.followPeer", func() {
				defer wg.Done()

				h.followPeer(pctx, peerID, base)
			})
		}

		select {
		case <-ctx.Done():
			return
		case <-h.wake:
		case <-tick.C:
		}

		if h.subCount() == 0 {
			return
		}
	}
}

// followSelf forwards this speaker's own hub.
func (h *Household) followSelf(ctx context.Context) {
	events, unsubscribe, ok := h.self()
	if !ok {
		h.log.Warn("household: own event hub full")

		return
	}
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case state, ok := <-events:
			if !ok {
				return
			}

			h.publish(ctx, "", state)
		}
	}
}

// followPeer keeps one peer's /api/events open until ctx ends.
func (h *Household) followPeer(ctx context.Context, peerID, base string) {
	for {
		err := h.readPeer(ctx, peerID, base+"/api/events")
		if err != nil && ctx.Err() == nil {
			h.log.Debug("household: peer stream ended", "device", peerID, "err", err)
		}

		if ctx.Err() != nil {
			return // replaced or stopped: follow cleared the entry; a newer stream may own it
		}
		// Nothing says what it plays now: a browser that joins must not get its last state
		// for the minutes it takes the peer to fall off the list.
		h.mu.Lock()
		delete(h.last, peerID)
		h.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

func (h *Household) readPeer(ctx context.Context, peerID, url string) error {
	// Only this peer's request is cancelled when it goes silent; followPeer reconnects.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	idle := time.AfterFunc(h.IdleTimeout, cancel)
	defer idle.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("peer request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")

	resp, err := h.transport.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("peer request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w %d", errPeerStatus, resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, peerReadBuffer), maxPeerEvent)

	event := ""

	for scanner.Scan() {
		idle.Reset(h.IdleTimeout)

		line := scanner.Text()
		switch {
		case line == "":
			event = ""
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:") && event == "state":
			h.publish(ctx, peerID, []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))))
		default:
			// Other fields (id:, retry:, comments) and other events carry nothing we use.
		}
	}

	err = scanner.Err()
	if err != nil {
		return fmt.Errorf("peer stream: %w", err)
	}

	return errPeerClosed
}
