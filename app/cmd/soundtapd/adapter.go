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
var adapterSession atomic.Pointer[abl.Session]

// mixerLevel is the level the mixer plays at: full scale while the host product or the
// soundbar processor applies the volume (stock VOLUME_FORWARD_ABL, ADR 0109), the volume
// itself otherwise.
func mixerLevel(output int) int {
	if s := adapterSession.Load(); s != nil && s.OwnsVolume() {
		return 100
	}
	return soundbar.MixerLevel(output)
}

// adapterSpeaker is the speaker as the link sees it.
type adapterSpeaker struct {
	ctx context.Context
	sp  *speakerstate.Speaker
}

func (a adapterSpeaker) Playing() bool {
	st := a.sp.StatusSnapshot()
	return !st.Standby && st.Source != ""
}

func (a adapterSpeaker) Volume() int { return a.sp.StatusSnapshot().Volume }

func (a adapterSpeaker) SetVolume(level int) { _ = a.sp.SetVolume(a.ctx, level) }

// HostLeft follows the host to its own source or off, as stock does: SoundTap stops.
func (a adapterSpeaker) HostLeft() { _ = a.sp.EnterStandby(a.ctx) }

// runAdapterLink runs the SoundTouch adapter's link to its host product until ctx ends: it
// finds the host, selects the adapter on it while SoundTap plays, and hands the volume over
// (docs/hardware/lisa/README.md). A model without the link never calls it.
func runAdapterLink(ctx context.Context, sp *speakerstate.Speaker, btPair func(), log *slog.Logger) {
	port, err := abl.OpenSerial(ctx, board.HostLinkDev) // the module's UART0 (stock /dev/ttyO0)
	if err != nil {
		log.Error("adapter link", "err", err)
		return
	}
	context.AfterFunc(ctx, func() { _ = port.Close() })
	s := abl.NewSession(abl.NewMaster(port, nil), adapterSpeaker{ctx: ctx, sp: sp}, log)
	// The adapter takes its host's name, as stock's product XML for that host does.
	s.OnHost = func(h abl.Host) { sp.SetType(board.Type + " (" + h.Product() + ")") }
	s.Bluetooth = board.BTUart != ""
	s.OnKey = func(key string, preset int, store bool) { adapterKey(ctx, sp, btPair, key, preset, store, log) }
	s.OnSelect = func(preset int) {
		if preset > 0 {
			_ = sp.PlayPreset(ctx, preset)
		} else if sp.StatusSnapshot().Standby {
			sp.ApplyKey("POWER") // wakes into the last source, as stock's TellBoseAppToSelectLastSoundtouchSource
		}
	}
	// The hook's level is 0 while muted: the host gets the volume itself and the mute apart.
	sp.OnVolume(func(int) {
		st := sp.StatusSnapshot()
		s.SetVolume(st.Volume)
		s.SetMute(st.Muted)
	})
	adapterSession.Store(s)
	s.Run(ctx)
}

// adapterKey does what a key of the host's remote asks: a preset played or stored, a
// transport key, Bluetooth pairing or clearing the pairings.
func adapterKey(ctx context.Context, sp *speakerstate.Speaker, btPair func(), key string, preset int, store bool, log *slog.Logger) {
	switch {
	case preset > 0 && store:
		if ok, err := sp.StoreCurrentAsPreset(fmt.Sprintf("PRESET_%d", preset)); err != nil || !ok {
			log.Warn("adapter: host remote store preset", "preset", preset, "stored", ok, "err", err)
		}
	case preset > 0:
		_ = sp.PlayPreset(ctx, preset)
	case key == abl.KeyBTPair:
		if btPair != nil {
			btPair()
		}
	case key == abl.KeyBTClear:
		clearBluetoothPairings(log)
	case key != "":
		sp.ApplyKey(key)
	}
}
