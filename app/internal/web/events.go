// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// Live state pushed to browsers over Server-Sent Events (/api/events) so the UI
// updates the instant playback or volume changes, instead of each tab polling
// /api/now + /api/volume every few seconds. One hub polls the speaker on behalf
// of every connected browser and broadcasts only when the state actually
// changes, so N open tabs cost one poll loop, not N.
const (
	// eventsActive is the poll cadence while playback is in flux (buffering /
	// switching), so "starten → bufferen → live" lands quickly on screen.
	eventsActive = 1500 * time.Millisecond
	// eventsIdle is the calm cadence otherwise — still catches volume changes
	// from the physical buttons or another app within a couple of seconds.
	eventsIdle = 3 * time.Second
	// eventsHeartbeat keeps the SSE connection warm through proxies that would
	// otherwise time out an idle stream. It is a real event, not a comment, so the
	// browser sees it too: a stream that goes quiet for a few beats belongs to a
	// speaker that is gone (a reboot black-holes the TCP connection rather than
	// closing it), and the page says so.
	eventsHeartbeat = 5 * time.Second
	// maxSubscribers caps concurrent SSE streams. This box has 64 MB of RAM and one
	// core; a browser that reconnects in a loop, or anything else holding /api/events
	// open, would otherwise add a goroutine and a buffer per connection until the
	// daemon is killed. A handful of tabs plus MQTT clients is the real load.
	maxSubscribers = 32
)

// sseWriteTimeout bounds each write to an event stream. A phone that drops off Wi-Fi
// stops acknowledging without closing TCP, and a write to it would block until TCP
// gives up, about 15 minutes, holding one of the maxSubscribers slots all that time.
// A live client takes every write within a heartbeat, so it never comes near this.
var sseWriteTimeout = 15 * time.Second

// sseStream writes one event stream, each write under a fresh write deadline. An error
// from write or flush means the client is gone: the handler ends and frees its slot.
type sseStream struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func newSSEStream(w http.ResponseWriter) *sseStream {
	return &sseStream{w: w, rc: http.NewResponseController(w)}
}

func (s *sseStream) write(format string, args ...any) error {
	if err := s.rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(s.w, format, args...)
	return err
}

func (s *sseStream) flush() error { return s.rc.Flush() }

// send writes one event and flushes it to the client.
func (s *sseStream) send(format string, args ...any) error {
	if err := s.write(format, args...); err != nil {
		return err
	}
	return s.flush()
}

// snapshot is one push of live speaker state. Fields are omitted when a read
// failed so the browser keeps its last-known value instead of a zeroed one.
type snapshot struct {
	Now    *speaker.NowPlaying `json:"now,omitempty"`
	Volume *int                `json:"volume,omitempty"`
	Muted  bool                `json:"muted,omitempty"` // the speaker is silent; Volume is the level it returns to
	// Presets is a revision of the stored presets, not the presets themselves: it
	// changes when any slot does, so an open page re-reads them after a change made on
	// the speaker's buttons, another phone or an MQTT client.
	Presets string `json:"presets,omitempty"`
	// Followers is the device id of every speaker playing along with this one, and
	// Devices a revision of the device registry. Over the household stream they let the
	// home screen see groups and speakers change without asking each speaker.
	// Followers is omitted when this speaker's snapserver could not be read.
	Followers *[]string `json:"followers,omitempty"`
	Devices   string    `json:"devices,omitempty"`
}

// hub fans one speaker poll loop out to every connected SSE client.
type hub struct {
	poll func(context.Context) (snapshot, bool) // reads the speaker; false on error
	log  *slog.Logger

	mu   sync.Mutex
	subs map[chan []byte]struct{}
	last []byte // most recent broadcast payload, replayed to new subscribers
	wake chan struct{}
}

func newHub(poll func(context.Context) (snapshot, bool), log *slog.Logger) *hub {
	return &hub{
		poll: poll,
		log:  log,
		subs: map[chan []byte]struct{}{},
		wake: make(chan struct{}, 1),
	}
}

// run drives the poll loop until ctx is cancelled. It idles (no speaker traffic)
// whenever no browser is connected, and polls promptly again on a nudge or when
// a new subscriber joins.
func (h *hub) run(ctx context.Context) {
	for {
		if h.subCount() == 0 {
			select {
			case <-ctx.Done():
				return
			case <-h.wake:
				continue
			}
		}
		interval := eventsIdle
		if snap, ok := h.poll(ctx); ok {
			if b, err := json.Marshal(snap); err == nil {
				h.broadcast(b)
			}
			if activeNow(snap.Now) {
				interval = eventsActive
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-h.wake: // a nudge (state just changed) or a new subscriber
		case <-time.After(interval):
		}
	}
}

// activeNow reports whether playback is mid-transition, so the loop polls faster
// until it settles.
func activeNow(np *speaker.NowPlaying) bool {
	return np != nil && np.PlayStatus == "BUFFERING_STATE"
}

func (h *hub) subCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// broadcast delivers a payload to every subscriber, remembering it for clients
// that connect later. A slow client never stalls the loop: when its queue is full the
// oldest queued snapshot makes room for this one. Skipping the newest instead left a
// client on stale state for as long as the state then stayed the same.
func (h *hub) broadcast(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if bytes.Equal(b, h.last) {
		return // nothing changed; don't wake idle clients
	}
	h.last = b
	for ch := range h.subs {
		select {
		case ch <- b:
		default:
			select { // full: every queued snapshot is older than this one
			case <-ch:
			default:
			}
			select { // only broadcast sends, under h.mu, so this slot stays free
			case ch <- b:
			default:
			}
		}
	}
}

// subscribe registers a client. It replays the last known state immediately and
// nudges the loop to fetch a fresh reading, then returns an unsubscribe func.
// The third result is false when maxSubscribers streams are already open.
func (h *hub) subscribe() (<-chan []byte, func(), bool) {
	ch := make(chan []byte, 4)
	h.mu.Lock()
	if len(h.subs) >= maxSubscribers {
		h.mu.Unlock()
		return nil, func() {}, false
	}
	// The replay is queued before the lock goes: a broadcast in between would otherwise
	// queue a newer state ahead of it, and the client would end on the older one.
	if h.last != nil {
		ch <- h.last // buffered and empty: cannot block
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	h.nudge()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}, true
}

// nudge asks the loop to poll now instead of waiting for the next tick. Called
// after any action that changes playback or volume so the push is immediate.
func (h *hub) nudge() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// pollState reads the live speaker state the UI cares about. now-playing is
// required (its failure means the speaker is unreachable); volume is
// best-effort so a hiccup reading it doesn't blank the now-playing push.
func (s *Server) pollState(ctx context.Context) (snapshot, bool) {
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	np, err := s.backend.NowPlaying(c)
	if err != nil {
		return snapshot{}, false
	}
	s.enrichNow(np)
	snap := snapshot{Now: np}
	if v, err := s.backend.Volume(c); err == nil {
		snap.Volume = &v
		snap.Muted = muted(s.backend)
	}
	if ps, err := s.backend.Presets(c); err == nil {
		snap.Presets = presetsRevision(ps)
	}
	s.addHouseholdState(c, &snap)
	return snap, true
}

// addHouseholdState fills in who follows this speaker, read from its snapserver as
// GET /api/multiroom reports it, and the device registry's revision.
func (s *Server) addHouseholdState(ctx context.Context, snap *snapshot) {
	if s.multiroom != nil {
		if status, err := s.multiroom.GetStatus(ctx); err == nil {
			ids := []string{}
			for _, group := range status.Groups {
				for _, c := range group.Clients {
					if id := multiroom.DeviceIDFor(c.ID); c.Connected && c.ID != s.snapclientID && id != "" {
						ids = append(ids, id)
					}
				}
			}
			sort.Strings(ids) // the order snapserver lists them in is no change
			snap.Followers = &ids
		}
	}
	h := fnv.New64a()
	if s.settings != nil {
		fmt.Fprintf(h, "%s\n", s.settings.Get().Name)
	}
	for _, d := range s.knownDevices(Device{}) {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%v\n", d.ID, d.Name, d.Model, d.Firmware, d.IP, d.Port, d.Departure)
	}
	snap.Devices = strconv.FormatUint(h.Sum64(), 36)
}

// presetsRevision is a short hash of what the preset slots hold. It hashes the presets
// as they are served, every field included, so a change only to a logo or to a
// field added later still tells open pages to re-read them.
func presetsRevision(ps []speaker.Preset) string {
	h := fnv.New64a()
	_ = json.NewEncoder(h).Encode(ps) // a hash.Hash never fails a write
	return strconv.FormatUint(h.Sum64(), 36)
}

// events streams live speaker state to one browser over Server-Sent Events.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http.Flusher); !ok {
		apiError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // don't let a reverse proxy buffer the stream

	ch, unsubscribe, ok := s.hub.subscribe()
	if !ok {
		apiError(w, http.StatusServiceUnavailable, "too many event streams")
		return
	}
	defer unsubscribe()

	stream := newSSEStream(w)
	if stream.send("retry: 3000\n\n") != nil { // tell EventSource how fast to reconnect
		return
	}

	hb := time.NewTicker(eventsHeartbeat)
	defer hb.Stop()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case b, ok := <-ch:
			if !ok {
				return
			}
			err = stream.send("event: state\ndata: %s\n\n", b)
		case <-hb.C:
			err = stream.send("event: ping\ndata:\n\n")
		}
		if err != nil {
			return // the client stopped reading: free its slot
		}
	}
}
