// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/stein155/soundtap/app/internal/abl"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// adapterSession is the running link to the adapter's host, nil until it runs.
//
//nolint:gochecknoglobals // process-wide state: the link sets it, the mixer and API read it
var adapterSession atomic.Pointer[abl.Session]

// adapterFullScale is the mixer level while the host or the soundbar applies the volume.
const adapterFullScale = 100

// mixerLevel is the level the mixer plays at: full scale while the host product or the
// soundbar processor applies the volume (stock VOLUME_FORWARD_ABL, ADR 0109), the volume
// itself otherwise.
func mixerLevel(output int) int {
	if session := adapterSession.Load(); session != nil && session.OwnsVolume() {
		return adapterFullScale
	}

	return soundbar.MixerLevel(output)
}

// adapterSpeaker is the speaker as the link sees it. abl.Speaker's methods take no context,
// so it carries the link's own.
type adapterSpeaker struct {
	ctx     context.Context //nolint:containedctx // abl.Speaker's methods take none: the link's lifetime
	speaker *speakerstate.Speaker
}

func (a adapterSpeaker) Playing() bool {
	st := a.speaker.StatusSnapshot()

	return !st.Standby && st.Source != ""
}

func (a adapterSpeaker) Volume() int { return a.speaker.StatusSnapshot().Volume }

func (a adapterSpeaker) SetVolume(level int) { _ = a.speaker.SetVolume(a.ctx, level) }

// HostLeft follows the host to its own source or off, as stock does: SoundTap stops.
func (a adapterSpeaker) HostLeft() { _ = a.speaker.EnterStandby(a.ctx) }

// runAdapterLink runs the SoundTouch adapter's link to its host product until ctx ends: it
// finds the host, selects the adapter on it while SoundTap plays, and hands the volume over
// (docs/hardware/lisa/README.md). A model without the link never calls it.
func runAdapterLink(ctx context.Context, speaker *speakerstate.Speaker, btPair func(), log *slog.Logger) {
	port, err := abl.OpenSerial(ctx, board.HostLinkDev) // the module's UART0 (stock /dev/ttyO0)
	if err != nil {
		log.Error("adapter link", "err", err)

		return
	}

	context.AfterFunc(ctx, func() { _ = port.Close() })
	session := abl.NewSession(abl.NewMaster(port, nil), adapterSpeaker{ctx: ctx, speaker: speaker}, log)
	// The adapter takes its host's name, as stock's product XML for that host does.
	session.OnHost = func(host abl.Host) { speaker.SetType(board.Type + " (" + host.Product() + ")") }
	session.Bluetooth = board.BTUart != ""
	session.OnKey = func(key string, preset int, store bool) {
		hostKey := adapterHostKey{key: key, preset: preset, store: store}
		adapterKey(ctx, speaker, btPair, hostKey, log)
	}
	session.OnSelect = func(preset int) {
		if preset > 0 {
			_ = speaker.PlayPreset(ctx, preset)
		} else if speaker.StatusSnapshot().Standby {
			speaker.ApplyKey("POWER") // wakes into the last source, as stock's TellBoseAppToSelectLastSoundtouchSource
		}
	}
	// The hook's level is 0 while muted: the host gets the volume itself and the mute apart.
	speaker.OnVolume(func(int) {
		st := speaker.StatusSnapshot()
		session.SetVolume(st.Volume)
		session.SetMute(st.Muted)
	})
	adapterSession.Store(session)
	session.Run(ctx)
}

// adapterKey does what a key of the host's remote asks: a preset played or stored, a
// transport key, Bluetooth pairing or clearing the pairings.
func adapterKey(
	ctx context.Context, speaker *speakerstate.Speaker, btPair func(), hostKey adapterHostKey, log *slog.Logger,
) {
	switch {
	case hostKey.preset > 0 && hostKey.store:
		ok, err := speaker.StoreCurrentAsPreset(fmt.Sprintf("PRESET_%d", hostKey.preset))
		if err != nil || !ok {
			log.Warn("adapter: host remote store preset", "preset", hostKey.preset, "stored", ok, "err", err)
		}
	case hostKey.preset > 0:
		_ = speaker.PlayPreset(ctx, hostKey.preset)
	case hostKey.key == abl.KeyBTPair:
		if btPair != nil {
			btPair()
		}
	case hostKey.key == abl.KeyBTClear:
		// Not cut short when the link ends: the owner asked for the pairings to go.
		clearBluetoothPairings(context.WithoutCancel(ctx), log)
	case hostKey.key != "":
		speaker.ApplyKey(hostKey.key)
	default: // no key and no preset: nothing to do
	}
}

// adapterHostKey is one key of the host's remote as the link reports it: a key name, or a
// preset to play or (store) to store.
type adapterHostKey struct {
	key    string
	preset int
	store  bool
}
