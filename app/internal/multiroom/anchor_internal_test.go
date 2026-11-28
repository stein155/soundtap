// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/snapcast"
)

// The anchor is wanted from a Want until the hold has passed without another, and the
// process gate hears of both turns: the start at once, the end when the hold runs out.
func TestAnchorDemandHoldsThenLetsGo(t *testing.T) {
	t.Parallel()

	var changes atomic.Int32

	demand := NewAnchorDemand(150*time.Millisecond, func() { changes.Add(1) })
	if demand.Wanted() {
		t.Fatal("an anchor nobody asked for is wanted")
	}

	demand.Want()

	if !demand.Wanted() || changes.Load() != 1 {
		t.Fatalf("after Want: wanted %v, %d changes, want true and 1", demand.Wanted(), changes.Load())
	}

	time.Sleep(100 * time.Millisecond)
	demand.Want() // a listener seen again: the hold starts over, and nothing turned

	if changes.Load() != 1 {
		t.Fatalf("a Want while wanted reported a change: %d", changes.Load())
	}

	time.Sleep(100 * time.Millisecond) // 200 ms after the first Want, 100 ms after the second

	if !demand.Wanted() {
		t.Fatal("the second Want did not extend the hold")
	}

	deadline := time.Now().Add(2 * time.Second)
	for demand.Wanted() || changes.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the hold never ran out: wanted %v, %d changes", demand.Wanted(), changes.Load())
		}

		time.Sleep(10 * time.Millisecond)
	}

	var none *AnchorDemand
	if none.Wanted() {
		t.Fatal("a nil demand wants the anchor")
	}
}

// memberFake is a snapserver whose anchor client appears only some polls after the anchor
// was started, as a parked snapclient's does, and which records the calls in order.
type memberFake struct {
	Backend

	mu         sync.Mutex
	calls      []string
	started    bool
	pollsAfter int // GetStatus calls after the start before the anchor is known; -1 never
	known      bool
	clients    []string
}

func (f *memberFake) GetStatus(context.Context) (snapcast.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "status")
	if f.started && f.pollsAfter >= 0 {
		if f.pollsAfter == 0 {
			f.known = true
		}

		f.pollsAfter--
	}

	follower := snapClient("soundtap-follower", true)
	status := snapcast.Status{Groups: []snapcast.Group{{ID: "g-follower", Clients: []snapcast.ClientInfo{follower}}}}

	if f.known {
		local := snapClient("soundtap-local", true)
		status.Groups = append(status.Groups, snapcast.Group{ID: "g-local", Clients: []snapcast.ClientInfo{local}})
	}

	return status, nil
}

func (f *memberFake) SetClients(_ context.Context, group string, clients []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "set "+group)
	f.clients = clients

	return nil
}

func (*memberFake) SetVolume(context.Context, string, snapcast.Volume) error { return nil }

func (f *memberFake) want() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "want")
	f.started = true
}

// newMemberFake is a memberFake whose anchor shows up pollsAfter polls after its start.
func newMemberFake(pollsAfter int) *memberFake {
	return &memberFake{
		Backend: nil, mu: sync.Mutex{}, calls: nil, started: false, pollsAfter: pollsAfter, known: false, clients: nil,
	}
}

// testEnv is an Env with none of the speaker's parts wired and a silent log.
func testEnv() Env {
	return Env{
		Log: slog.New(slog.DiscardHandler), Path: "",
		NowPlaying: nil, SelectSource: nil, SourceChanging: nil, RestartSnapclient: nil,
		LeaderLive: nil, LeaderNow: nil, Nudge: nil, DeviceIP: nil,
		PeerLeader: nil, PeerNow: nil, LeaderReachable: nil, FollowerPeers: nil,
		StepPeerVolume: nil, ReleasePeer: nil, JoinLeaderGroup: nil, PeerTimeout: 0,
		Snapcast: nil, LocalID: "", WantAnchor: nil, Active: nil, ActivePath: "", Downstream: nil,
		LeaderTrim: nil,
	}
}

// snapClient is a snapcast client with only its id and connection set.
func snapClient(id string, connected bool) snapcast.ClientInfo {
	var client snapcast.ClientInfo

	client.ID, client.Connected = id, connected

	return client
}

// A follower joins a leader whose anchor is parked and whose snapserver never saw it: the
// membership call starts the anchor first, waits until snapserver knows it, and then puts
// the follower in the anchor's group instead of failing with ErrNoLocal.
func TestSetMemberStartsTheAnchorBeforeJoining(t *testing.T) {
	t.Parallel()

	fake := newMemberFake(2)
	env := testEnv()
	env.Snapcast, env.LocalID, env.WantAnchor = fake, "soundtap-local", fake.want

	var group Group

	err := group.SetMember(context.Background(), env, "follower", true)
	if err != nil {
		t.Fatalf("SetMember: %v", err)
	}

	if want := []string{"want", "status", "status", "status", "set g-local"}; !slices.Equal(fake.calls, want) {
		t.Fatalf("calls %q, want %q", fake.calls, want)
	}

	if want := []string{"soundtap-local", "soundtap-follower"}; !slices.Equal(fake.clients, want) {
		t.Fatalf("group clients %q, want %q", fake.clients, want)
	}
}

// An anchor that never shows up ends the wait at its bound with ErrNoLocal, as before.
func TestSetMemberGivesUpOnAnAnchorThatNeverComes(t *testing.T) {
	t.Parallel()

	fake := newMemberFake(-1)
	env := testEnv()
	env.Snapcast, env.LocalID, env.WantAnchor = fake, "soundtap-local", fake.want

	var group Group

	group.anchorWait = 300 * time.Millisecond
	start := time.Now()

	err := group.SetMember(context.Background(), env, "follower", true)
	if !errors.Is(err, ErrNoLocal) {
		t.Fatalf("SetMember = %v, want ErrNoLocal", err)
	}

	if took := time.Since(start); took < group.anchorWait || took > group.anchorWait+time.Second {
		t.Fatalf("gave up after %v, want about %v", took, group.anchorWait)
	}

	if slices.Contains(fake.calls, "set g-local") {
		t.Fatal("changed a group without the anchor")
	}
}

// A standby speaker that leads no group gives its anchor no reason to run; one with a
// follower connected keeps it.
func TestSyncListenersWantsTheAnchorOnlyWhileLeading(t *testing.T) {
	t.Parallel()

	for _, follower := range []bool{false, true} {
		status := snapcast.Status{Groups: []snapcast.Group{{ID: "g", Clients: []snapcast.ClientInfo{
			snapClient("soundtap-local", false),
		}}}}
		if follower {
			status.Groups[0].Clients = append(status.Groups[0].Clients, snapClient("soundtap-follower", true))
		}

		wanted := false
		env := testEnv()
		env.Snapcast, env.LocalID = statusFake{Backend: nil, status: status}, "soundtap-local"
		env.Active = func(bool) {}
		env.Downstream = func(context.Context, string) (int, error) { return 0, errNoFollowerAPI }
		env.WantAnchor = func() { wanted = true }
		SyncListeners(context.Background(), env)

		if wanted != follower {
			t.Fatalf("with a follower %v: anchor wanted %v", follower, wanted)
		}
	}
}

// errNoFollowerAPI is a follower that does not answer how long its audio chain is.
var errNoFollowerAPI = errors.New("no follower API")

// statusFake is a snapserver that only answers GetStatus.
type statusFake struct {
	Backend

	status snapcast.Status
}

func (f statusFake) GetStatus(context.Context) (snapcast.Status, error) { return f.status, nil }

// While parked the anchor is still this speaker: its own row stays connected, and a week of
// standby does not drop it from its own list.
func TestParkedAnchorIsStillThisSpeaker(t *testing.T) {
	t.Parallel()

	local := snapClient("soundtap-local", false)
	local.LastSeen.Sec = time.Now().Add(-8 * 24 * time.Hour).Unix()
	status := snapcast.Status{Groups: []snapcast.Group{{ID: "g", Clients: []snapcast.ClientInfo{local}}}}

	rows := Clients(status, "soundtap-local", nil, "Kitchen", 30)
	if len(rows) != 1 || !rows[0].Current || !rows[0].Connected || rows[0].LastSeenAgo != 0 ||
		rows[0].GroupID != "g" {
		t.Fatalf("rows %+v, want this speaker connected in group g", rows)
	}
}
