// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/snapcast"
)

// One household stream carries this speaker's own state and each peer's, tagged by id,
// and a peer that sends something other than JSON cannot break it.

// householdEvent is one speaker's entry in the household stream.
type householdEvent struct {
	ID    string         `json:"id"`
	Self  bool           `json:"self"`
	State householdState `json:"state"`
}

// householdState is the part of a speaker's state the household stream test reads.
type householdState struct {
	Volume int `json:"volume"`
}

func TestHouseholdStreamMergesPeers(t *testing.T) {
	t.Parallel()

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events" {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "retry: 3000\n\nevent: state\ndata: not json\n\n")
		fmt.Fprint(w, "event: state\ndata: {\"volume\":12}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer peer.Close()

	host, port, _ := net.SplitHostPort(strings.TrimPrefix(peer.URL, "http://"))
	peerPort, _ := strconv.Atoi(port)

	var srv Server

	vol := 30
	srv.hub = newHub(func(context.Context) (snapshot, bool) {
		return snapshot{Volume: &vol, Now: nil, Muted: false, Presets: "", Followers: nil, Devices: ""}, true
	}, slog.Default())
	srv.household = newHousehold(&srv, slog.Default())
	srv.SetDevice(Device{
		ID:           "SN2",
		Name:         "Zolder",
		IP:           host,
		Port:         uint16(peerPort),
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.hub.run(ctx)
	go srv.household.Run(ctx)

	ts := httptest.NewServer(http.HandlerFunc(srv.householdEvents))
	defer ts.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, http.NoBody)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	got := map[string]int{}
	done := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			data, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}

			var event householdEvent

			err := json.Unmarshal([]byte(data), &event)
			if err != nil {
				done <- fmt.Errorf("broken event %q: %w", data, err)

				return
			}

			key := event.ID
			if event.Self {
				key = "self"
			}

			got[key] = event.State.Volume
			if len(got) == 2 {
				done <- nil

				return
			}
		}

		done <- scanner.Err()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatalf("timed out; got %v", got)
	}

	if got["self"] != 30 || got["SN2"] != 12 {
		t.Fatalf("states = %v, want self 30 and SN2 12", got)
	}
}

// A speaker's pushed state says who plays along with it and when its device registry
// changed, so the home screen can leave each speaker's group and the device list unpolled
// while the household stream is open.
func TestStateCarriesFollowersAndDevices(t *testing.T) {
	t.Parallel()

	status := multiroomStatus()
	status.Groups[0].Clients[1].ID = "soundtap-SN3" // follows this speaker
	status.Groups[0].Clients[2].ID = "soundtap-SN4" // dropped out
	status.Groups[1].Clients[0].ID = "soundtap-SN5" // in a group of its own, still listening

	var srv Server

	srv.log = slog.Default()
	srv.ReconfigureForTest(func(o *Options) {
		o.Multiroom.Backend, o.Multiroom.LocalID = &fakeMultiroom{
			statuses: []snapcast.Status{status},
			volumeID: "",
			volume:   snapcast.Volume{Percent: 0, Muted: false},
			groupID:  "",
			clients:  nil,
			latency:  nil,
		}, "local"
	})

	var first snapshot
	srv.addHouseholdState(context.Background(), &first)

	pushedFirst, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(pushedFirst), `"followers":["SN3","SN5"]`) || first.Devices == "" {
		t.Fatalf("pushed %s, want followers SN3 and SN5 and a devices revision", pushedFirst)
	}

	srv.SetDevice(Device{
		ID:           "SN2",
		Name:         "Zolder",
		IP:           "10.0.0.9",
		Port:         80,
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})

	var second snapshot
	srv.addHouseholdState(context.Background(), &second)

	if second.Devices == first.Devices {
		t.Fatalf("devices revision %q unchanged after a new speaker", second.Devices)
	}

	// Nobody listening is an answer too: an empty list, not an omitted one.
	srv.ReconfigureForTest(func(o *Options) {
		o.Multiroom.Backend, o.Multiroom.LocalID = &fakeMultiroom{
			statuses: []snapcast.Status{{Groups: nil}},
			volumeID: "",
			volume:   snapcast.Volume{Percent: 0, Muted: false},
			groupID:  "",
			clients:  nil,
			latency:  nil,
		}, "local"
	})

	var lone snapshot
	srv.addHouseholdState(context.Background(), &lone)

	pushed, err := json.Marshal(lone)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(pushed), `"followers":[]`) {
		t.Fatalf("pushed %s, want an empty followers list", pushed)
	}
}
