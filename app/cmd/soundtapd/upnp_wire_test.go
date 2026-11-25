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

// upnpTitleMeta is a controller's metadata with only a title.
func upnpTitleMeta(title string) upnp.Meta {
	return upnp.Meta{Title: title, Artist: "", Album: "", Art: "", Duration: 0}
}

// A failed stream keeps UPNP selected; the controller must see STOPPED so its Play
// retries the stream instead of being swallowed.
func TestUPnPTransportStateFollowsPlayStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	speaker := speakerstate.New()

	backend := upnpBackend{sp: speaker}

	err := backend.PlayURL(ctx, "http://192.168.1.9/dead.mp3", upnpTitleMeta("dead"), 0)
	if err != nil {
		t.Fatal(err)
	}

	if got := backend.TransportState(ctx); got != "TRANSITIONING" {
		t.Fatalf("buffering state = %q, want TRANSITIONING", got)
	}

	speaker.FailCurrentPlayback()

	if got := backend.TransportState(ctx); got != "STOPPED" {
		t.Fatalf("failed stream state = %q, want STOPPED", got)
	}
}

// Pause keeps UPNP selected as PAUSED_PLAYBACK, Play streams again, and a Pause that
// arrives after the user switched sources leaves the new source playing.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestUPnPPauseHoldsTheSource(t *testing.T) {
	ctx := context.Background()
	speaker := speakerstate.New()

	backend := upnpBackend{sp: speaker}

	err := backend.PlayURL(ctx, "http://192.168.1.9/a.flac", upnpTitleMeta("a"), 0)
	if err != nil {
		t.Fatal(err)
	}

	err = backend.Pause(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got := backend.TransportState(ctx); got != "PAUSED_PLAYBACK" {
		t.Fatalf("after Pause = %q, want PAUSED_PLAYBACK", got)
	}

	err = backend.Pause(ctx)
	if err != nil || backend.TransportState(ctx) != "PAUSED_PLAYBACK" {
		t.Fatalf("second Pause must not toggle back: %v %q", err, backend.TransportState(ctx))
	}

	err = backend.PlayURL(ctx, "http://192.168.1.9/a.flac", upnpTitleMeta("a"), 0)
	if err != nil {
		t.Fatal(err)
	}

	if got := backend.TransportState(ctx); got != "TRANSITIONING" {
		t.Fatalf("Play after Pause = %q, want TRANSITIONING", got)
	}

	err = speaker.Select(ctx, "TUNEIN", "", "s1", "Radio", "", "")
	if err != nil {
		t.Fatal(err)
	}

	err = backend.Pause(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if np, _ := speaker.NowPlaying(ctx); np.Source != "TUNEIN" || np.PlayStatus == "PAUSE_STATE" {
		t.Fatalf("late Pause touched %s: %s", np.Source, np.PlayStatus)
	}

	if got := backend.TransportState(ctx); got != "NO_MEDIA_PRESENT" {
		t.Fatalf("another source = %q, want NO_MEDIA_PRESENT", got)
	}
}

// The DIDL's artist and cover show in now playing next to its title.
func TestUPnPPlayShowsArtistAndCover(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	speaker := speakerstate.New()
	backend := upnpBackend{sp: speaker}

	meta := upnp.Meta{Title: "Song", Artist: "Band", Album: "", Art: "http://192.168.1.9/cover.jpg", Duration: 0}

	err := backend.PlayURL(ctx, "http://192.168.1.9/a.flac", meta, 0)
	if err != nil {
		t.Fatal(err)
	}

	if np, _ := speaker.NowPlaying(ctx); np.Station != "Song" || np.Artist != "Band" || np.Art != meta.Art {
		t.Fatalf("now playing = %+v", np)
	}
}

// A controller sees a mute as volume 0 and sets it through SetMute; the speaker keeps the
// level to come back to.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestUPnPMuteKeepsTheLevel(t *testing.T) {
	ctx := context.Background()
	speaker := speakerstate.New()

	backend := upnpBackend{sp: speaker}

	err := speaker.SetVolume(ctx, 40)
	if err != nil {
		t.Fatal(err)
	}

	err = backend.SetMute(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := backend.Volume(ctx); v != 0 {
		t.Fatalf("muted Volume = %d, want 0", v)
	}

	err = backend.SetMute(ctx, true)
	if err != nil || !speaker.Muted() {
		t.Fatalf("a second SetMute(true) must stay muted: %v", err)
	}

	err = backend.SetMute(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := backend.Volume(ctx); v != 40 {
		t.Fatalf("unmuted Volume = %d, want 40", v)
	}
}

// A folder plays through: the track that ends starts the next, the Next key walks it, and
// a controller's own URL ends the queue instead of being played over.
//
//nolint:paralleltest // changes package-wide state other tests read: upnpFolder, upnpFolderMu
func TestUPnPFolderPlaysThrough(t *testing.T) {
	ctx := t.Context()

	upnpFolder.Clear()
	defer upnpFolder.Clear()

	audio := "object.item.audioItem.musicTrack"
	folder := []upnp.Item{
		{ID: "", Title: "1", Class: audio, Artist: "", Album: "", Art: "", URL: "http://192.168.1.9/1.mp3"},
		{ID: "", Title: "2", Class: audio, Artist: "", Album: "", Art: "", URL: "http://192.168.1.9/2.mp3"},
		{ID: "", Title: "3", Class: audio, Artist: "", Album: "", Art: "", URL: "http://192.168.1.9/3.mp3"},
	}

	speaker := speakerstate.New()

	err := speaker.Select(ctx, sourceUPnP, "", folder[0].URL, "1", "", "")
	if err != nil {
		t.Fatal(err)
	}

	upnpFolder.Set(folder, folder[0].URL)

	go runUPnPFolder(ctx, speaker, slog.Default())

	waitOn := func(want string) {
		t.Helper()

		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if np, _ := speaker.NowPlaying(ctx); np != nil && np.Location == want && np.PlayStatus != "STOP_STATE" {
				return
			}
		}

		np, _ := speaker.NowPlaying(ctx)
		t.Fatalf("speaker on %+v, want %s", np, want)
	}

	speaker.FailCurrentPlayback() // the same stop a track's clean end leaves
	waitOn(folder[1].URL)

	err = upnpFolderStep(ctx, speaker, 1)
	if err != nil {
		t.Fatal(err)
	}

	waitOn(folder[2].URL)

	own := "http://192.168.1.9/own.mp3"

	err = speaker.Select(ctx, sourceUPnP, "", own, "own", "", "")
	if err != nil {
		t.Fatal(err)
	}

	for end := time.Now().Add(5 * time.Second); upnpFolder.Keep(folder[2].URL) && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}

	speaker.FailCurrentPlayback()
	time.Sleep(50 * time.Millisecond)

	if np, _ := speaker.NowPlaying(ctx); np.Location != own {
		t.Fatalf("a controller's track must not be followed by the folder: on %s", np.Location)
	}
}

// The album goes with the track and does not outlive it.
func TestUPnPAlbumFollowsTheTrack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	speaker := speakerstate.New()

	backend := upnpBackend{sp: speaker}

	meta := upnp.Meta{Title: "a", Artist: "Band", Album: "Record", Art: "", Duration: 0}

	err := backend.PlayURL(ctx, "http://192.168.1.9/a.flac", meta, 0)
	if err != nil {
		t.Fatal(err)
	}

	if np, _ := speaker.NowPlaying(ctx); np.Album != "Record" {
		t.Fatalf("album = %q", np.Album)
	}

	err = backend.PlayURL(ctx, "http://192.168.1.9/b.flac", upnpTitleMeta("b"), 0)
	if err != nil {
		t.Fatal(err)
	}

	if np, _ := speaker.NowPlaying(ctx); np.Album != "" {
		t.Fatalf("album %q outlived its track", np.Album)
	}
}

// The renderer's routes share port 80 with the API, so they sit behind its Host and CSRF
// guard too: without it a rebound web page could play and set the volume.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
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
