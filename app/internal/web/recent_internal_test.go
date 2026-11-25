// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"testing"
)

// Recent keeps what can be started again: TuneIn and a Spotify list started here, not a
// UPnP controller's stream URL, which is gone once it stops.
func TestRecentKeepsOnlyReplayableLocations(t *testing.T) {
	t.Parallel()

	srv, spk, _ := radioServer(t)

	ctx := context.Background()
	for _, c := range []struct{ source, location, name string }{
		{"UPNP", "http://192.0.2.124:8098/flow/x/1.mp3", "Music Assistant"},
		{"SPOTIFY", "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M", "Road trip"},
	} {
		err := spk.Select(ctx, c.source, "", c.location, c.name, "", "")
		if err != nil {
			t.Fatal(err)
		}

		srv.snapshotRecent(ctx)
	}

	got := map[string]string{}
	for _, e := range srv.recentStore.All() {
		got[e.Source] = e.Location
	}

	if got["UPNP"] != "" {
		t.Errorf("UPNP location kept: %q", got["UPNP"])
	}

	if got["SPOTIFY"] != "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M" {
		t.Errorf("SPOTIFY location = %q", got["SPOTIFY"])
	}
}

// A URL that never starts must not stay behind on Home.
func TestRecentSkipsWhatNeverStartedPlaying(t *testing.T) {
	t.Parallel()

	srv, spk, _ := radioServer(t)

	ctx := context.Background()

	err := spk.Select(ctx, "TUNEIN", "", "http://192.0.2.1/x.mp3", "Dead", "", "")
	if err != nil {
		t.Fatal(err)
	}

	spk.BeginBuffering("TUNEIN")
	srv.snapshotRecent(ctx)

	if got := srv.recentStore.All(); len(got) != 0 {
		t.Fatalf("recent = %+v, want nothing", got)
	}
}

// A pause before the first audio is still nothing heard; after real audio it is.
func TestRecentSkipsWhatWasPausedBeforeItPlayed(t *testing.T) {
	t.Parallel()

	srv, spk, _ := radioServer(t)

	ctx := context.Background()

	err := spk.Select(ctx, "TUNEIN", "", "http://192.0.2.1/x.mp3", "Never heard", "", "")
	if err != nil {
		t.Fatal(err)
	}

	spk.BeginBuffering("TUNEIN")

	err = spk.Key(ctx, "PAUSE")
	if err != nil {
		t.Fatal(err)
	}

	srv.snapshotRecent(ctx)

	if got := srv.recentStore.All(); len(got) != 0 {
		t.Fatalf("recent = %+v, want nothing", got)
	}

	err = spk.Key(ctx, "PLAY")
	if err != nil {
		t.Fatal(err)
	}

	spk.ConfirmPlaying(spk.BeginBuffering("TUNEIN"))

	err = spk.Key(ctx, "PAUSE")
	if err != nil {
		t.Fatal(err)
	}

	srv.snapshotRecent(ctx)

	if got := srv.recentStore.All(); len(got) != 1 {
		t.Fatalf("recent = %+v, want the station once it played", got)
	}
}

// A phone cannot be started again from the speaker, so its name stays off the list.
func TestRecentSkipsBluetooth(t *testing.T) {
	t.Parallel()

	srv, spk, _ := radioServer(t)

	ctx := context.Background()

	err := spk.Select(ctx, sourceBluetooth, "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	spk.SetBluetoothTrack("Anna", "Song", "Band", "")
	srv.snapshotRecent(ctx)

	if got := srv.recentStore.All(); len(got) != 0 {
		t.Fatalf("recent = %+v, want nothing", got)
	}
}
