// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/snapcast"
	"github.com/stein155/soundtap/app/internal/speaker"
)

type fakeMultiroom struct {
	statuses []snapcast.Status
	volumeID string
	volume   snapcast.Volume
	groupID  string
	clients  []string
	latency  map[string]int
}

func (f *fakeMultiroom) GetStatus(context.Context) (snapcast.Status, error) {
	status := f.statuses[0]
	if len(f.statuses) > 1 {
		f.statuses = f.statuses[1:]
	}

	return status, nil
}

func (f *fakeMultiroom) SetVolume(_ context.Context, id string, volume snapcast.Volume) error {
	f.volumeID, f.volume = id, volume

	return nil
}

func (f *fakeMultiroom) SetLatency(_ context.Context, id string, ms int) error {
	if f.latency == nil {
		f.latency = map[string]int{}
	}

	f.latency[id] = ms

	return nil
}

func (f *fakeMultiroom) SetClients(_ context.Context, groupID string, clients []string) error {
	f.groupID, f.clients = groupID, append([]string(nil), clients...)

	return nil
}

func multiroomStatus() snapcast.Status {
	status := snapcast.Status{Groups: []snapcast.Group{
		{ID: "local-group", Clients: nil},
		{ID: "other-group", Clients: nil},
	}}
	client := func(id, configName, hostName, ip string, connected bool, percent int, muted bool) snapcast.ClientInfo {
		var client snapcast.ClientInfo

		client.ID, client.Connected = id, connected
		client.Config.Name, client.Host.Name, client.Host.IP = configName, hostName, ip
		client.Config.Volume = snapcast.Volume{Percent: percent, Muted: muted}

		return client
	}
	status.Groups[0].Clients = []snapcast.ClientInfo{
		client("local", "Living room", "soundtap", "10.0.0.2", true, 20, false),
		client("peer", "", "Kitchen", "10.0.0.3", true, 30, true),
		client("offline", "", "Offline", "10.0.0.4", false, 40, false),
	}
	status.Groups[1].Clients = []snapcast.ClientInfo{client("other", "", "", "10.0.0.5", true, 50, false)}

	return status
}

func multiroomHandler(fake *fakeMultiroom) http.Handler {
	s := new(Server)
	s.log = slog.New(slog.DiscardHandler)
	s.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = fake, "local" })

	return s.Handler()
}

func multiroomRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// A client that dropped out is reported as offline rather than dropped from the
// list: a speaker that silently vanishes reads as a bug in the app.
func TestMultiroomListsConnectedClients(t *testing.T) {
	t.Parallel()

	handler := multiroomHandler(&fakeMultiroom{
		statuses: []snapcast.Status{multiroomStatus()},
		volumeID: "",
		volume:   snapcast.Volume{Percent: 0, Muted: false},
		groupID:  "",
		clients:  nil,
		latency:  nil,
	})

	rec := multiroomRequest(t, handler, http.MethodGet, "/api/multiroom", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}

	var rows []multiroom.Client

	err := json.Unmarshal(rec.Body.Bytes(), &rows)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 4 || rows[0].Name != "Living room" || !rows[0].Current || !rows[0].Member || !rows[0].Connected {
		t.Fatalf("rows = %+v", rows)
	}

	if rows[1].Name != "Kitchen" || rows[1].Percent != 30 || !rows[1].Muted || !rows[1].Member || !rows[1].Connected {
		t.Fatalf("peer = %+v", rows[1])
	}

	if rows[2].Name != "Offline" || rows[2].Connected {
		t.Fatalf("offline = %+v", rows[2])
	}

	if rows[3].Name != "other" || rows[3].Member || rows[3].GroupID != "other-group" {
		t.Fatalf("other = %+v", rows[3])
	}
}

func TestMultiroomVolumeAndMembership(t *testing.T) {
	t.Parallel()

	fake := &fakeMultiroom{
		statuses: []snapcast.Status{multiroomStatus(), multiroomStatus()},
		volumeID: "",
		volume:   snapcast.Volume{Percent: 0, Muted: false},
		groupID:  "",
		clients:  nil,
		latency:  nil,
	}
	handler := multiroomHandler(fake)

	rec := multiroomRequest(
		t,
		handler,
		http.MethodPut,
		"/api/multiroom/clients/peer/volume",
		`{"percent":61,"muted":true}`,
	)
	if rec.Code != http.StatusOK || fake.volumeID != "peer" || fake.volume != (snapcast.Volume{
		Percent: 61,
		Muted:   true,
	}) {
		t.Fatalf("volume = %d id=%q value=%+v", rec.Code, fake.volumeID, fake.volume)
	}

	rec = multiroomRequest(t, handler, http.MethodPut, "/api/multiroom/clients/peer/membership", `{"member":false}`)
	if rec.Code != http.StatusOK || fake.groupID != "local-group" ||
		len(fake.clients) != 2 || fake.clients[0] != "local" || fake.clients[1] != "offline" ||
		fake.volumeID != "peer" || fake.volume != (snapcast.Volume{Percent: 30, Muted: true}) {
		t.Fatalf("membership = %d group=%q clients=%v", rec.Code, fake.groupID, fake.clients)
	}
}

func TestMultiroomValidation(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		path string
		body string
		want int
	}{
		{"/api/multiroom/clients/bad%2Fid/volume", `{"percent":10,"muted":false}`, http.StatusBadRequest},
		{"/api/multiroom/clients/peer%232/volume", `{"percent":10,"muted":false}`, http.StatusNotFound},
		{"/api/multiroom/clients/peer/volume", `{"percent":101,"muted":false}`, http.StatusBadRequest},
		{"/api/multiroom/clients/peer/volume", `{"percent":10}`, http.StatusBadRequest},
		{"/api/multiroom/clients/missing/volume", `{"percent":10,"muted":false}`, http.StatusNotFound},
		{"/api/multiroom/clients/local/membership", `{"member":false}`, http.StatusBadRequest},
	} {
		handler := multiroomHandler(&fakeMultiroom{
			statuses: []snapcast.Status{multiroomStatus()},
			volumeID: "",
			volume:   snapcast.Volume{Percent: 0, Muted: false},
			groupID:  "",
			clients:  nil,
			latency:  nil,
		})

		rec := multiroomRequest(t, handler, http.MethodPut, testCase.path, testCase.body)
		if rec.Code != testCase.want {
			t.Errorf("PUT %s = %d, want %d: %s", testCase.path, rec.Code, testCase.want, rec.Body.String())
		}
	}
}

// Latency and "last seen" are the two numbers that explain a group that is out of
// sync or short a speaker, so they have to survive the mapping to JSON.
func TestMultiroomReportsLatencyAndLastSeen(t *testing.T) {
	t.Parallel()

	status := multiroomStatus()
	status.Groups[0].Clients[1].Config.Latency = 120
	status.Groups[0].Clients[2].LastSeen.Sec = time.Now().Unix() - 90

	handler := multiroomHandler(&fakeMultiroom{
		statuses: []snapcast.Status{status},
		volumeID: "",
		volume:   snapcast.Volume{Percent: 0, Muted: false},
		groupID:  "",
		clients:  nil,
		latency:  nil,
	})

	var rows []multiroom.Client

	err := json.Unmarshal(multiroomRequest(t, handler, http.MethodGet, "/api/multiroom", "").Body.Bytes(), &rows)
	if err != nil {
		t.Fatal(err)
	}

	if rows[1].Latency != 120 {
		t.Errorf("latency = %d, want 120", rows[1].Latency)
	}

	if rows[2].LastSeenAgo < 80 || rows[2].LastSeenAgo > 200 {
		t.Errorf("lastSeenAgo = %d, want about 90", rows[2].LastSeenAgo)
	}
	// A connected client has no "last seen": it is being seen now.
	if rows[1].LastSeenAgo != 0 {
		t.Errorf("connected client reports lastSeenAgo = %d", rows[1].LastSeenAgo)
	}
}

// Following another speaker is one setting and one restart. The wrapper the boot script
// supervises reads that setting, so what this endpoint has to get right is the file, the
// kick, and refusing anything that would not survive being pasted onto a command line.
//
//nolint:paralleltest // it swaps the package-level leader lookup
func TestFollowAnotherSpeakerWritesTheSettingAndKicksTheClient(t *testing.T) {
	defer func(old func(context.Context, string) string) { peerLeader = old }(peerLeader)

	peerLeader = func(context.Context, string) string { return "" }
	dir := t.TempDir()
	kicks := 0
	sources := []string{}
	srv := new(Server)
	srv.homeDir = dir
	srv.restartSnapclient = func() { kicks++ }
	srv.selectSource = func(_ context.Context, source string) error {
		sources = append(sources, source)

		return nil
	}
	path := filepath.Join(dir, "multiroom-server")

	post := func(body string) int {
		w := httptest.NewRecorder()
		srv.postMultiroomFollow(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow",
			strings.NewReader(body)))

		return w.Code
	}

	if code := post(`{"host":"10.0.1.126"}`); code != http.StatusOK {
		t.Fatalf("follow returned %d", code)
	}

	got, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(got)) != "10.0.1.126" {
		t.Fatalf("setting = %q, %v", got, err)
	}

	if kicks != 1 {
		t.Errorf("snapclient kicked %d times, want 1", kicks)
	}

	if code := post(`{"host":""}`); code != http.StatusOK {
		t.Fatalf("unfollow returned %d", code)
	}

	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Errorf("setting survived unfollow: %v", err)
	}

	if len(sources) != 2 || sources[0] != "MULTIROOM" || sources[1] != "STANDBY" {
		t.Errorf("sources = %v, want [MULTIROOM STANDBY]", sources)
	}

	// A host that is not a host never reaches the file, and never kicks anything.
	for _, bad := range []string{
		`{"host":"192.168.1.1 --player alsa"}`, `{"host":"$(reboot)"}`,
		`{"host":"a;b"}`, `{"host":"-x"}`,
	} {
		if code := post(bad); code != http.StatusBadRequest {
			t.Errorf("%s returned %d, want 400", bad, code)
		}
	}

	if kicks != 2 {
		t.Errorf("snapclient kicked %d times after the bad hosts, want 2", kicks)
	}
}

// A follower starts in the order a local source does: the old source stops, the source
// switches (the Mixer's hold closes), and only then does the leader's stream come in.
// The client used to restart first, ahead of the hold.
//
//nolint:paralleltest // it swaps the package-level leader lookup
func TestFollowSwitchesTheSourceBeforeTheStream(t *testing.T) {
	defer func(old func(context.Context, string) string) { peerLeader = old }(peerLeader)

	peerLeader = func(context.Context, string) string { return "" }

	var steps []string

	srv := new(Server)
	srv.homeDir = t.TempDir()
	srv.sourceChanging = func(source string) { steps = append(steps, "stop old for "+source) }
	srv.restartSnapclient = func() { steps = append(steps, "stream") }
	srv.selectSource = func(_ context.Context, source string) error {
		steps = append(steps, "select "+source)

		return nil
	}
	rec := httptest.NewRecorder()
	srv.postMultiroomFollow(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow",
		strings.NewReader(`{"host":"10.0.1.126"}`)))

	want := []string{"stop old for MULTIROOM", "select MULTIROOM", "stream"}
	if rec.Code != http.StatusOK || !slices.Equal(steps, want) {
		t.Fatalf("follow = %d, steps %q, want %q", rec.Code, steps, want)
	}
}

// Inviting is the same handover seen from the other side: the speaker the user is looking
// at calls the other one, because the browser may not.
func TestInviteAnotherSpeakerCallsItWithThisSpeakersAddress(t *testing.T) {
	t.Parallel()

	var got struct {
		path, body string
	}

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.path, got.body = r.URL.Path, string(b)

		writeJSON(w, http.StatusOK, map[string]string{"following": "10.0.0.1"})
	}))
	defer other.Close()

	host := strings.TrimPrefix(other.URL, "http://")

	anchors := 0
	srv := new(Server)
	srv.selfAddress = func(context.Context) string { return "10.0.0.1" }
	srv.wantAnchor = func() { anchors++ }
	rec := httptest.NewRecorder()
	srv.postMultiroomInvite(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/invite",
		strings.NewReader(`{"ip":"`+host+`"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("invite returned %d: %s", rec.Code, rec.Body.String())
	}
	// About to lead: the parked anchor starts while the other speaker switches over.
	if anchors != 1 {
		t.Errorf("anchor wanted %d times, want 1", anchors)
	}

	if got.path != "/api/multiroom/follow" {
		t.Errorf("called %q", got.path)
	}

	if !strings.Contains(got.body, `"host":"10.0.0.1"`) {
		t.Errorf("body = %q, want this speaker's own address", got.body)
	}

	rec = httptest.NewRecorder()
	srv.postMultiroomInvite(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/invite",
		strings.NewReader(`{"ip":"1.2.3.4 --player alsa"}`)))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("a host with a second word returned %d, want 400", rec.Code)
	}
}

// A speaker others listen to refuses to follow (no loops, no chains); leaving is always
// allowed.
func TestFollowRefusedWhileLeadingListeners(t *testing.T) {
	t.Parallel()

	var leader snapcast.ClientInfo

	leader.ID, leader.Connected = "soundtap-self", true

	var listener snapcast.ClientInfo

	listener.ID, listener.Connected = "soundtap-other", true
	srv := new(Server)
	srv.homeDir = t.TempDir()
	srv.snapclientID = leader.ID
	srv.multiroom = &fakeMultiroom{
		statuses: []snapcast.Status{{Groups: []snapcast.Group{
			{ID: "g", Clients: []snapcast.ClientInfo{leader, listener}},
		}}},
		volumeID: "", volume: snapcast.Volume{Percent: 0, Muted: false}, groupID: "", clients: nil, latency: nil,
	}

	post := func(body string) int {
		w := httptest.NewRecorder()
		srv.postMultiroomFollow(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow",
			strings.NewReader(body)))

		return w.Code
	}
	if code := post(`{"host":"10.0.1.126"}`); code != http.StatusConflict {
		t.Fatalf("follow while leading returned %d, want 409", code)
	}

	if code := post(`{"host":""}`); code != http.StatusOK {
		t.Fatalf("leave returned %d, want 200", code)
	}
}

// A speaker refuses to follow one that itself follows: no chains, and no loop when the
// other one follows this speaker.
//
//nolint:paralleltest // it points the package-level downstream port at a test server
func TestFollowRefusedWhenTheTargetFollows(t *testing.T) {
	targetDir := t.TempDir()
	target := new(Server)
	target.homeDir = targetDir
	target.log = slog.New(slog.DiscardHandler)

	srv := httptest.NewServer(target.Handler())
	defer srv.Close()
	defer func(p string) { downstreamPort = p }(downstreamPort)

	downstreamPort = srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	s := new(Server)
	s.homeDir = t.TempDir()
	post := func() int {
		w := httptest.NewRecorder()
		s.postMultiroomFollow(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow",
			strings.NewReader(`{"host":"127.0.0.1"}`)))

		return w.Code
	}

	leaderFile := filepath.Join(targetDir, "multiroom-server")

	err := os.WriteFile(leaderFile, []byte("10.0.0.9\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if code := post(); code != http.StatusConflict {
		t.Fatalf("follow of a follower returned %d, want 409", code)
	}

	err = os.Remove(leaderFile)
	if err != nil {
		t.Fatal(err)
	}

	if code := post(); code != http.StatusOK {
		t.Fatalf("follow of a leader returned %d, want 200", code)
	}
}

// A follower that invites another speaker hands over its own leader's address and id, so
// the group stays one hop deep and the invited speaker can find that leader again
// after it is renumbered. An unknown leader id is left out, never replaced by the
// follower's own.
//
//nolint:paralleltest // it swaps the package-level leader check
func TestInviteFromFollowerPassesTheLeader(t *testing.T) {
	var body string

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)

		writeJSON(w, http.StatusOK, map[string]string{})
	}))
	defer other.Close()

	dir := t.TempDir()
	// A follower leads nothing: its anchor stays parked.
	srv := new(Server)
	srv.homeDir = dir
	srv.selfAddress = func(context.Context) string { return "10.0.0.1" }
	srv.wantAnchor = func() { t.Error("a follower's invite started its anchor") }
	invite := func() map[string]string {
		t.Helper()

		rec := httptest.NewRecorder()
		srv.postMultiroomInvite(
			rec,
			httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/invite",
				strings.NewReader(`{"ip":"`+strings.TrimPrefix(other.URL, "http://")+`"}`)),
		)

		var sent map[string]string
		if rec.Code != http.StatusOK || json.Unmarshal([]byte(body), &sent) != nil {
			t.Fatalf("invite %d, sent %q", rec.Code, body)
		}

		return sent
	}

	err := srv.setFollow("10.0.0.9", "leader-B")
	if err != nil {
		t.Fatal(err)
	}

	sent := invite()
	if sent["host"] != "10.0.0.9" || sent["id"] != "leader-B" {
		t.Fatalf("sent %v, want leader-B at 10.0.0.9", sent)
	}

	// The invited speaker stores that pair and finds leader-B, not this follower, at its
	// new address.
	follower := new(Server)
	follower.homeDir = t.TempDir()
	follower.log = quiet()

	err = follower.setFollow(sent["host"], sent["id"])
	if err != nil {
		t.Fatal(err)
	}

	defer func(old func(context.Context, string) bool) { leaderReachable = old }(leaderReachable)

	leaderReachable = func(_ context.Context, host string) bool { return host != "10.0.0.9" }

	follower.SetDevice(Device{
		ID:           "leader-B",
		IP:           "10.0.0.20",
		Name:         "",
		Model:        "",
		Firmware:     "",
		Port:         0,
		Departure:    false,
		Capabilities: nil,
	})
	follower.SetDevice(Device{
		ID:           "follower-A",
		IP:           "10.0.0.1",
		Name:         "",
		Model:        "",
		Firmware:     "",
		Port:         0,
		Departure:    false,
		Capabilities: nil,
	})
	follower.checkLeader(context.Background(), 0)

	if host, id := follower.Follow(); host != "10.0.0.20" || id != "leader-B" {
		t.Fatalf("invited speaker follows %s/%s, want leader-B at 10.0.0.20", host, id)
	}

	err = srv.setFollow("10.0.0.9", "")
	if err != nil {
		t.Fatal(err)
	}

	if sent := invite(); sent["host"] != "10.0.0.9" || sent["id"] != "" {
		t.Fatalf("sent %v, want the leader's address without an id", sent)
	}
}

// A WatchLeader check that was slow to decide never undoes what the user chose meanwhile:
// a new leader or leaving the group. The failure count starts over with a new leader.
//
//nolint:paralleltest // it swaps the package-level leader check
func TestStaleLeaderCheckLeavesANewFollowAlone(t *testing.T) {
	ctx := context.Background()

	defer func(old func(context.Context, string) bool) { leaderReachable = old }(leaderReachable)

	for _, change := range []struct{ name, host, id string }{
		{"new leader", "10.0.1.2", "leader-2"},
		{"left the group", "", ""},
	} {
		t.Run(change.name, func(t *testing.T) {
			var sources []string

			kicks := 0

			srv := new(Server)
			srv.homeDir = t.TempDir()
			srv.log = quiet()
			srv.restartSnapclient = func() { kicks++ }
			srv.selectSource = func(_ context.Context, source string) error {
				sources = append(sources, source)

				return nil
			}

			err := srv.setFollow("10.0.1.1", "leader-1")
			if err != nil {
				t.Fatal(err)
			}

			leaderReachable = func(context.Context, string) bool { return true }

			srv.checkLeader(ctx, 0) // binds the count to leader-1

			entered, release := make(chan struct{}), make(chan bool)
			leaderReachable = func(context.Context, string) bool {
				entered <- struct{}{}

				return <-release
			}

			done := make(chan int)
			go func() { done <- srv.checkLeader(ctx, multiroom.LeaderGiveUp-1) }()

			<-entered

			err = srv.setFollow(change.host, change.id)
			if err != nil {
				t.Fatal(err)
			}

			release <- false

			<-done

			if host, id := srv.Follow(); host != change.host || id != change.id {
				t.Fatalf("follow = %s/%s, want %s/%s", host, id, change.host, change.id)
			}

			if len(sources) != 0 || kicks != 0 {
				t.Fatalf("stale check acted: sources=%v kicks=%d", sources, kicks)
			}
		})
	}

	t.Run("count starts over", func(t *testing.T) {
		srv := new(Server)
		srv.homeDir = t.TempDir()
		srv.log = quiet()
		leaderReachable = func(context.Context, string) bool { return false }

		err := srv.setFollow("10.0.1.1", "leader-1")
		if err != nil {
			t.Fatal(err)
		}

		if fails := srv.checkLeader(ctx, 0); fails != 1 {
			t.Fatalf("fails = %d after one failed check, want 1", fails)
		}

		err = srv.setFollow("10.0.1.2", "leader-2")
		if err != nil {
			t.Fatal(err)
		}

		if fails := srv.checkLeader(ctx, multiroom.LeaderGiveUp-1); fails != 1 {
			t.Fatalf("fails = %d after a new leader, want 1", fails)
		}

		if host, _ := srv.Follow(); host != "10.0.1.2" {
			t.Fatal("left the new leader after one failed check")
		}
	})
}

// A follower finds a renumbered leader by its device id, and lets a vanished one go after
// multiroom.LeaderGiveUp failed checks instead of sitting silent on MULTIROOM.
//
//nolint:paralleltest // it swaps the package-level leader check
func TestFollowerFindsAMovedLeaderAndLeavesAGoneOne(t *testing.T) {
	dir := t.TempDir()
	kicks := 0

	var sources []string

	srv := new(Server)
	srv.homeDir = dir
	srv.log = quiet()
	srv.restartSnapclient = func() { kicks++ }
	srv.selectSource = func(_ context.Context, source string) error {
		sources = append(sources, source)

		return nil
	}
	reachable := map[string]bool{}

	defer func(old func(context.Context, string) bool) { leaderReachable = old }(leaderReachable)

	leaderReachable = func(_ context.Context, host string) bool { return reachable[host] }
	path := filepath.Join(dir, "multiroom-server")
	ctx := context.Background()

	err := srv.setFollow("10.0.1.10", "leader-1")
	if err != nil {
		t.Fatal(err)
	}

	reachable["10.0.1.10"] = true

	if f := srv.checkLeader(ctx, 3); f != 0 || kicks != 0 {
		t.Fatalf("reachable leader: fails=%d kicks=%d", f, kicks)
	}

	// Renumbered: the registry knows the id at a new address that answers.
	reachable["10.0.1.10"] = false

	srv.SetDevice(Device{
		ID:           "leader-1",
		IP:           "10.0.1.20",
		Name:         "",
		Model:        "",
		Firmware:     "",
		Port:         0,
		Departure:    false,
		Capabilities: nil,
	})

	reachable["10.0.1.20"] = true

	if f := srv.checkLeader(ctx, 0); f != 0 || kicks != 1 {
		t.Fatalf("moved leader: fails=%d kicks=%d", f, kicks)
	}

	if host, id := srv.Follow(); host != "10.0.1.20" || id != "leader-1" {
		t.Fatalf("following %s/%s, want leader-1 at the new address", host, id)
	}

	// Gone: count up, then leave.
	reachable["10.0.1.20"] = false

	fails := 0
	for range multiroom.LeaderGiveUp - 1 {
		fails = srv.checkLeader(ctx, fails)
	}

	if fails != multiroom.LeaderGiveUp-1 || len(sources) != 0 {
		t.Fatalf("left early: fails=%d sources=%v", fails, sources)
	}

	srv.checkLeader(ctx, fails)

	if len(sources) != 1 || sources[0] != "STANDBY" {
		t.Fatalf("sources = %v, want [STANDBY]", sources)
	}

	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Errorf("follow survived leaving: %v", err)
	}
}

// Whom a speaker follows is one write: a write cut short leaves the old leader's address
// and id or the new one's, never one leader's address with the other's id, which would
// later send the speaker back to the group it left. The id path is made a
// directory so a second write beside the address, as the speaker once did, fails right
// where a power cut would land.
func TestFollowIsOneWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	srv := new(Server)
	srv.homeDir = dir

	err := srv.setFollow("10.0.0.1", "leader-A")
	if err != nil {
		t.Fatal(err)
	}

	idPath := filepath.Join(dir, "multiroom-server.id")

	err = os.RemoveAll(idPath)
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(idPath, "x"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	_ = srv.setFollow("10.0.0.2", "leader-B")

	host, id := srv.Follow()
	if old, now := host == "10.0.0.1" && id == "leader-A", host == "10.0.0.2" && id == "leader-B"; !old && !now {
		t.Fatalf("follows %s/%s, a mix of two leaders", host, id)
	}
}

// The leader asks each follower how long its chain is and hands that to snapserver as
// the client's latency; a follower already within latencyStep is left alone.
//
//nolint:paralleltest // it points the package-level downstream port at a test server
func TestLeaderSyncsFollowerLatency(t *testing.T) {
	follower := new(Server)
	follower.log = slog.New(slog.DiscardHandler)
	follower.AttachForTest(
		func(h *Attached) { h.Audio.Downstream = func() time.Duration { return 412 * time.Millisecond } },
	)

	srv := httptest.NewServer(follower.Handler())
	defer srv.Close()
	defer func(p string) { downstreamPort = p }(downstreamPort)

	downstreamPort = srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	fake := &fakeMultiroom{
		statuses: nil,
		volumeID: "",
		volume:   snapcast.Volume{Percent: 0, Muted: false},
		groupID:  "",
		clients:  nil,
		latency:  nil,
	}
	leader := new(Server)
	leader.log = slog.New(slog.DiscardHandler)
	leader.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = fake, "local" })

	client := func(id string, connected bool) snapcast.ClientInfo {
		var c snapcast.ClientInfo

		c.ID, c.Connected, c.Host.IP = id, connected, "127.0.0.1"

		return c
	}
	status := snapcast.Status{Groups: []snapcast.Group{{ID: "g", Clients: []snapcast.ClientInfo{
		client("local", true), client("st10", true), client("gone", false),
	}}}}
	multiroom.SyncLatencies(context.Background(), leader.groupEnv(), status)

	if len(fake.latency) != 1 || fake.latency["st10"] != 412 {
		t.Fatalf("latencies set = %v, want only st10 at 412", fake.latency)
	}

	fake.latency = nil
	status.Groups[0].Clients[1].Config.Latency = 405
	multiroom.SyncLatencies(context.Background(), leader.groupEnv(), status)

	if len(fake.latency) != 0 {
		t.Fatalf("a follower 7 ms off was re-synced: %v", fake.latency)
	}
}

// A follower follows its leader into standby without leaving the group, and back out of
// it when the leader plays again -- also when it rebooted and so came up in standby
// . A leader that does not answer is left to WatchLeader.
//
//nolint:paralleltest // it swaps the package-level peer now-playing lookup
func TestFollowerFollowsTheLeadersPower(t *testing.T) {
	srv, fakeSpeaker, _ := radioServer(t)
	srv.homeDir = t.TempDir()

	var sources []string

	srv.selectSource = func(ctx context.Context, source string) error {
		sources = append(sources, source)

		return fakeSpeaker.SetSource(ctx, source)
	}
	leader := struct{ on, ok bool }{true, true}

	defer func(old func(context.Context, string) (speaker.NowPlaying, bool)) { peerNow = old }(peerNow)

	peerNow = func(context.Context, string) (speaker.NowPlaying, bool) {
		if leader.on {
			return speaker.NowPlaying{
				Source:     "TUNEIN",
				Track:      "",
				Artist:     "",
				Album:      "",
				Station:    "",
				StationID:  "",
				Location:   "",
				Art:        "",
				PlayStatus: "",
				CanSkip:    false,
				Failed:     false,
				Unheard:    false,
			}, leader.ok
		}

		return speaker.NowPlaying{
			Source:     "STANDBY",
			Track:      "",
			Artist:     "",
			Album:      "",
			Station:    "",
			StationID:  "",
			Location:   "",
			Art:        "",
			PlayStatus: "",
			CanSkip:    false,
			Failed:     false,
			Unheard:    false,
		}, leader.ok
	}
	ctx := context.Background()

	srv.syncLeaderPower(ctx)

	if len(sources) != 0 {
		t.Fatalf("not following, yet sources = %v", sources)
	}

	err := srv.setFollow("10.0.1.10", "leader-1")
	if err != nil {
		t.Fatal(err)
	}
	// Came up in standby while the leader plays: join.
	srv.syncLeaderPower(ctx)

	if len(sources) != 1 || sources[0] != "MULTIROOM" {
		t.Fatalf("sources = %v, want [MULTIROOM]", sources)
	}

	srv.syncLeaderPower(ctx)

	if len(sources) != 1 {
		t.Fatalf("joined twice: %v", sources)
	}
	// The leader goes to standby: so does the follower, marked as the group's own.
	leader.on = false

	srv.syncLeaderPower(ctx)

	if len(sources) != 2 || sources[1] != "STANDBY" || !srv.KeepFollow() || srv.KeepFollow() {
		t.Fatalf("sources = %v; keepFollow must be set once", sources)
	}

	if srv.followedHost() != "10.0.1.10" {
		t.Fatal("standby with the leader dropped the follow")
	}
	// An unreachable leader changes nothing here.
	leader.on, leader.ok = true, false

	srv.syncLeaderPower(ctx)

	if len(sources) != 2 {
		t.Fatalf("acted on an unreachable leader: %v", sources)
	}

	leader.ok = true

	srv.syncLeaderPower(ctx)

	if len(sources) != 3 || sources[2] != "MULTIROOM" {
		t.Fatalf("sources = %v, want MULTIROOM again", sources)
	}
}

// powerBackend is a speaker that is in standby or not.
type powerBackend struct {
	Backend

	standby atomic.Bool
}

func (b *powerBackend) NowPlaying(context.Context) (*speaker.NowPlaying, error) {
	if b.standby.Load() {
		return &speaker.NowPlaying{
			Source:     standbySource,
			Track:      "",
			Artist:     "",
			Album:      "",
			Station:    "",
			StationID:  "",
			Location:   "",
			Art:        "",
			PlayStatus: "",
			CanSkip:    false,
			Failed:     false,
			Unheard:    false,
		}, nil
	}

	return &speaker.NowPlaying{
		Source:     "TUNEIN",
		Track:      "",
		Artist:     "",
		Album:      "",
		Station:    "",
		StationID:  "",
		Location:   "",
		Art:        "",
		PlayStatus: "",
		CanSkip:    false,
		Failed:     false,
		Unheard:    false,
	}, nil
}

// countingMultiroom counts the snapserver status requests.
type countingMultiroom struct {
	fakeMultiroom

	asked atomic.Int32
}

func (c *countingMultiroom) GetStatus(ctx context.Context) (snapcast.Status, error) {
	c.asked.Add(1)

	return c.fakeMultiroom.GetStatus(ctx)
}

// In standby WatchMultiroom asks snapserver every multiroomStandbyEvery-th tick, and at
// once again after waking.
func TestWatchMultiroomSlowsDownInStandby(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		backend := &powerBackend{Backend: nil, standby: atomic.Bool{}}
		backend.standby.Store(true)

		fake := &countingMultiroom{
			fakeMultiroom: fakeMultiroom{
				statuses: []snapcast.Status{{Groups: nil}},
				volumeID: "",
				volume:   snapcast.Volume{Percent: 0, Muted: false},
				groupID:  "",
				clients:  nil,
				latency:  nil,
			},
			asked: atomic.Int32{},
		}
		srv := new(Server)
		srv.backend = backend
		srv.log = slog.New(slog.DiscardHandler)
		srv.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = fake, "local" })
		srv.AttachForTest(func(h *Attached) { h.Audio.MultiroomActive = func(bool) {} })

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go srv.WatchMultiroom(ctx, 3*time.Second)

		time.Sleep(33*time.Second + time.Millisecond) // eleven ticks, 0 to 10
		synctest.Wait()

		if got := fake.asked.Load(); got != 3 {
			t.Fatalf("standby: %d status requests in eleven ticks, want 3", got)
		}

		backend.standby.Store(false)
		time.Sleep(3 * time.Second)
		synctest.Wait()

		if got := fake.asked.Load(); got != 4 {
			t.Fatalf("awake: %d status requests, want 4 (asked on the first tick)", got)
		}

		cancel()
	})
}

// A speaker refuses to follow itself: its own snapserver always answers, so the
// leader-gone watchdog would never let it go.
//
//nolint:paralleltest // it swaps the package-level peer address check
func TestFollowRefusesThisSpeakersOwnAddress(t *testing.T) {
	defer func(f func(net.IP) bool) { peerIPAllowed = f }(peerIPAllowed)

	peerIPAllowed = privatePeerIP
	own := ""

	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
				own = n.IP.String()

				break
			}
		}
	}

	for _, host := range []string{"127.0.0.1", "localhost", "169.254.1.1", own} {
		if host == "" {
			continue
		}

		s := new(Server)
		s.homeDir = t.TempDir()
		w := httptest.NewRecorder()
		s.postMultiroomFollow(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow",
			strings.NewReader(`{"host":"`+host+`"}`)))

		if w.Code != http.StatusBadRequest {
			t.Errorf("follow %q returned %d, want 400", host, w.Code)
		}
	}

	if !followTargetAllowed("192.168.1.10") || !followTargetAllowed("keuken.local") {
		t.Error("another speaker's address was refused")
	}
}

// Two speakers asked to follow each other at once both see "follows nobody" on the first
// look. The look after the write must catch the other's follow, or A->B->A is persisted.
//
//nolint:paralleltest // it swaps the package-level leader lookup
func TestConcurrentReciprocalFollowDoesNotLoop(t *testing.T) {
	first, second := new(Server), new(Server)
	first.homeDir, second.homeDir = t.TempDir(), t.TempDir()

	defer func(old func(context.Context, string) string) { peerLeader = old }(peerLeader)

	var (
		looks      atomic.Int32
		bothLooked sync.WaitGroup
	)
	bothLooked.Add(2)

	peerLeader = func(_ context.Context, host string) string {
		peer := first
		if host == "10.0.1.201" {
			peer = second
		}

		got := peer.followedHost()
		// The first look of each handler waits for the other's, so both read before either writes.
		if looks.Add(1) <= 2 {
			bothLooked.Done()
			bothLooked.Wait()
		}

		return got
	}

	var wg sync.WaitGroup
	for _, testCase := range []struct {
		s    *Server
		host string
	}{{first, "10.0.1.201"}, {second, "10.0.1.200"}} {
		wg.Go(func() {
			testCase.s.postMultiroomFollow(
				httptest.NewRecorder(),
				httptest.NewRequestWithContext(t.Context(), http.MethodPost,
					"/api/multiroom/follow", strings.NewReader(`{"host":"`+testCase.host+`"}`)),
			)
		})
	}

	wg.Wait()

	if first.followedHost() != "" && second.followedHost() != "" {
		t.Errorf("A follows %q and B follows %q: a loop", first.followedHost(), second.followedHost())
	}
}

// groupFake is a snapcast whose group really changes: SetClients is slow, so two
// overlapping membership PUTs both read the group before either writes it.
type groupFake struct {
	fakeMultiroom

	mu      sync.Mutex
	members []string
}

func (f *groupFake) GetStatus(context.Context) (snapcast.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var status snapcast.Status

	status.Groups = []snapcast.Group{{ID: "g", Clients: nil}, {ID: "idle", Clients: nil}}

	for _, id := range []string{"local", "a", "b"} {
		var client snapcast.ClientInfo

		client.ID, client.Connected = id, true
		if slices.Contains(f.members, id) {
			status.Groups[0].Clients = append(status.Groups[0].Clients, client)
		} else {
			status.Groups[1].Clients = append(status.Groups[1].Clients, client)
		}
	}

	return status, nil
}

func (f *groupFake) SetClients(_ context.Context, _ string, clients []string) error {
	time.Sleep(30 * time.Millisecond)
	f.mu.Lock()

	f.members = append([]string(nil), clients...)
	f.mu.Unlock()

	return nil
}

func TestMultiroomOverlappingMembershipKeepsBothRooms(t *testing.T) {
	t.Parallel()

	fake := &groupFake{
		members: []string{"local"},
		fakeMultiroom: fakeMultiroom{
			statuses: nil,
			volumeID: "",
			volume:   snapcast.Volume{Percent: 0, Muted: false},
			groupID:  "",
			clients:  nil,
			latency:  nil,
		},
		mu: sync.Mutex{},
	}
	srv := new(Server)
	srv.log = slog.New(slog.DiscardHandler)
	srv.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = fake, "local" })
	h := srv.Handler()

	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Go(func() {
			multiroomRequest(t, h, http.MethodPut, "/api/multiroom/clients/"+id+"/membership", `{"member":true}`)
		})
	}

	wg.Wait()

	if len(fake.members) != 3 {
		t.Fatalf("group = %v, want local, a and b", fake.members)
	}
}

// The arbiter's leave runs after the source change that caused it. A follow written in
// between, to another leader or to the same one with MULTIROOM picked again, is the
// speaker's new group and survives the late leave; with nothing in between it ends.
func TestLateLeaveKeepsNewerFollow(t *testing.T) {
	t.Parallel()

	srv, fakeSpeaker, _ := radioServer(t)
	kicks := 0

	srv.restartSnapclient = func() { kicks++ }

	err := srv.setFollow("10.0.1.10", "leader-1")
	if err != nil {
		t.Fatal(err)
	}

	host, leaderID := srv.Follow() // what the arbiter reads when the source leaves MULTIROOM

	playStation(t, fakeSpeaker)

	err = srv.setFollow("10.0.1.11", "leader-2")
	if err != nil {
		t.Fatal(err)
	}

	srv.LeaveFollow(host, leaderID)

	if h, i := srv.Follow(); h != "10.0.1.11" || i != "leader-2" || kicks != 0 {
		t.Fatalf("follow = %q %q, kicks %d; the newer follow must survive", h, i, kicks)
	}

	host, leaderID = srv.Follow()

	err = fakeSpeaker.Select(context.Background(), sourceMultiroom, "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	srv.LeaveFollow(host, leaderID)

	if h, _ := srv.Follow(); h != "10.0.1.11" || kicks != 0 {
		t.Fatalf("follow = %q, kicks %d; back on MULTIROOM, the follow must stay", h, kicks)
	}

	playStation(t, fakeSpeaker)
	srv.LeaveFollow(host, leaderID)

	if h, _ := srv.Follow(); h != "" || kicks != 1 {
		t.Fatalf("follow = %q, kicks %d; a leave with nothing newer must end the follow", h, kicks)
	}
}

// A host name that resolves to loopback passes the name check, so the dial itself refuses
// it: the invite is a POST from this process and would pass every loopbackOnly check.
//
//nolint:paralleltest // it swaps the package-level peer address check
func TestPeerDialRefusesLoopbackAliases(t *testing.T) {
	defer func(f func(net.IP) bool) { peerIPAllowed = f }(peerIPAllowed)

	peerIPAllowed = privatePeerIP

	for _, target := range []string{"localhost.", "127.0.0.1:80", "LOCALHOST.:8090"} {
		req, _ := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"http://"+target+"/api/multiroom/follow",
			http.NoBody,
		)

		resp, err := inviteClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}

		if !errors.Is(err, errPeerAddress) {
			t.Errorf("%s: err %v, want errPeerAddress", target, err)
		}
	}
}

// A follow puts the follower back in the leader's group, unmuted. snapserver remembers a
// client taken out of the group (membership false) in a group of its own and muted, and
// an invite used to leave it there, playing silence.
//
//nolint:paralleltest // it points the package-level downstream port at a test server
func TestFollowJoinsTheLeadersGroup(t *testing.T) {
	defer func(old func(context.Context, string) string) { peerLeader = old }(peerLeader)

	peerLeader = func(context.Context, string) string { return "" }
	status := snapcast.Status{Groups: []snapcast.Group{
		{ID: "lead-group", Clients: nil},
		{ID: "own-group", Clients: nil},
	}}
	client := func(id string, muted bool) snapcast.ClientInfo {
		var c snapcast.ClientInfo

		c.ID, c.Connected = id, true
		c.Config.Volume = snapcast.Volume{Percent: 40, Muted: muted}

		return c
	}
	status.Groups[0].Clients = []snapcast.ClientInfo{client("soundtap-LEAD", true)}
	status.Groups[1].Clients = []snapcast.ClientInfo{client("soundtap-WLA", true)}
	snap := &fakeMultiroom{
		statuses: []snapcast.Status{status},
		volumeID: "",
		volume:   snapcast.Volume{Percent: 0, Muted: false},
		groupID:  "",
		clients:  nil,
		latency:  nil,
	}
	leader := new(Server)
	leader.homeDir = t.TempDir()
	leader.log = slog.New(slog.DiscardHandler)
	leader.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = snap, "soundtap-LEAD" })

	srv := httptest.NewServer(leader.Handler())
	defer srv.Close()
	defer func(p string) { downstreamPort = p }(downstreamPort)

	downstreamPort = srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	follower := new(Server)
	follower.homeDir = t.TempDir()
	follower.log = slog.New(slog.DiscardHandler)
	follower.ReconfigureForTest(func(o *Options) {
		o.Multiroom.Backend, o.Multiroom.LocalID = &fakeMultiroom{
			statuses: []snapcast.Status{{Groups: []snapcast.Group{
				{ID: "g", Clients: []snapcast.ClientInfo{client("soundtap-WLA", false)}},
			}}},
			volumeID: "", volume: snapcast.Volume{Percent: 0, Muted: false}, groupID: "", clients: nil, latency: nil,
		}, "soundtap-WLA"
	})

	rec := httptest.NewRecorder()
	follower.postMultiroomFollow(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow",
			strings.NewReader(`{"host":"127.0.0.1"}`)),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("follow = %d %s", rec.Code, rec.Body)
	}

	follower.group.WaitPeers()

	if snap.groupID != "lead-group" || !slices.Equal(snap.clients, []string{"soundtap-LEAD", "soundtap-WLA"}) {
		t.Fatalf("leader's group %q = %v, want the follower in lead-group", snap.groupID, snap.clients)
	}

	if snap.volumeID != "soundtap-WLA" || snap.volume.Muted {
		t.Fatalf("follower volume %q %+v, want it unmuted", snap.volumeID, snap.volume)
	}
}

// A follower that reconnects on its own asks the leader's stream port open for its address.
func TestListenAllowsTheCallerAddress(t *testing.T) { //nolint:paralleltest // it swaps the package-level follower hook
	var got string

	orig := allowFollower

	t.Cleanup(func() { allowFollower = orig })

	allowFollower = func(_ context.Context, host string) { got = host }
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/listen", nil)
	r.RemoteAddr = "192.168.1.9:51000"
	w := httptest.NewRecorder()
	new(Server).postMultiroomListen(w, r)

	if w.Code != http.StatusNoContent || got != "192.168.1.9" {
		t.Fatalf("status %d, allowed %q", w.Code, got)
	}
}
