// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"testing"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// A power answer for a leader the user has since left, or replaced, must do nothing.
//
//nolint:paralleltest // it swaps the package-level peer now-playing lookup
func TestSyncLeaderPowerIgnoresStaleFollow(t *testing.T) {
	for name, change := range map[string]func(*Server) error{
		"leaving":  func(s *Server) error { return s.setFollow("", "") },
		"changing": func(s *Server) error { return s.setFollow("10.0.1.11", "new-leader") },
	} {
		t.Run(name, func(t *testing.T) {
			srv, fakeSpeaker, _ := radioServer(t)
			srv.homeDir = t.TempDir()
			ctx := context.Background()

			err := srv.setFollow("10.0.1.10", "old-leader")
			if err != nil {
				t.Fatal(err)
			}

			err = fakeSpeaker.SetSource(ctx, "STANDBY")
			if err != nil {
				t.Fatal(err)
			}

			var sources []string

			srv.selectSource = func(ctx context.Context, source string) error {
				sources = append(sources, source)

				return fakeSpeaker.SetSource(ctx, source)
			}

			old := peerNow
			defer func() { peerNow = old }()

			peerNow = func(context.Context, string) (speaker.NowPlaying, bool) {
				err := change(srv)
				if err != nil {
					t.Fatal(err)
				}

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
				}, true
			}

			srv.syncLeaderPower(ctx)

			if len(sources) > 0 {
				t.Fatalf("stale leader answer selected %v", sources)
			}
		})
	}
}

// A follower whose leader does not answer is not playing: it shows BUFFERING_STATE
// until the leader is back, instead of PLAY_STATE for the whole grace period.
//
//nolint:paralleltest // it swaps the package-level peer now-playing lookup
func TestFollowerBuffersWhileLeaderIsGone(t *testing.T) {
	srv, fakeSpeaker, _ := radioServer(t)
	srv.homeDir = t.TempDir()
	srv.leaderLive, srv.selectSource = fakeSpeaker.SetLeaderLive, fakeSpeaker.SetSource
	ctx := context.Background()

	err := srv.setFollow("10.0.1.10", "leader")
	if err != nil {
		t.Fatal(err)
	}

	err = fakeSpeaker.SetSource(ctx, "MULTIROOM")
	if err != nil {
		t.Fatal(err)
	}

	old := peerNow
	defer func() { peerNow = old }()

	for _, step := range []struct {
		ok   bool
		want string
	}{{false, "BUFFERING_STATE"}, {true, "PLAY_STATE"}} {
		peerNow = func(context.Context, string) (speaker.NowPlaying, bool) {
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
			}, step.ok
		}

		srv.syncLeaderPower(ctx)

		if np, _ := fakeSpeaker.NowPlaying(ctx); np.Source != "MULTIROOM" || np.PlayStatus != step.want {
			t.Fatalf("leader answers=%v: %s %s, want %s", step.ok, np.Source, np.PlayStatus, step.want)
		}
	}
}

// A follower shows what its leader plays, on everything that reads its own state, and
// keeps MULTIROOM as the source and no location of its own.
//
//nolint:paralleltest // it swaps the package-level peer now-playing lookup
func TestFollowerMirrorsLeaderNowPlaying(t *testing.T) {
	srv, fakeSpeaker, _ := radioServer(t)
	srv.homeDir = t.TempDir()
	srv.leaderNow, srv.selectSource = fakeSpeaker.SetLeaderNow, fakeSpeaker.SetSource
	ctx := context.Background()

	err := srv.setFollow("10.0.1.10", "leader")
	if err != nil {
		t.Fatal(err)
	}

	err = fakeSpeaker.SetSource(ctx, "MULTIROOM")
	if err != nil {
		t.Fatal(err)
	}

	old := peerNow
	defer func() { peerNow = old }()

	peerNow = func(context.Context, string) (speaker.NowPlaying, bool) {
		return speaker.NowPlaying{
			Source: "TUNEIN", Track: "Song", Artist: "Band", Station: "Qmusic",
			Art: "http://logo", Location: "/v1/playback/station/s1",
			Album:      "",
			StationID:  "",
			PlayStatus: "",
			CanSkip:    false,
			Failed:     false,
			Unheard:    false,
		}, true
	}

	srv.syncLeaderPower(ctx)
	playing, _ := fakeSpeaker.NowPlaying(ctx)

	want := speaker.NowPlaying{
		Source: "MULTIROOM", Track: "Song", Artist: "Band", Station: "Qmusic",
		Art: "http://logo", PlayStatus: playing.PlayStatus,
		Album:     "",
		StationID: "",
		Location:  "",
		CanSkip:   false,
		Failed:    false,
		Unheard:   false,
	}
	if *playing != want {
		t.Fatalf("follower shows %+v, want %+v", *playing, want)
	}
}
