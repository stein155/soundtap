// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"errors"
	"io"
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
	var changes atomic.Int32
	a := NewAnchorDemand(150*time.Millisecond, func() { changes.Add(1) })
	if a.Wanted() {
		t.Fatal("an anchor nobody asked for is wanted")
	}
	a.Want()
	if !a.Wanted() || changes.Load() != 1 {
		t.Fatalf("after Want: wanted %v, %d changes, want true and 1", a.Wanted(), changes.Load())
	}
	time.Sleep(100 * time.Millisecond)
	a.Want() // a listener seen again: the hold starts over, and nothing turned
	if changes.Load() != 1 {
		t.Fatalf("a Want while wanted reported a change: %d", changes.Load())
	}
	time.Sleep(100 * time.Millisecond) // 200 ms after the first Want, 100 ms after the second
	if !a.Wanted() {
		t.Fatal("the second Want did not extend the hold")
	}
	for deadline := time.Now().Add(2 * time.Second); a.Wanted() || changes.Load() < 2; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the hold never ran out: wanted %v, %d changes", a.Wanted(), changes.Load())
		}
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

func (f *memberFake) want() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "want")
	f.started = true
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
	follower := snapcast.ClientInfo{ID: "soundtap-follower", Connected: true}
	status := snapcast.Status{Groups: []snapcast.Group{{ID: "g-follower", Clients: []snapcast.ClientInfo{follower}}}}
	if f.known {
		local := snapcast.ClientInfo{ID: "soundtap-local", Connected: true}
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

func (f *memberFake) SetVolume(context.Context, string, snapcast.Volume) error { return nil }

// A follower joins a leader whose anchor is parked and whose snapserver never saw it: the
// membership call starts the anchor first, waits until snapserver knows it, and then puts
// the follower in the anchor's group instead of failing with ErrNoLocal.
func TestSetMemberStartsTheAnchorBeforeJoining(t *testing.T) {
	fake := &memberFake{pollsAfter: 2}
	env := Env{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Snapcast: fake,
		LocalID: "soundtap-local", WantAnchor: fake.want}
	var g Group
	if err := g.SetMember(context.Background(), env, "follower", true); err != nil {
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
	wait := anchorWait
	anchorWait = 300 * time.Millisecond
	t.Cleanup(func() { anchorWait = wait })
	fake := &memberFake{pollsAfter: -1}
	env := Env{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Snapcast: fake,
		LocalID: "soundtap-local", WantAnchor: fake.want}
	var g Group
	start := time.Now()
	err := g.SetMember(context.Background(), env, "follower", true)
	if !errors.Is(err, ErrNoLocal) {
		t.Fatalf("SetMember = %v, want ErrNoLocal", err)
	}
	if took := time.Since(start); took < anchorWait || took > anchorWait+time.Second {
		t.Fatalf("gave up after %v, want about %v", took, anchorWait)
	}
	if slices.Contains(fake.calls, "set g-local") {
		t.Fatal("changed a group without the anchor")
	}
}

// A standby speaker that leads no group gives its anchor no reason to run; one with a
// follower connected keeps it.
func TestSyncListenersWantsTheAnchorOnlyWhileLeading(t *testing.T) {
	for _, follower := range []bool{false, true} {
		status := snapcast.Status{Groups: []snapcast.Group{{ID: "g", Clients: []snapcast.ClientInfo{
			{ID: "soundtap-local", Connected: false},
		}}}}
		if follower {
			status.Groups[0].Clients = append(status.Groups[0].Clients, snapcast.ClientInfo{ID: "soundtap-follower", Connected: true})
		}
		wanted := false
		env := Env{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Snapcast: statusFake{status: status},
			LocalID: "soundtap-local", Active: func(bool) {}, Downstream: func(context.Context, string) (int, error) {
				return 0, errors.New("no follower API")
			}, WantAnchor: func() { wanted = true }}
		SyncListeners(context.Background(), env)
		if wanted != follower {
			t.Fatalf("with a follower %v: anchor wanted %v", follower, wanted)
		}
	}
}

// statusFake is a snapserver that only answers GetStatus.
type statusFake struct {
	Backend
	status snapcast.Status
}

func (f statusFake) GetStatus(context.Context) (snapcast.Status, error) { return f.status, nil }

// While parked the anchor is still this speaker: its own row stays connected, and a week of
// standby does not drop it from its own list.
func TestParkedAnchorIsStillThisSpeaker(t *testing.T) {
	var local snapcast.ClientInfo
	local.ID = "soundtap-local"
	local.LastSeen.Sec = time.Now().Add(-8 * 24 * time.Hour).Unix()
	status := snapcast.Status{Groups: []snapcast.Group{{ID: "g", Clients: []snapcast.ClientInfo{local}}}}
	rows := Clients(status, "soundtap-local", nil, "Kitchen", 30)
	if len(rows) != 1 || !rows[0].Current || !rows[0].Connected || rows[0].LastSeenAgo != 0 || rows[0].GroupID != "g" {
		t.Fatalf("rows %+v, want this speaker connected in group g", rows)
	}
}
