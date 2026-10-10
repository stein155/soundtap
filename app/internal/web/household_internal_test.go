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
func TestHouseholdStreamMergesPeers(t *testing.T) {
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
	p, _ := strconv.Atoi(port)

	var s Server
	vol := 30
	s.hub = newHub(func(context.Context) (snapshot, bool) {
		return snapshot{Volume: &vol}, true
	}, slog.Default())
	s.household = newHousehold(&s, slog.Default())
	s.SetDevice(Device{ID: "SN2", Name: "Zolder", IP: host, Port: uint16(p)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.hub.run(ctx)
	go s.household.Run(ctx)

	ts := httptest.NewServer(http.HandlerFunc(s.householdEvents))
	defer ts.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	got := map[string]int{}
	done := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var ev struct {
				ID    string `json:"id"`
				Self  bool   `json:"self"`
				State struct {
					Volume int `json:"volume"`
				} `json:"state"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				done <- fmt.Errorf("broken event %q: %w", data, err)
				return
			}
			key := ev.ID
			if ev.Self {
				key = "self"
			}
			got[key] = ev.State.Volume
			if len(got) == 2 {
				done <- nil
				return
			}
		}
		done <- sc.Err()
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
	status := multiroomStatus()
	status.Groups[0].Clients[1].ID = "soundtap-SN3" // follows this speaker
	status.Groups[0].Clients[2].ID = "soundtap-SN4" // dropped out
	status.Groups[1].Clients[0].ID = "soundtap-SN5" // in a group of its own, still listening
	s := Server{log: slog.Default()}
	s.ReconfigureForTest(func(o *Options) {
		o.Multiroom.Backend, o.Multiroom.LocalID = &fakeMultiroom{statuses: []snapcast.Status{status}}, "local"
	})

	var first snapshot
	s.addHouseholdState(context.Background(), &first)
	b, _ := json.Marshal(first)
	if !strings.Contains(string(b), `"followers":["SN3","SN5"]`) || first.Devices == "" {
		t.Fatalf("pushed %s, want followers SN3 and SN5 and a devices revision", b)
	}

	s.SetDevice(Device{ID: "SN2", Name: "Zolder", IP: "10.0.0.9", Port: 80})
	var second snapshot
	s.addHouseholdState(context.Background(), &second)
	if second.Devices == first.Devices {
		t.Fatalf("devices revision %q unchanged after a new speaker", second.Devices)
	}

	// Nobody listening is an answer too: an empty list, not an omitted one.
	s.ReconfigureForTest(func(o *Options) {
		o.Multiroom.Backend, o.Multiroom.LocalID = &fakeMultiroom{statuses: []snapcast.Status{{}}}, "local"
	})
	var lone snapshot
	s.addHouseholdState(context.Background(), &lone)
	if b, _ := json.Marshal(lone); !strings.Contains(string(b), `"followers":[]`) {
		t.Fatalf("pushed %s, want an empty followers list", b)
	}
}
