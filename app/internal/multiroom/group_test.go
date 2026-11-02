// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// The standby mark is for the group's standby only: a leave to another source, or a mark
// nobody consumed in time, must not swallow a real leave.
func TestKeepFollowIsNotLeftStale(t *testing.T) {
	var g Group
	source := "TUNEIN" // the source changed under the mark: a real leave
	env := Env{NowPlaying: func(context.Context) (*speaker.NowPlaying, error) {
		return &speaker.NowPlaying{Source: source}, nil
	}}
	fresh := time.Now().Add(keepFollowFor).UnixNano()
	g.keep.Store(fresh)
	if g.KeepFollow(env) {
		t.Fatal("a leave to another source used the standby mark")
	}
	if g.keep.Load() != 0 {
		t.Fatal("the mark was left behind")
	}
	source = SourceStandby
	g.keep.Store(time.Now().Add(-time.Second).UnixNano())
	if g.KeepFollow(env) {
		t.Fatal("an expired mark swallowed a leave")
	}
	g.keep.Store(fresh)
	if !g.KeepFollow(env) {
		t.Fatal("the group's own standby was not kept")
	}
}

// A follower that follows its leader into standby must not stay on the leader's silent
// stream: snapclient goes back to the anchor after the switch to STANDBY, and follows the
// leader again after the switch back to MULTIROOM, in that order.
func TestLeaderStandbyRestartsSnapclientAfterTheSwitch(t *testing.T) {
	path := t.TempDir() + "/multiroom-server"
	if err := WriteFollow(path, "192.168.1.20", "lead"); err != nil {
		t.Fatal(err)
	}
	source, leader := SourceMultiroom, SourceStandby
	var calls []string
	env := Env{
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Path: path,
		NowPlaying: func(context.Context) (*speaker.NowPlaying, error) {
			return &speaker.NowPlaying{Source: source}, nil
		},
		SelectSource: func(_ context.Context, s string) error {
			source = s
			calls = append(calls, s)
			return nil
		},
		RestartSnapclient: func() { calls = append(calls, "restart") },
		PeerNow: func(context.Context, string) (speaker.NowPlaying, bool) {
			return speaker.NowPlaying{Source: leader}, true
		},
	}
	var g Group
	g.SyncLeaderPower(t.Context(), env)
	leader = "TUNEIN"
	g.SyncLeaderPower(t.Context(), env)
	want := []string{SourceStandby, "restart", SourceMultiroom, "restart"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
