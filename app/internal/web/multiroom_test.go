// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"testing"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// A power answer for a leader the user has since left, or replaced, must do nothing.
func TestSyncLeaderPowerIgnoresStaleFollow(t *testing.T) {
	for name, change := range map[string]func(*Server) error{
		"leaving":  func(s *Server) error { return s.setFollow("", "") },
		"changing": func(s *Server) error { return s.setFollow("10.0.1.11", "new-leader") },
	} {
		t.Run(name, func(t *testing.T) {
			s, sp, _ := radioServer(t)
			s.homeDir = t.TempDir()
			ctx := context.Background()
			if err := s.setFollow("10.0.1.10", "old-leader"); err != nil {
				t.Fatal(err)
			}
			if err := sp.SetSource(ctx, "STANDBY"); err != nil {
				t.Fatal(err)
			}
			var sources []string
			s.selectSource = func(ctx context.Context, source string) error {
				sources = append(sources, source)
				return sp.SetSource(ctx, source)
			}
			old := peerNow
			defer func() { peerNow = old }()
			peerNow = func(context.Context, string) (speaker.NowPlaying, bool) {
				if err := change(s); err != nil {
					t.Fatal(err)
				}
				return speaker.NowPlaying{Source: "TUNEIN"}, true
			}
			s.syncLeaderPower(ctx)
			if len(sources) > 0 {
				t.Fatalf("stale leader answer selected %v", sources)
			}
		})
	}
}

// A follower whose leader does not answer is not playing: it shows BUFFERING_STATE
// until the leader is back, instead of PLAY_STATE for the whole grace period.
func TestFollowerBuffersWhileLeaderIsGone(t *testing.T) {
	s, sp, _ := radioServer(t)
	s.homeDir = t.TempDir()
	s.leaderLive, s.selectSource = sp.SetLeaderLive, sp.SetSource
	ctx := context.Background()
	if err := s.setFollow("10.0.1.10", "leader"); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetSource(ctx, "MULTIROOM"); err != nil {
		t.Fatal(err)
	}
	old := peerNow
	defer func() { peerNow = old }()
	for _, step := range []struct {
		ok   bool
		want string
	}{{false, "BUFFERING_STATE"}, {true, "PLAY_STATE"}} {
		peerNow = func(context.Context, string) (speaker.NowPlaying, bool) {
			return speaker.NowPlaying{Source: "TUNEIN"}, step.ok
		}
		s.syncLeaderPower(ctx)
		if np, _ := sp.NowPlaying(ctx); np.Source != "MULTIROOM" || np.PlayStatus != step.want {
			t.Fatalf("leader answers=%v: %s %s, want %s", step.ok, np.Source, np.PlayStatus, step.want)
		}
	}
}

// A follower shows what its leader plays, on everything that reads its own state, and
// keeps MULTIROOM as the source and no location of its own.
func TestFollowerMirrorsLeaderNowPlaying(t *testing.T) {
	s, sp, _ := radioServer(t)
	s.homeDir = t.TempDir()
	s.leaderNow, s.selectSource = sp.SetLeaderNow, sp.SetSource
	ctx := context.Background()
	if err := s.setFollow("10.0.1.10", "leader"); err != nil {
		t.Fatal(err)
	}
	if err := sp.SetSource(ctx, "MULTIROOM"); err != nil {
		t.Fatal(err)
	}
	old := peerNow
	defer func() { peerNow = old }()
	peerNow = func(context.Context, string) (speaker.NowPlaying, bool) {
		return speaker.NowPlaying{Source: "TUNEIN", Track: "Song", Artist: "Band", Station: "Qmusic",
			Art: "http://logo", Location: "/v1/playback/station/s1"}, true
	}
	s.syncLeaderPower(ctx)
	np, _ := sp.NowPlaying(ctx)
	want := speaker.NowPlaying{Source: "MULTIROOM", Track: "Song", Artist: "Band", Station: "Qmusic",
		Art: "http://logo", PlayStatus: np.PlayStatus}
	if *np != want {
		t.Fatalf("follower shows %+v, want %+v", *np, want)
	}
}
