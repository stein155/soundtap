// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// nowPlaying is what a speaker playing source reports.
func nowPlaying(source string) *speaker.NowPlaying {
	var np speaker.NowPlaying

	np.Source = source

	return &np
}

// The standby mark is for the group's standby only: a leave to another source, or a mark
// nobody consumed in time, must not swallow a real leave.
func TestKeepFollowIsNotLeftStale(t *testing.T) {
	t.Parallel()

	var group Group

	source := "TUNEIN" // the source changed under the mark: a real leave
	env := testEnv()
	env.NowPlaying = func(context.Context) (*speaker.NowPlaying, error) { return nowPlaying(source), nil }
	fresh := time.Now().Add(keepFollowFor).UnixNano()
	group.keep.Store(fresh)

	if group.KeepFollow(env) {
		t.Fatal("a leave to another source used the standby mark")
	}

	if group.keep.Load() != 0 {
		t.Fatal("the mark was left behind")
	}

	source = SourceStandby

	group.keep.Store(time.Now().Add(-time.Second).UnixNano())

	if group.KeepFollow(env) {
		t.Fatal("an expired mark swallowed a leave")
	}

	group.keep.Store(fresh)

	if !group.KeepFollow(env) {
		t.Fatal("the group's own standby was not kept")
	}
}

// A follower that follows its leader into standby must not stay on the leader's silent
// stream: snapclient goes back to the anchor after the switch to STANDBY, and follows the
// leader again after the switch back to MULTIROOM, in that order.
func TestLeaderStandbyRestartsSnapclientAfterTheSwitch(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/multiroom-server"

	err := WriteFollow(path, "192.168.1.20", "lead")
	if err != nil {
		t.Fatal(err)
	}

	source, leader := SourceMultiroom, SourceStandby

	var calls []string

	env := testEnv()
	env.Path = path
	env.NowPlaying = func(context.Context) (*speaker.NowPlaying, error) { return nowPlaying(source), nil }
	env.SelectSource = func(_ context.Context, to string) error {
		source = to
		calls = append(calls, to)

		return nil
	}
	env.RestartSnapclient = func() { calls = append(calls, "restart") }
	env.PeerNow = func(context.Context, string) (speaker.NowPlaying, bool) { return *nowPlaying(leader), true }

	var group Group
	group.SyncLeaderPower(t.Context(), env)

	leader = "TUNEIN"

	group.SyncLeaderPower(t.Context(), env)

	want := []string{SourceStandby, "restart", SourceMultiroom, "restart"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
