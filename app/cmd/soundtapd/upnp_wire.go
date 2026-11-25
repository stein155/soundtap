// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"crypto/sha1" //nolint:gosec // the UDN's name hash, not security: see upnpUDN
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/upnp"
)

// playStopped is the play status of a source that has stopped.
const playStopped = "STOP_STATE"

// upnpFolder is the media-server folder playing, as a queue: tracks follow each other and the
// Next and Previous keys walk it. upnpFolderMu keeps a step and its Select together, or the
// watcher would see the queue on the next track and the speaker still on this one.
//
//nolint:gochecknoglobals // process-wide state: the transport keys, the folder watcher and the API share the queue
var (
	upnpFolder   = &upnp.Queue{}
	upnpFolderMu sync.Mutex
)

// upnpFolderStep plays the track delta away in the folder. Without one it does nothing.
func upnpFolderStep(ctx context.Context, speaker *speakerstate.Speaker, delta int) error {
	upnpFolderMu.Lock()
	defer upnpFolderMu.Unlock()

	return stepFolder(ctx, speaker, delta)
}

func stepFolder(ctx context.Context, speaker *speakerstate.Speaker, delta int) error {
	item, ok := upnpFolder.Step(delta)
	if !ok {
		return nil // no folder playing
	}

	return playFolderItem(ctx, speaker, &item)
}

// playFolderItem plays a track of the folder.
func playFolderItem(ctx context.Context, speaker *speakerstate.Speaker, item *upnp.Item) error {
	if !upnp.MediaURL(ctx, item.URL) {
		upnpFolder.Clear()

		return nil
	}

	meta := upnp.Meta{Title: item.Title, Artist: item.Artist, Album: item.Album, Art: item.Art, Duration: 0}

	return upnpBackend{sp: speaker}.PlayURL(ctx, item.URL, meta, 0)
}

// runUPnPFolder starts the next track when one ends, or fails, and ends the queue once the
// speaker plays anything else: another source, or a controller's own URL. Standby is a
// source change, so a stop on the UPnP source only ever means the track is over.
func runUPnPFolder(ctx context.Context, speaker *speakerstate.Speaker, log *slog.Logger) {
	for {
		changed := speaker.Changed()

		upnpFolderMu.Lock()
		advanceUPnPFolder(ctx, speaker, log)
		upnpFolderMu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

// advanceUPnPFolder starts the folder's next track when the current one stopped, and ends
// the queue when the speaker plays anything else. The caller holds upnpFolderMu.
func advanceUPnPFolder(ctx context.Context, speaker *speakerstate.Speaker, log *slog.Logger) {
	nowPlaying, err := speaker.NowPlaying(ctx)
	if err != nil || nowPlaying == nil {
		return
	}

	location := nowPlaying.Location
	if nowPlaying.Source != sourceUPnP {
		location = ""
	}

	if !upnpFolder.Keep(location) || nowPlaying.PlayStatus != playStopped {
		return
	}

	item, ok := upnpFolder.Advance() // repeat-track plays the same one again
	if !ok {
		return
	}

	err = playFolderItem(ctx, speaker, &item)
	if err != nil {
		log.Warn("upnp folder: next track", "err", err)
	}
}

// upnpBackend adapts speakerstate to the UPnP renderer: a controller's SetAVTransportURI
// becomes the UPNP source (the player streams the URL through the arbiter bus), Stop
// goes to standby, and transport state reflects the live source.
type upnpBackend struct{ sp *speakerstate.Speaker }

// PlayURL selects the UPNP source on uri, from offset into it.
func (b upnpBackend) PlayURL(ctx context.Context, uri string, meta upnp.Meta, offset time.Duration) error {
	b.sp.Wake(ctx)

	if meta.Title == "" {
		meta.Title = "UPnP"
	}
	// source=UPNP, location=uri -> runPlayer ffmpegs it to the bus.
	err := b.sp.SelectAt(ctx, sourceUPnP, uri, meta.Title, meta.Art, offset)
	if err != nil {
		return fmt.Errorf("select the UPnP stream: %w", err)
	}

	if meta.Artist != "" {
		err := b.sp.SetTrackMeta(ctx, sourceUPnP, "", meta.Artist, "")
		if err != nil {
			return fmt.Errorf("UPnP track metadata: %w", err)
		}
	}

	b.sp.SetAlbum(sourceUPnP, meta.Album)

	return nil
}

// Stop releases the UPNP source.
func (b upnpBackend) Stop(ctx context.Context) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("UPnP stop: %w", err)
	}
	// Release UPNP specifically (stale-safe + resume-aware): a late Stop from a controller
	// after the user switched sources must not knock the speaker into standby, and it must
	// not corrupt lastSource the way SetSource(sourceStandby) did.
	err = b.sp.ReleaseSource(ctx, sourceUPnP)
	if err != nil {
		return fmt.Errorf("release the UPnP source: %w", err)
	}

	return nil
}

// Pause presses PAUSE while UPNP plays: the player stops ffmpeg and keeps the source, so
// the controller sees PAUSED_PLAYBACK and its Play streams the URL again. Before, Pause
// went to standby and Music Assistant showed the player as stopped. Like Stop, a
// late Pause after the user switched sources leaves the new source alone.
func (b upnpBackend) Pause(ctx context.Context) error {
	nowPlaying, err := b.sp.NowPlaying(ctx)
	if err != nil {
		return fmt.Errorf("UPnP pause: %w", err)
	}

	if nowPlaying == nil || nowPlaying.Source != sourceUPnP || nowPlaying.PlayStatus == statusPaused {
		return nil
	}

	err = b.sp.Key(ctx, "PAUSE")
	if err != nil {
		return fmt.Errorf("UPnP pause: %w", err)
	}

	return nil
}

// TransportState maps the UPNP source's play status to the AVTransport state. A
// stream that failed keeps UPNP selected with STOP_STATE: that is STOPPED, so the
// controller shows it and its Play retries the stream.
func (b upnpBackend) TransportState(ctx context.Context) string {
	nowPlaying, err := b.sp.NowPlaying(ctx)
	if err != nil || nowPlaying == nil {
		return "STOPPED"
	}

	if nowPlaying.Source != sourceUPnP {
		// Another source owns the speaker: nothing of the controller's is loaded, and a
		// queued next track must not take the speaker back (Renderer.advance).
		return "NO_MEDIA_PRESENT"
	}

	switch nowPlaying.PlayStatus {
	case "PLAY_STATE":
		return "PLAYING"
	case "BUFFERING_STATE":
		return "TRANSITIONING"
	case statusPaused:
		return "PAUSED_PLAYBACK"
	default:
		return "STOPPED"
	}
}

// Volume is what is heard, so a controller sees a mute as volume 0 and GetMute follows.
func (b upnpBackend) Volume(ctx context.Context) (int, error) {
	volume, err := b.sp.Volume(ctx)
	if b.sp.Muted() {
		volume = 0
	}

	if err != nil {
		return volume, fmt.Errorf("UPnP volume: %w", err)
	}

	return volume, nil
}

// SetVolume sets the speaker's volume.
func (b upnpBackend) SetVolume(ctx context.Context, level int) error {
	err := b.sp.SetVolume(ctx, level)
	if err != nil {
		return fmt.Errorf("UPnP set volume: %w", err)
	}

	return nil
}

// SetMute goes through the MUTE key, which keeps the level to come back to; the key is a
// toggle, so it is only pressed when the state differs.
//
//nolint:revive // flag-parameter: upnp.Backend's method, on is the mute state asked for
func (b upnpBackend) SetMute(ctx context.Context, on bool) error {
	if b.sp.Muted() == on {
		return nil
	}

	err := b.sp.Key(ctx, "MUTE")
	if err != nil {
		return fmt.Errorf("UPnP mute: %w", err)
	}

	return nil
}

// upnpUDN derives a stable MediaRenderer UUID from the device id. SHA-1 names it, as it
// always has: a controller knows the renderer by this UUID, so it must not change.
func upnpUDN(deviceID string) string {
	sum := sha1.Sum([]byte("soundtap-upnp:" + deviceID)) //nolint:gosec // a stable name, not security
	digest := hex.EncodeToString(sum[:16])

	return "uuid:" + digest[0:8] + "-" + digest[8:12] + "-" + digest[12:16] + "-" + digest[16:20] + "-" + digest[20:32]
}

// startUPnP wires and starts the MediaRenderer: HTTP routes on mux behind guard, SSDP in a
// goroutine.
func startUPnP(
	ctx context.Context, speaker *speakerstate.Speaker, mux *http.ServeMux, guard func(http.Handler) http.Handler,
	name, port string, log *slog.Logger,
) *upnp.Renderer {
	renderer := &upnp.Renderer{
		UDN:     upnpUDN(speaker.DeviceID),
		Name:    name,
		IP:      "", // empty: the renderer looks up the LAN address when it advertises
		Port:    port,
		Backend: upnpBackend{speaker},
		// /run: a soundtapd restart keeps the subscriptions, a reboot starts clean.
		SubsFile: "/run/soundtap/upnp-subscriptions.json",
		OnPosition: func(position, duration time.Duration) {
			speaker.SetPosition(sourceUPnP, int(position.Milliseconds()), int(duration.Milliseconds()))
		},
	}
	routes := http.NewServeMux()
	renderer.Routes(routes)
	mux.Handle("/upnp/", guard(routes))

	go renderer.Serve(ctx, func(msg string, kv ...any) { log.Info(msg, kv...) })

	log.Info("upnp renderer up", "udn", renderer.UDN)

	return renderer
}
