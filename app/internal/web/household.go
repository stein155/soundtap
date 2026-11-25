// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/stein155/soundtap/app/internal/household"
)

// peerIdleTimeout is how long a peer's household stream may stay silent before it is
// dropped and reopened. A peer pings every eventsHeartbeat, so three missed pings mean
// it is hung without closing TCP.
const peerIdleTimeout = 3 * eventsHeartbeat

// newHousehold wires the household stream to this speaker's hub and device registry.
// The hub is looked up per follow, as s.hub may be set after this call in tests.
func newHousehold(s *Server, log *slog.Logger) *household.Household {
	return household.New(log, maxSubscribers, peerIdleTimeout, peerTransport, s.peerAddresses,
		func() (<-chan []byte, func(), bool) { return s.hub.subscribe() })
}

// peerAddresses is peerAddress for every peer worth following: online (no goodbye, not
// past peerMissLimit failed probes), fresh, not this
// speaker, at most limit of them.
func (s *Server) peerAddresses(limit int) map[string]string {
	s.devices.mu.Lock()

	ids := make([]string, 0, len(s.devices.by))
	for id, rec := range s.devices.by {
		if !rec.device.Departure && rec.misses < peerMissLimit && id != s.serial {
			ids = append(ids, id)
		}
	}
	s.devices.mu.Unlock()

	out := map[string]string{}
	for _, deviceID := range ids {
		if len(out) == limit {
			break
		}

		if base := s.peerAddress(deviceID); base != "" {
			out[deviceID] = base
		}
	}

	return out
}

// householdEvents streams every speaker's live state to one browser.
func (s *Server) householdEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http.Flusher); !ok {
		apiError(w, http.StatusInternalServerError, "streaming unsupported")

		return
	}

	sub, ok := s.household.Subscribe()
	if !ok {
		apiError(w, http.StatusServiceUnavailable, "too many event streams")

		return
	}
	defer s.household.Unsubscribe(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	stream := newSSEStream(w)
	if stream.send("retry: 3000\n\n") != nil {
		return
	}

	heartbeat := time.NewTicker(eventsHeartbeat)
	defer heartbeat.Stop()

	for {
		var err error

		select {
		case <-r.Context().Done():
			return
		case <-sub.Ready():
			for _, b := range s.household.Take(sub) {
				err = stream.write("event: state\ndata: %s\n\n", b)
				if err != nil {
					break
				}
			}

			if err == nil {
				err = stream.flush()
			}
		case <-heartbeat.C:
			err = stream.send("event: ping\ndata:\n\n")
		}

		if err != nil {
			return // the client stopped reading: free its slot
		}
	}
}
