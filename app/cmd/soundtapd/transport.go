// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/panel"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/web"
)

// btCtl is the AVRCP shell installed by os/overlay.
const btCtl = "/opt/soundtap/bt-ctl.sh"

// The transport arguments a skipper gets.
const (
	argNext = "next"
	argPrev = "prev"
)

// transportHook turns NEXT_TRACK/PREV_TRACK/PAUSE/PLAY into a command on the source that owns the
// amp, through that source's control channel: AVRCP for Bluetooth, librespot's control
// API for Spotify (a playlist or album preset), DACP for AirPlay. A TuneIn station has
// no next track; sources without a skipper return without acting
// rather than pretending, and call unavailable so the panel can say so, as stock's does.
//
// skippers are injected so the mapping can be tested without a Bluetooth stack.
func transportHook(source func() string, skippers map[string]func(arg string) error, unavailable func(), log *slog.Logger) func(string) error {
	return func(action string) error {
		arg := map[string]string{"NEXT_TRACK": argNext, "PREV_TRACK": argPrev, "PAUSE": "pause", "PLAY": "play"}[action] //nolint:goconst // key names
		if arg == "" {
			return nil
		}
		src := source()
		run, ok := skippers[src]
		if !ok {
			log.Debug("skip ignored: source cannot skip", "action", action, "source", src)
			if arg == argNext || arg == argPrev {
				unavailable()
			}
			return nil
		}
		if err := run(arg); err != nil {
			log.Warn("skip failed", "action", action, "source", src, "err", err)
			return err
		}
		return nil
	}
}

// runBTCtl invokes the AVRCP shell with a deadline: bluetoothctl waits on D-Bus, and a
// wedged bluetoothd must not leave a button press hanging.
func runBTCtl(arg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runBTCtlVerb(ctx, arg)
}

// wireTransport routes transport keys to the source that owns them, or to the leader.
func wireTransport(ctx context.Context, sp *speakerstate.Speaker, webSrv *web.Server, spot *spotify.Client, disp *display.Manager, log *slog.Logger) {
	sp.OnTransport(transportHook(func() string { return sp.StatusSnapshot().Source }, map[string]func(string) error{
		sourceBluetooth: runBTCtl,
		sourceUPnP: func(arg string) error {
			sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			switch arg {
			case argNext:
				return upnpFolderStep(sctx, sp, 1)
			case argPrev:
				return upnpFolderStep(sctx, sp, -1)
			}
			return nil
		},
		sourceAirPlay: func(arg string) error {
			sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return airplayKey(sctx, arg)
		},
		sourceSpotify: func(arg string) error {
			sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return spot.Command(sctx, arg)
		},
	}, func() {
		disp.SetTransient(display.Content{Frame: panel.Message(panel.Text("Action unavailable"))}, 2*time.Second)
	}, log.With("comp", "transport")))
	sp.OnForwardKey(func(key string) bool {
		if !webSrv.ForwardsKey(key) {
			return false
		}
		if err := webSrv.ForwardKey(ctx, key); err != nil {
			log.Warn("key not forwarded to the leader, acting locally", "comp", "transport", "key", key, "err", err)
			return false
		}
		return true
	})
}
