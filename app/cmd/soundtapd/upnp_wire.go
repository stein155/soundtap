// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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
var (
	upnpFolder   = &upnp.Queue{}
	upnpFolderMu sync.Mutex
)

// upnpFolderStep plays the track delta away in the folder. Without one it does nothing.
func upnpFolderStep(ctx context.Context, sp *speakerstate.Speaker, delta int) error {
	upnpFolderMu.Lock()
	defer upnpFolderMu.Unlock()
	return stepFolder(ctx, sp, delta)
}

func stepFolder(ctx context.Context, sp *speakerstate.Speaker, delta int) error {
	it, ok := upnpFolder.Step(delta)
	return playFolderItem(ctx, sp, it, ok)
}

// playFolderItem plays a track of the folder; ok false means there is none.
func playFolderItem(ctx context.Context, sp *speakerstate.Speaker, it upnp.Item, ok bool) error {
	if !ok {
		return nil
	}
	if !upnp.MediaURL(ctx, it.URL) {
		upnpFolder.Clear()
		return nil
	}
	return upnpBackend{sp: sp}.PlayURL(ctx, it.URL, upnp.Meta{Title: it.Title, Artist: it.Artist, Album: it.Album, Art: it.Art}, 0)
}

// runUPnPFolder starts the next track when one ends, or fails, and ends the queue once the
// speaker plays anything else: another source, or a controller's own URL. Standby is a
// source change, so a stop on the UPnP source only ever means the track is over.
func runUPnPFolder(ctx context.Context, sp *speakerstate.Speaker, log *slog.Logger) {
	for {
		changed := sp.Changed()
		upnpFolderMu.Lock()
		if np, err := sp.NowPlaying(ctx); err == nil && np != nil {
			location := np.Location
			if np.Source != sourceUPnP {
				location = ""
			}
			if upnpFolder.Keep(location) && np.PlayStatus == playStopped {
				it, ok := upnpFolder.Advance() // repeat-track plays the same one again
				if err := playFolderItem(ctx, sp, it, ok); err != nil {
					log.Warn("upnp folder: next track", "err", err)
				}
			}
		}
		upnpFolderMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

// upnpBackend adapts speakerstate to the UPnP renderer: a controller's SetAVTransportURI
// becomes the UPNP source (the player streams the URL through the arbiter bus), Stop
// goes to standby, and transport state reflects the live source.
type upnpBackend struct{ sp *speakerstate.Speaker }

func (b upnpBackend) PlayURL(ctx context.Context, uri string, m upnp.Meta, at time.Duration) error {
	b.sp.Wake(ctx)
	if m.Title == "" {
		m.Title = "UPnP"
	}
	// source=UPNP, location=uri -> runPlayer ffmpegs it to the bus.
	if err := b.sp.SelectAt(ctx, sourceUPnP, uri, m.Title, m.Art, at); err != nil {
		return err
	}
	if m.Artist != "" {
		if err := b.sp.SetTrackMeta(ctx, sourceUPnP, "", m.Artist, ""); err != nil {
			return err
		}
	}
	b.sp.SetAlbum(sourceUPnP, m.Album)
	return nil
}

func (b upnpBackend) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Release UPNP specifically (stale-safe + resume-aware): a late Stop from a controller
	// after the user switched sources must not knock the speaker into standby, and it must
	// not corrupt lastSource the way SetSource(sourceStandby) did.
	return b.sp.ReleaseSource(ctx, sourceUPnP)
}

// Pause presses PAUSE while UPNP plays: the player stops ffmpeg and keeps the source, so
// the controller sees PAUSED_PLAYBACK and its Play streams the URL again. Before, Pause
// went to standby and Music Assistant showed the player as stopped. Like Stop, a
// late Pause after the user switched sources leaves the new source alone.
func (b upnpBackend) Pause(ctx context.Context) error {
	np, err := b.sp.NowPlaying(ctx)
	if err != nil {
		return err
	}
	if np == nil || np.Source != sourceUPnP || np.PlayStatus == statusPaused {
		return nil
	}
	return b.sp.Key(ctx, "PAUSE")
}

// TransportState maps the UPNP source's play status to the AVTransport state. A
// stream that failed keeps UPNP selected with STOP_STATE: that is STOPPED, so the
// controller shows it and its Play retries the stream.
func (b upnpBackend) TransportState(ctx context.Context) string {
	np, err := b.sp.NowPlaying(ctx)
	if err != nil || np == nil {
		return "STOPPED"
	}
	if np.Source != sourceUPnP {
		// Another source owns the speaker: nothing of the controller's is loaded, and a
		// queued next track must not take the speaker back (Renderer.advance).
		return "NO_MEDIA_PRESENT"
	}
	switch np.PlayStatus {
	case "PLAY_STATE":
		return "PLAYING"
	case "BUFFERING_STATE":
		return "TRANSITIONING"
	case statusPaused:
		return "PAUSED_PLAYBACK"
	}
	return "STOPPED"
}

// Volume is what is heard, so a controller sees a mute as volume 0 and GetMute follows.
func (b upnpBackend) Volume(ctx context.Context) (int, error) {
	v, err := b.sp.Volume(ctx)
	if b.sp.Muted() {
		v = 0
	}
	return v, err
}

func (b upnpBackend) SetVolume(ctx context.Context, level int) error {
	return b.sp.SetVolume(ctx, level)
}

// SetMute goes through the MUTE key, which keeps the level to come back to; the key is a
// toggle, so it is only pressed when the state differs.
func (b upnpBackend) SetMute(ctx context.Context, on bool) error {
	if b.sp.Muted() == on {
		return nil
	}
	return b.sp.Key(ctx, "MUTE")
}

// upnpUDN derives a stable MediaRenderer UUID from the device id.
func upnpUDN(deviceID string) string {
	sum := sha1.Sum([]byte("soundtap-upnp:" + deviceID))
	h := hex.EncodeToString(sum[:16])
	return "uuid:" + h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// startUPnP wires and starts the MediaRenderer: HTTP routes on mux behind guard, SSDP in a
// goroutine.
func startUPnP(ctx context.Context, sp *speakerstate.Speaker, mux *http.ServeMux, guard func(http.Handler) http.Handler, name, port string, log *slog.Logger) *upnp.Renderer {
	r := &upnp.Renderer{
		UDN:     upnpUDN(sp.DeviceID),
		Name:    name,
		Port:    port,
		Backend: upnpBackend{sp},
		// /run: a soundtapd restart keeps the subscriptions, a reboot starts clean.
		SubsFile: "/run/soundtap/upnp-subscriptions.json",
		OnPosition: func(position, duration time.Duration) {
			sp.SetPosition(sourceUPnP, int(position.Milliseconds()), int(duration.Milliseconds()))
		},
	}
	routes := http.NewServeMux()
	r.Routes(routes)
	mux.Handle("/upnp/", guard(routes))
	go r.Serve(ctx, func(msg string, kv ...any) { log.Info(msg, kv...) })
	log.Info("upnp renderer up", "udn", r.UDN)
	return r
}
