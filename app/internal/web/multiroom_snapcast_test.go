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
	status := snapcast.Status{Groups: []snapcast.Group{{ID: "local-group"}, {ID: "other-group"}}}
	client := func(id, configName, hostName, ip string, connected bool, percent int, muted bool) snapcast.ClientInfo {
		var c snapcast.ClientInfo
		c.ID, c.Connected = id, connected
		c.Config.Name, c.Host.Name, c.Host.IP = configName, hostName, ip
		c.Config.Volume = snapcast.Volume{Percent: percent, Muted: muted}
		return c
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
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = fake, "local" })
	return s.Handler()
}

func multiroomRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A client that dropped out is reported as offline rather than dropped from the
// list: a speaker that silently vanishes reads as a bug in the app.
func TestMultiroomListsConnectedClients(t *testing.T) {
	h := multiroomHandler(&fakeMultiroom{statuses: []snapcast.Status{multiroomStatus()}})
	rec := multiroomRequest(t, h, http.MethodGet, "/api/multiroom", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}
	var rows []multiroom.Client
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
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
	fake := &fakeMultiroom{statuses: []snapcast.Status{multiroomStatus(), multiroomStatus()}}
	h := multiroomHandler(fake)
	rec := multiroomRequest(t, h, http.MethodPut, "/api/multiroom/clients/peer/volume", `{"percent":61,"muted":true}`)
	if rec.Code != http.StatusOK || fake.volumeID != "peer" || fake.volume != (snapcast.Volume{Percent: 61, Muted: true}) {
		t.Fatalf("volume = %d id=%q value=%+v", rec.Code, fake.volumeID, fake.volume)
	}
	rec = multiroomRequest(t, h, http.MethodPut, "/api/multiroom/clients/peer/membership", `{"member":false}`)
	if rec.Code != http.StatusOK || fake.groupID != "local-group" || len(fake.clients) != 2 || fake.clients[0] != "local" || fake.clients[1] != "offline" || fake.volumeID != "peer" || fake.volume != (snapcast.Volume{Percent: 30, Muted: true}) {
		t.Fatalf("membership = %d group=%q clients=%v", rec.Code, fake.groupID, fake.clients)
	}
}

func TestMultiroomValidation(t *testing.T) {
	for _, tc := range []struct {
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
		h := multiroomHandler(&fakeMultiroom{statuses: []snapcast.Status{multiroomStatus()}})
		if rec := multiroomRequest(t, h, http.MethodPut, tc.path, tc.body); rec.Code != tc.want {
			t.Errorf("PUT %s = %d, want %d: %s", tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// Latency and "last seen" are the two numbers that explain a group that is out of
// sync or short a speaker, so they have to survive the mapping to JSON.
func TestMultiroomReportsLatencyAndLastSeen(t *testing.T) {
	status := multiroomStatus()
	status.Groups[0].Clients[1].Config.Latency = 120
	status.Groups[0].Clients[2].LastSeen.Sec = time.Now().Unix() - 90

	h := multiroomHandler(&fakeMultiroom{statuses: []snapcast.Status{status}})
	var rows []multiroom.Client
	if err := json.Unmarshal(multiroomRequest(t, h, http.MethodGet, "/api/multiroom", "").Body.Bytes(), &rows); err != nil {
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
func TestFollowAnotherSpeakerWritesTheSettingAndKicksTheClient(t *testing.T) {
	defer func(old func(context.Context, string) string) { peerLeader = old }(peerLeader)
	peerLeader = func(context.Context, string) string { return "" }
	dir := t.TempDir()
	kicks := 0
	sources := []string{}
	s := &Server{
		homeDir:           dir,
		restartSnapclient: func() { kicks++ },
		selectSource: func(_ context.Context, source string) error {
			sources = append(sources, source)
			return nil
		},
	}
	path := filepath.Join(dir, "multiroom-server")

	post := func(body string) int {
		w := httptest.NewRecorder()
		s.postMultiroomFollow(w, httptest.NewRequest(http.MethodPost, "/api/multiroom/follow",
			strings.NewReader(body)))
		return w.Code
	}

	if code := post(`{"host":"10.0.1.126"}`); code != http.StatusOK {
		t.Fatalf("follow returned %d", code)
	}
	if got, err := os.ReadFile(path); err != nil || strings.TrimSpace(string(got)) != "10.0.1.126" {
		t.Fatalf("setting = %q, %v", got, err)
	}
	if kicks != 1 {
		t.Errorf("snapclient kicked %d times, want 1", kicks)
	}

	if code := post(`{"host":""}`); code != http.StatusOK {
		t.Fatalf("unfollow returned %d", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("setting survived unfollow: %v", err)
	}
	if len(sources) != 2 || sources[0] != "MULTIROOM" || sources[1] != "STANDBY" {
		t.Errorf("sources = %v, want [MULTIROOM STANDBY]", sources)
	}

	// A host that is not a host never reaches the file, and never kicks anything.
	for _, bad := range []string{`{"host":"192.168.1.1 --player alsa"}`, `{"host":"$(reboot)"}`,
		`{"host":"a;b"}`, `{"host":"-x"}`} {
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
func TestFollowSwitchesTheSourceBeforeTheStream(t *testing.T) {
	defer func(old func(context.Context, string) string) { peerLeader = old }(peerLeader)
	peerLeader = func(context.Context, string) string { return "" }
	var steps []string
	s := &Server{
		homeDir:           t.TempDir(),
		sourceChanging:    func(source string) { steps = append(steps, "stop old for "+source) },
		restartSnapclient: func() { steps = append(steps, "stream") },
		selectSource: func(_ context.Context, source string) error {
			steps = append(steps, "select "+source)
			return nil
		},
	}
	w := httptest.NewRecorder()
	s.postMultiroomFollow(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow",
		strings.NewReader(`{"host":"10.0.1.126"}`)))
	want := []string{"stop old for MULTIROOM", "select MULTIROOM", "stream"}
	if w.Code != http.StatusOK || !slices.Equal(steps, want) {
		t.Fatalf("follow = %d, steps %q, want %q", w.Code, steps, want)
	}
}

// Inviting is the same handover seen from the other side: the speaker the user is looking
// at calls the other one, because the browser may not.
func TestInviteAnotherSpeakerCallsItWithThisSpeakersAddress(t *testing.T) {
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
	s := &Server{selfAddress: func(context.Context) string { return "10.0.0.1" }, wantAnchor: func() { anchors++ }}
	w := httptest.NewRecorder()
	s.postMultiroomInvite(w, httptest.NewRequest(http.MethodPost, "/api/multiroom/invite",
		strings.NewReader(`{"ip":"`+host+`"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("invite returned %d: %s", w.Code, w.Body.String())
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

	w = httptest.NewRecorder()
	s.postMultiroomInvite(w, httptest.NewRequest(http.MethodPost, "/api/multiroom/invite",
		strings.NewReader(`{"ip":"1.2.3.4 --player alsa"}`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("a host with a second word returned %d, want 400", w.Code)
	}
}

// A speaker others listen to refuses to follow (no loops, no chains); leaving is always
// allowed.
func TestFollowRefusedWhileLeadingListeners(t *testing.T) {
	leader := snapcast.ClientInfo{ID: "soundtap-self", Connected: true}
	listener := snapcast.ClientInfo{ID: "soundtap-other", Connected: true}
	s := &Server{
		homeDir:      t.TempDir(),
		snapclientID: leader.ID,
		multiroom: &fakeMultiroom{statuses: []snapcast.Status{{Groups: []snapcast.Group{
			{ID: "g", Clients: []snapcast.ClientInfo{leader, listener}},
		}}}},
	}
	post := func(body string) int {
		w := httptest.NewRecorder()
		s.postMultiroomFollow(w, httptest.NewRequest(http.MethodPost, "/api/multiroom/follow",
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
func TestFollowRefusedWhenTheTargetFollows(t *testing.T) {
	targetDir := t.TempDir()
	target := &Server{homeDir: targetDir, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(target.Handler())
	defer srv.Close()
	defer func(p string) { downstreamPort = p }(downstreamPort)
	downstreamPort = srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	s := &Server{homeDir: t.TempDir()}
	post := func() int {
		w := httptest.NewRecorder()
		s.postMultiroomFollow(w, httptest.NewRequest(http.MethodPost, "/api/multiroom/follow",
			strings.NewReader(`{"host":"127.0.0.1"}`)))
		return w.Code
	}
	leaderFile := filepath.Join(targetDir, "multiroom-server")
	if err := os.WriteFile(leaderFile, []byte("10.0.0.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := post(); code != http.StatusConflict {
		t.Fatalf("follow of a follower returned %d, want 409", code)
	}
	if err := os.Remove(leaderFile); err != nil {
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
	s := &Server{homeDir: dir, selfAddress: func(context.Context) string { return "10.0.0.1" },
		wantAnchor: func() { t.Error("a follower's invite started its anchor") }}
	invite := func() map[string]string {
		t.Helper()
		w := httptest.NewRecorder()
		s.postMultiroomInvite(w, httptest.NewRequest(http.MethodPost, "/api/multiroom/invite",
			strings.NewReader(`{"ip":"`+strings.TrimPrefix(other.URL, "http://")+`"}`)))
		var sent map[string]string
		if w.Code != http.StatusOK || json.Unmarshal([]byte(body), &sent) != nil {
			t.Fatalf("invite %d, sent %q", w.Code, body)
		}
		return sent
	}

	if err := s.setFollow("10.0.0.9", "leader-B"); err != nil {
		t.Fatal(err)
	}
	sent := invite()
	if sent["host"] != "10.0.0.9" || sent["id"] != "leader-B" {
		t.Fatalf("sent %v, want leader-B at 10.0.0.9", sent)
	}

	// The invited speaker stores that pair and finds leader-B, not this follower, at its
	// new address.
	c := &Server{homeDir: t.TempDir(), log: quiet()}
	if err := c.setFollow(sent["host"], sent["id"]); err != nil {
		t.Fatal(err)
	}
	defer func(old func(context.Context, string) bool) { leaderReachable = old }(leaderReachable)
	leaderReachable = func(_ context.Context, host string) bool { return host != "10.0.0.9" }
	c.SetDevice(Device{ID: "leader-B", IP: "10.0.0.20"})
	c.SetDevice(Device{ID: "follower-A", IP: "10.0.0.1"})
	c.checkLeader(context.Background(), 0)
	if host, id := c.follow(); host != "10.0.0.20" || id != "leader-B" {
		t.Fatalf("invited speaker follows %s/%s, want leader-B at 10.0.0.20", host, id)
	}

	if err := s.setFollow("10.0.0.9", ""); err != nil {
		t.Fatal(err)
	}
	if sent := invite(); sent["host"] != "10.0.0.9" || sent["id"] != "" {
		t.Fatalf("sent %v, want the leader's address without an id", sent)
	}
}

// A WatchLeader check that was slow to decide never undoes what the user chose meanwhile:
// a new leader or leaving the group. The failure count starts over with a new leader.
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
			s := &Server{
				homeDir: t.TempDir(), log: quiet(),
				restartSnapclient: func() { kicks++ },
				selectSource: func(_ context.Context, source string) error {
					sources = append(sources, source)
					return nil
				},
			}
			if err := s.setFollow("10.0.1.1", "leader-1"); err != nil {
				t.Fatal(err)
			}
			leaderReachable = func(context.Context, string) bool { return true }
			s.checkLeader(ctx, 0) // binds the count to leader-1
			entered, release := make(chan struct{}), make(chan bool)
			leaderReachable = func(context.Context, string) bool {
				entered <- struct{}{}
				return <-release
			}
			done := make(chan int)
			go func() { done <- s.checkLeader(ctx, multiroom.LeaderGiveUp-1) }()
			<-entered
			if err := s.setFollow(change.host, change.id); err != nil {
				t.Fatal(err)
			}
			release <- false
			<-done
			if host, id := s.follow(); host != change.host || id != change.id {
				t.Fatalf("follow = %s/%s, want %s/%s", host, id, change.host, change.id)
			}
			if len(sources) != 0 || kicks != 0 {
				t.Fatalf("stale check acted: sources=%v kicks=%d", sources, kicks)
			}
		})
	}

	t.Run("count starts over", func(t *testing.T) {
		s := &Server{homeDir: t.TempDir(), log: quiet()}
		leaderReachable = func(context.Context, string) bool { return false }
		if err := s.setFollow("10.0.1.1", "leader-1"); err != nil {
			t.Fatal(err)
		}
		if fails := s.checkLeader(ctx, 0); fails != 1 {
			t.Fatalf("fails = %d after one failed check, want 1", fails)
		}
		if err := s.setFollow("10.0.1.2", "leader-2"); err != nil {
			t.Fatal(err)
		}
		if fails := s.checkLeader(ctx, multiroom.LeaderGiveUp-1); fails != 1 {
			t.Fatalf("fails = %d after a new leader, want 1", fails)
		}
		if host, _ := s.follow(); host != "10.0.1.2" {
			t.Fatalf("left the new leader after one failed check")
		}
	})
}

// A follower finds a renumbered leader by its device id, and lets a vanished one go after
// multiroom.LeaderGiveUp failed checks instead of sitting silent on MULTIROOM.
func TestFollowerFindsAMovedLeaderAndLeavesAGoneOne(t *testing.T) {
	dir := t.TempDir()
	kicks := 0
	var sources []string
	s := &Server{
		homeDir:           dir,
		log:               quiet(),
		restartSnapclient: func() { kicks++ },
		selectSource: func(_ context.Context, source string) error {
			sources = append(sources, source)
			return nil
		},
	}
	up := map[string]bool{}
	defer func(old func(context.Context, string) bool) { leaderReachable = old }(leaderReachable)
	leaderReachable = func(_ context.Context, host string) bool { return up[host] }
	path := filepath.Join(dir, "multiroom-server")
	ctx := context.Background()

	if err := s.setFollow("10.0.1.10", "leader-1"); err != nil {
		t.Fatal(err)
	}
	up["10.0.1.10"] = true
	if f := s.checkLeader(ctx, 3); f != 0 || kicks != 0 {
		t.Fatalf("reachable leader: fails=%d kicks=%d", f, kicks)
	}

	// Renumbered: the registry knows the id at a new address that answers.
	up["10.0.1.10"] = false
	s.SetDevice(Device{ID: "leader-1", IP: "10.0.1.20"})
	up["10.0.1.20"] = true
	if f := s.checkLeader(ctx, 0); f != 0 || kicks != 1 {
		t.Fatalf("moved leader: fails=%d kicks=%d", f, kicks)
	}
	if host, id := s.follow(); host != "10.0.1.20" || id != "leader-1" {
		t.Fatalf("following %s/%s, want leader-1 at the new address", host, id)
	}

	// Gone: count up, then leave.
	up["10.0.1.20"] = false
	fails := 0
	for range multiroom.LeaderGiveUp - 1 {
		fails = s.checkLeader(ctx, fails)
	}
	if fails != multiroom.LeaderGiveUp-1 || len(sources) != 0 {
		t.Fatalf("left early: fails=%d sources=%v", fails, sources)
	}
	if s.checkLeader(ctx, fails); len(sources) != 1 || sources[0] != "STANDBY" {
		t.Fatalf("sources = %v, want [STANDBY]", sources)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("follow survived leaving: %v", err)
	}
}

// Whom a speaker follows is one write: a write cut short leaves the old leader's address
// and id or the new one's, never one leader's address with the other's id, which would
// later send the speaker back to the group it left. The id path is made a
// directory so a second write beside the address, as the speaker once did, fails right
// where a power cut would land.
func TestFollowIsOneWrite(t *testing.T) {
	dir := t.TempDir()
	s := &Server{homeDir: dir}
	if err := s.setFollow("10.0.0.1", "leader-A"); err != nil {
		t.Fatal(err)
	}
	idPath := filepath.Join(dir, "multiroom-server.id")
	if err := os.RemoveAll(idPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(idPath, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = s.setFollow("10.0.0.2", "leader-B")
	host, id := s.follow()
	if old, now := host == "10.0.0.1" && id == "leader-A", host == "10.0.0.2" && id == "leader-B"; !old && !now {
		t.Fatalf("follows %s/%s, a mix of two leaders", host, id)
	}
}

// The leader asks each follower how long its chain is and hands that to snapserver as
// the client's latency; a follower already within latencyStep is left alone.
func TestLeaderSyncsFollowerLatency(t *testing.T) {
	follower := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	follower.AttachForTest(func(h *Attached) { h.Audio.Downstream = func() time.Duration { return 412 * time.Millisecond } })
	srv := httptest.NewServer(follower.Handler())
	defer srv.Close()
	defer func(p string) { downstreamPort = p }(downstreamPort)
	downstreamPort = srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	fake := &fakeMultiroom{}
	leader := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
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
func TestFollowerFollowsTheLeadersPower(t *testing.T) {
	s, sp, _ := radioServer(t)
	s.homeDir = t.TempDir()
	var sources []string
	s.selectSource = func(ctx context.Context, source string) error {
		sources = append(sources, source)
		return sp.SetSource(ctx, source)
	}
	leader := struct{ on, ok bool }{true, true}
	defer func(old func(context.Context, string) (speaker.NowPlaying, bool)) { peerNow = old }(peerNow)
	peerNow = func(context.Context, string) (speaker.NowPlaying, bool) {
		if leader.on {
			return speaker.NowPlaying{Source: "TUNEIN"}, leader.ok
		}
		return speaker.NowPlaying{Source: "STANDBY"}, leader.ok
	}
	ctx := context.Background()

	s.syncLeaderPower(ctx)
	if len(sources) != 0 {
		t.Fatalf("not following, yet sources = %v", sources)
	}
	if err := s.setFollow("10.0.1.10", "leader-1"); err != nil {
		t.Fatal(err)
	}
	// Came up in standby while the leader plays: join.
	s.syncLeaderPower(ctx)
	if len(sources) != 1 || sources[0] != "MULTIROOM" {
		t.Fatalf("sources = %v, want [MULTIROOM]", sources)
	}
	s.syncLeaderPower(ctx)
	if len(sources) != 1 {
		t.Fatalf("joined twice: %v", sources)
	}
	// The leader goes to standby: so does the follower, marked as the group's own.
	leader.on = false
	s.syncLeaderPower(ctx)
	if len(sources) != 2 || sources[1] != "STANDBY" || !s.KeepFollow() || s.KeepFollow() {
		t.Fatalf("sources = %v; keepFollow must be set once", sources)
	}
	if s.followedHost() != "10.0.1.10" {
		t.Fatal("standby with the leader dropped the follow")
	}
	// An unreachable leader changes nothing here.
	leader.on, leader.ok = true, false
	s.syncLeaderPower(ctx)
	if len(sources) != 2 {
		t.Fatalf("acted on an unreachable leader: %v", sources)
	}
	leader.ok = true
	s.syncLeaderPower(ctx)
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
		return &speaker.NowPlaying{Source: standbySource}, nil
	}
	return &speaker.NowPlaying{Source: "TUNEIN"}, nil
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
	synctest.Test(t, func(t *testing.T) {
		backend := &powerBackend{}
		backend.standby.Store(true)
		fake := &countingMultiroom{fakeMultiroom: fakeMultiroom{statuses: []snapcast.Status{{}}}}
		s := &Server{backend: backend, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		s.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = fake, "local" })
		s.AttachForTest(func(h *Attached) { h.Audio.MultiroomActive = func(bool) {} })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go s.WatchMultiroom(ctx, 3*time.Second)

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
func TestFollowRefusesThisSpeakersOwnAddress(t *testing.T) {
	defer func(f func(net.IP) bool) { peerIPAllowed = f }(peerIPAllowed)
	peerIPAllowed = strictPeerIP
	own := ""
	if addrs, err := net.InterfaceAddrs(); err == nil {
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
		s := &Server{homeDir: t.TempDir()}
		w := httptest.NewRecorder()
		s.postMultiroomFollow(w, httptest.NewRequest(http.MethodPost, "/api/multiroom/follow",
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
func TestConcurrentReciprocalFollowDoesNotLoop(t *testing.T) {
	a, b := &Server{homeDir: t.TempDir()}, &Server{homeDir: t.TempDir()}
	defer func(old func(context.Context, string) string) { peerLeader = old }(peerLeader)
	var looks atomic.Int32
	var bothLooked sync.WaitGroup
	bothLooked.Add(2)
	peerLeader = func(_ context.Context, host string) string {
		peer := a
		if host == "10.0.1.201" {
			peer = b
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
	for _, c := range []struct {
		s    *Server
		host string
	}{{a, "10.0.1.201"}, {b, "10.0.1.200"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.s.postMultiroomFollow(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/api/multiroom/follow", strings.NewReader(`{"host":"`+c.host+`"}`)))
		}()
	}
	wg.Wait()
	if a.followedHost() != "" && b.followedHost() != "" {
		t.Errorf("A follows %q and B follows %q: a loop", a.followedHost(), b.followedHost())
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
	var st snapcast.Status
	st.Groups = []snapcast.Group{{ID: "g"}, {ID: "idle"}}
	for _, id := range []string{"local", "a", "b"} {
		var c snapcast.ClientInfo
		c.ID, c.Connected = id, true
		if slices.Contains(f.members, id) {
			st.Groups[0].Clients = append(st.Groups[0].Clients, c)
		} else {
			st.Groups[1].Clients = append(st.Groups[1].Clients, c)
		}
	}
	return st, nil
}

func (f *groupFake) SetClients(_ context.Context, _ string, clients []string) error {
	time.Sleep(30 * time.Millisecond)
	f.mu.Lock()
	f.members = append([]string(nil), clients...)
	f.mu.Unlock()
	return nil
}

func TestMultiroomOverlappingMembershipKeepsBothRooms(t *testing.T) {
	fake := &groupFake{members: []string{"local"}}
	srv := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = fake, "local" })
	h := srv.Handler()
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			multiroomRequest(t, h, http.MethodPut, "/api/multiroom/clients/"+id+"/membership", `{"member":true}`)
		}()
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
	s, sp, _ := radioServer(t)
	kicks := 0
	s.restartSnapclient = func() { kicks++ }
	if err := s.setFollow("10.0.1.10", "leader-1"); err != nil {
		t.Fatal(err)
	}
	host, id := s.Follow() // what the arbiter reads when the source leaves MULTIROOM
	playStation(t, sp)
	if err := s.setFollow("10.0.1.11", "leader-2"); err != nil {
		t.Fatal(err)
	}
	s.LeaveFollow(host, id)
	if h, i := s.Follow(); h != "10.0.1.11" || i != "leader-2" || kicks != 0 {
		t.Fatalf("follow = %q %q, kicks %d; the newer follow must survive", h, i, kicks)
	}

	host, id = s.Follow()
	if err := sp.Select(context.Background(), sourceMultiroom, "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	s.LeaveFollow(host, id)
	if h, _ := s.Follow(); h != "10.0.1.11" || kicks != 0 {
		t.Fatalf("follow = %q, kicks %d; back on MULTIROOM, the follow must stay", h, kicks)
	}

	playStation(t, sp)
	s.LeaveFollow(host, id)
	if h, _ := s.Follow(); h != "" || kicks != 1 {
		t.Fatalf("follow = %q, kicks %d; a leave with nothing newer must end the follow", h, kicks)
	}
}

// A host name that resolves to loopback passes the name check, so the dial itself refuses
// it: the invite is a POST from this process and would pass every loopbackOnly check.
func TestPeerDialRefusesLoopbackAliases(t *testing.T) {
	defer func(f func(net.IP) bool) { peerIPAllowed = f }(peerIPAllowed)
	peerIPAllowed = strictPeerIP
	for _, target := range []string{"localhost.", "127.0.0.1:80", "LOCALHOST.:8090"} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+target+"/api/multiroom/follow", nil)
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
func TestFollowJoinsTheLeadersGroup(t *testing.T) {
	defer func(old func(context.Context, string) string) { peerLeader = old }(peerLeader)
	peerLeader = func(context.Context, string) string { return "" }
	status := snapcast.Status{Groups: []snapcast.Group{{ID: "lead-group"}, {ID: "own-group"}}}
	client := func(id string, muted bool) snapcast.ClientInfo {
		var c snapcast.ClientInfo
		c.ID, c.Connected = id, true
		c.Config.Volume = snapcast.Volume{Percent: 40, Muted: muted}
		return c
	}
	status.Groups[0].Clients = []snapcast.ClientInfo{client("soundtap-LEAD", true)}
	status.Groups[1].Clients = []snapcast.ClientInfo{client("soundtap-WLA", true)}
	snap := &fakeMultiroom{statuses: []snapcast.Status{status}}
	leader := &Server{homeDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	leader.ReconfigureForTest(func(o *Options) { o.Multiroom.Backend, o.Multiroom.LocalID = snap, "soundtap-LEAD" })
	srv := httptest.NewServer(leader.Handler())
	defer srv.Close()
	defer func(p string) { downstreamPort = p }(downstreamPort)
	downstreamPort = srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	follower := &Server{homeDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	follower.ReconfigureForTest(func(o *Options) {
		o.Multiroom.Backend, o.Multiroom.LocalID = &fakeMultiroom{statuses: []snapcast.Status{{Groups: []snapcast.Group{
			{ID: "g", Clients: []snapcast.ClientInfo{client("soundtap-WLA", false)}}}}}}, "soundtap-WLA"
	})
	w := httptest.NewRecorder()
	follower.postMultiroomFollow(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/follow",
		strings.NewReader(`{"host":"127.0.0.1"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("follow = %d %s", w.Code, w.Body)
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
func TestListenAllowsTheCallerAddress(t *testing.T) {
	var got string
	orig := allowFollower
	t.Cleanup(func() { allowFollower = orig })
	allowFollower = func(_ context.Context, host string) { got = host }
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/multiroom/listen", nil)
	r.RemoteAddr = "192.168.1.9:51000"
	w := httptest.NewRecorder()
	(&Server{}).postMultiroomListen(w, r)
	if w.Code != http.StatusNoContent || got != "192.168.1.9" {
		t.Fatalf("status %d, allowed %q", w.Code, got)
	}
}
