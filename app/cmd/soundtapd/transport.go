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

// transportCommandTimeout bounds one transport command on a source's control channel.
const transportCommandTimeout = 5 * time.Second

// transportUnavailableShown is how long the panel says a key does nothing on this source.
const transportUnavailableShown = 2 * time.Second

// transportArg is the argument a skipper gets for a transport action, "" for none.
func transportArg(action string) string {
	return map[string]string{
		"NEXT_TRACK": argNext, "PREV_TRACK": argPrev, "PAUSE": "pause", "PLAY": "play",
	}[action]
}

// transportHook turns NEXT_TRACK/PREV_TRACK/PAUSE/PLAY into a command on the source that owns the
// amp, through that source's control channel: AVRCP for Bluetooth, librespot's control
// API for Spotify (a playlist or album preset), DACP for AirPlay. A TuneIn station has
// no next track; sources without a skipper return without acting
// rather than pretending, and call unavailable so the panel can say so, as stock's does.
//
// skippers are injected so the mapping can be tested without a Bluetooth stack.
func transportHook(
	source func() string, skippers map[string]func(arg string) error, unavailable func(), log *slog.Logger,
) func(string) error {
	return func(action string) error {
		arg := transportArg(action)
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

		err := run(arg)
		if err != nil {
			log.Warn("skip failed", "action", action, "source", src, "err", err)

			return err
		}

		return nil
	}
}

// runBTCtl invokes the AVRCP shell with a deadline: bluetoothctl waits on D-Bus, and a
// wedged bluetoothd must not leave a button press hanging.
func runBTCtl(arg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), transportCommandTimeout)
	defer cancel()

	return runBTCtlVerb(ctx, arg)
}

// wireTransport routes transport keys to the source that owns them, or to the leader.
func wireTransport(
	ctx context.Context, speaker *speakerstate.Speaker, webSrv *web.Server, spot *spotify.Client,
	disp *display.Manager, log *slog.Logger,
) {
	source := func() string { return speaker.StatusSnapshot().Source }
	speaker.OnTransport(transportHook(source, map[string]func(string) error{
		sourceBluetooth: runBTCtl,
		sourceUPnP: func(arg string) error {
			sctx, cancel := context.WithTimeout(ctx, transportCommandTimeout)
			defer cancel()

			switch arg {
			case argNext:
				return upnpFolderStep(sctx, speaker, 1)
			case argPrev:
				return upnpFolderStep(sctx, speaker, -1)
			default:
				return nil
			}
		},
		sourceAirPlay: func(arg string) error {
			sctx, cancel := context.WithTimeout(ctx, transportCommandTimeout)
			defer cancel()

			return airplayKey(sctx, arg)
		},
		sourceSpotify: func(arg string) error {
			sctx, cancel := context.WithTimeout(ctx, transportCommandTimeout)
			defer cancel()

			return spot.Command(sctx, arg)
		},
	}, func() {
		frame := panel.Say("Action unavailable")
		disp.SetTransient(display.Content{Frame: frame, Fade: 0}, transportUnavailableShown)
	}, log.With("comp", "transport")))
	speaker.OnForwardKey(func(key string) bool {
		if !webSrv.ForwardsKey(key) {
			return false
		}

		err := webSrv.ForwardKey(ctx, key)
		if err != nil {
			log.Warn("key not forwarded to the leader, acting locally", "comp", "transport", "key", key, "err", err)

			return false
		}

		return true
	})
}
