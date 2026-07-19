// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/upnp"
)

// A failed stream keeps UPNP selected; the controller must see STOPPED so its Play
// retries the stream instead of being swallowed.
func TestUPnPTransportStateFollowsPlayStatus(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	b := upnpBackend{sp: sp}
	if err := b.PlayURL(ctx, "http://192.168.1.9/dead.mp3", upnp.Meta{Title: "dead"}, 0); err != nil {
		t.Fatal(err)
	}
	if got := b.TransportState(ctx); got != "TRANSITIONING" {
		t.Fatalf("buffering state = %q, want TRANSITIONING", got)
	}
	sp.FailCurrentPlayback()
	if got := b.TransportState(ctx); got != "STOPPED" {
		t.Fatalf("failed stream state = %q, want STOPPED", got)
	}
}

// Pause keeps UPNP selected as PAUSED_PLAYBACK, Play streams again, and a Pause that
// arrives after the user switched sources leaves the new source playing.
func TestUPnPPauseHoldsTheSource(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	b := upnpBackend{sp: sp}
	if err := b.PlayURL(ctx, "http://192.168.1.9/a.flac", upnp.Meta{Title: "a"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if got := b.TransportState(ctx); got != "PAUSED_PLAYBACK" {
		t.Fatalf("after Pause = %q, want PAUSED_PLAYBACK", got)
	}
	if err := b.Pause(ctx); err != nil || b.TransportState(ctx) != "PAUSED_PLAYBACK" {
		t.Fatalf("second Pause must not toggle back: %v %q", err, b.TransportState(ctx))
	}
	if err := b.PlayURL(ctx, "http://192.168.1.9/a.flac", upnp.Meta{Title: "a"}, 0); err != nil {
		t.Fatal(err)
	}
	if got := b.TransportState(ctx); got != "TRANSITIONING" {
		t.Fatalf("Play after Pause = %q, want TRANSITIONING", got)
	}
	if err := sp.Select(ctx, "TUNEIN", "", "s1", "Radio", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if np, _ := sp.NowPlaying(ctx); np.Source != "TUNEIN" || np.PlayStatus == "PAUSE_STATE" {
		t.Fatalf("late Pause touched %s: %s", np.Source, np.PlayStatus)
	}
	if got := b.TransportState(ctx); got != "NO_MEDIA_PRESENT" {
		t.Fatalf("another source = %q, want NO_MEDIA_PRESENT", got)
	}
}

// The DIDL's artist and cover show in now playing next to its title.
func TestUPnPPlayShowsArtistAndCover(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	b := upnpBackend{sp: sp}
	m := upnp.Meta{Title: "Song", Artist: "Band", Art: "http://192.168.1.9/cover.jpg"}
	if err := b.PlayURL(ctx, "http://192.168.1.9/a.flac", m, 0); err != nil {
		t.Fatal(err)
	}
	if np, _ := sp.NowPlaying(ctx); np.Station != "Song" || np.Artist != "Band" || np.Art != m.Art {
		t.Fatalf("now playing = %+v", np)
	}
}

// A controller sees a mute as volume 0 and sets it through SetMute; the speaker keeps the
// level to come back to.
func TestUPnPMuteKeepsTheLevel(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	b := upnpBackend{sp: sp}
	if err := sp.SetVolume(ctx, 40); err != nil {
		t.Fatal(err)
	}
	if err := b.SetMute(ctx, true); err != nil {
		t.Fatal(err)
	}
	if v, _ := b.Volume(ctx); v != 0 {
		t.Fatalf("muted Volume = %d, want 0", v)
	}
	if err := b.SetMute(ctx, true); err != nil || !sp.Muted() {
		t.Fatalf("a second SetMute(true) must stay muted: %v", err)
	}
	if err := b.SetMute(ctx, false); err != nil {
		t.Fatal(err)
	}
	if v, _ := b.Volume(ctx); v != 40 {
		t.Fatalf("unmuted Volume = %d, want 40", v)
	}
}

// A folder plays through: the track that ends starts the next, the Next key walks it, and
// a controller's own URL ends the queue instead of being played over.
func TestUPnPFolderPlaysThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upnpFolder.Clear()
	defer upnpFolder.Clear()
	audio := "object.item.audioItem.musicTrack"
	folder := []upnp.Item{
		{Class: audio, URL: "http://192.168.1.9/1.mp3", Title: "1"},
		{Class: audio, URL: "http://192.168.1.9/2.mp3", Title: "2"},
		{Class: audio, URL: "http://192.168.1.9/3.mp3", Title: "3"},
	}
	sp := speakerstate.New()
	if err := sp.Select(ctx, sourceUPnP, "", folder[0].URL, "1", "", ""); err != nil {
		t.Fatal(err)
	}
	upnpFolder.Set(folder, folder[0].URL)
	go runUPnPFolder(ctx, sp, slog.Default())

	waitOn := func(want string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if np, _ := sp.NowPlaying(ctx); np != nil && np.Location == want && np.PlayStatus != "STOP_STATE" {
				return
			}
		}
		np, _ := sp.NowPlaying(ctx)
		t.Fatalf("speaker on %+v, want %s", np, want)
	}
	sp.FailCurrentPlayback() // the same stop a track's clean end leaves
	waitOn(folder[1].URL)
	if err := upnpFolderStep(ctx, sp, 1); err != nil {
		t.Fatal(err)
	}
	waitOn(folder[2].URL)

	own := "http://192.168.1.9/own.mp3"
	if err := sp.Select(ctx, sourceUPnP, "", own, "own", "", ""); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(5 * time.Second); upnpFolder.Keep(folder[2].URL) && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}
	sp.FailCurrentPlayback()
	time.Sleep(50 * time.Millisecond)
	if np, _ := sp.NowPlaying(ctx); np.Location != own {
		t.Fatalf("a controller's track must not be followed by the folder: on %s", np.Location)
	}
}

// The album goes with the track and does not outlive it.
func TestUPnPAlbumFollowsTheTrack(t *testing.T) {
	ctx := context.Background()
	sp := speakerstate.New()
	b := upnpBackend{sp: sp}
	if err := b.PlayURL(ctx, "http://192.168.1.9/a.flac", upnp.Meta{Title: "a", Artist: "Band", Album: "Record"}, 0); err != nil {
		t.Fatal(err)
	}
	if np, _ := sp.NowPlaying(ctx); np.Album != "Record" {
		t.Fatalf("album = %q", np.Album)
	}
	if err := b.PlayURL(ctx, "http://192.168.1.9/b.flac", upnp.Meta{Title: "b"}, 0); err != nil {
		t.Fatal(err)
	}
	if np, _ := sp.NowPlaying(ctx); np.Album != "" {
		t.Fatalf("album %q outlived its track", np.Album)
	}
}

// The renderer's routes share port 80 with the API, so they sit behind its Host and CSRF
// guard too: without it a rebound web page could play and set the volume.
func TestUPnPRoutesSitBehindTheGuard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mux := http.NewServeMux()
	refuse := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	}
	startUPnP(ctx, speakerstate.New(), mux, refuse, "Keuken", "80", slogDiscard())
	for _, path := range []string{"/upnp/desc.xml", "/upnp/control/avtransport", "/upnp/event/avtransport"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want the guard's 403", path, rec.Code)
		}
	}
}
