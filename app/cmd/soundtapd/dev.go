// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// devMode is -dev: soundtapd on a developer's computer (`make dev`, CONTRIBUTING.md). MODEL
// still picks the profile the API and the UI describe, but nothing reaches hardware or the
// LAN: no audio, buttons, lamps, panel or amplifier lines, no mDNS name or _soundtap._tcp
// service, no hostname, no Wi-Fi manager and no boot confirmation. What is left is speakerstate,
// the in-process speaker, behind the web API on the -listen address.
var devMode atomic.Bool

// devPlayDelay is how long the silent player lets a source buffer before it plays.
const devPlayDelay = 700 * time.Millisecond

// enterDevMode switches every outward-facing part off for -dev. It refuses a flashed
// speaker: there the hardware is real and the init script owns soundtapd.
func enterDevMode(opt *options, log *slog.Logger) {
	if onFirmware() {
		log.Error("-dev runs on a computer, not on a speaker (SOUNDTAP_FIRMWARE=1)")
		os.Exit(2)
	}
	devMode.Store(true)
	opt.noAudio = true
	// The amplifier lines are written at shutdown even without audio (lifecycle.audioOff).
	amplifier = hw.NewAmp(board.Name, "", "")
	// The host's name is the developer's, not the speaker's.
	setHostname = func([]byte) error { return nil }
	// The names bt.sh reads live under /run on a speaker; keep them with the home.
	currentNamePath = filepath.Join(opt.home, "run-name")
	setupNamePath = filepath.Join(opt.home, "run-default-name")
	log.Info("development run: simulated speaker, no audio, no hardware, nothing announced on the LAN",
		"model", board.Name)
}

// runDevPlayer is the -dev stand-in for the player: a source that starts buffering plays
// after devPlayDelay, as if audio flowed, and nothing is decoded or heard. Without it every
// selection would end in STOP_STATE (runNoAudioWatchdog) and the UI could not be worked on.
func runDevPlayer(ctx context.Context, sp *speakerstate.Speaker) {
	tick := time.NewTicker(devPlayDelay / 2)
	defer tick.Stop()
	var since time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		st := sp.StatusSnapshot()
		switch {
		case st.PlayStatus != playBuffering:
			since = time.Time{}
		case since.IsZero():
			since = time.Now()
		case time.Since(since) >= devPlayDelay:
			sp.ConfirmPlaying(sp.BeginBuffering(st.Source))
			since = time.Time{}
		}
	}
}
