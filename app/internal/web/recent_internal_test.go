// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/recent"
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

// librespot reopens its sink on every pause, resume and track boundary and claims
// SPOTIFY again each time, while its player thread waits for the answer. Such a reclaim
// of the source that already owns the amp is no source change: it must not write the
// recent list to flash, which held the next track's first audio back.
func TestRecentNotWrittenWhenTheOwnerReclaims(t *testing.T) {
	t.Parallel()

	srv, spk, dir := radioServer(t)

	ctx := context.Background()

	err := spk.Select(ctx, "SPOTIFY", "", "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M", "Road trip", "", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/source",
		strings.NewReader(`{"source":"SPOTIFY"}`))
	req.Host, req.RemoteAddr = "127.0.0.1:8000", "127.0.0.1:54321"
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("reclaim = %d %s", rec.Code, rec.Body)
	}

	if got := srv.recentStore.All(); len(got) != 0 {
		t.Errorf("recent = %+v after a reclaim, want nothing", got)
	}

	_, err = os.Stat(filepath.Join(dir, "recent.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("recent.json written on a reclaim: %v", err)
	}
}

// A factory reset from the browser closes the recent list and the favorites before the
// speaker removes their files: a save still in flight is refused, not written back.
func TestFactoryResetClosesTheHistory(t *testing.T) {
	t.Parallel()

	srv, _, _ := radioServer(t)

	var recentErr, favoriteErr error

	srv.hardwareReset = func() error {
		recentErr = srv.recentStore.Record(recent.Entry{
			Source: "TUNEIN", Name: "Radio 1", Location: "s1", Art: "", StoppedAt: time.Now(),
		})
		favoriteErr = srv.favoritesStore.Add(favorites.Entry{
			Source: "TUNEIN", Name: "Radio 1", Location: "s1", Art: "",
		})

		return nil
	}

	rec := radioDo(t, srv.Handler(), http.MethodPost, "/api/factory-reset", `{"confirm":true}`,
		"Content-Type", "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", rec.Code, rec.Body)
	}

	if !errors.Is(recentErr, recent.ErrClosed) || !errors.Is(favoriteErr, favorites.ErrClosed) {
		t.Errorf("saves during the reset = %v, %v; want both refused as closed", recentErr, favoriteErr)
	}
}
