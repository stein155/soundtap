// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"

	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/panel"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/oled"
)

// frontPanel is the ST20 panel while the speaker is on: the keys tell it what they do,
// and it shows the calls to the paired phones bt_follow.go makes.
//
//nolint:gochecknoglobals // process-wide state: the keys, the display drivers and Bluetooth share the one panel
var frontPanel = newFrontPanel()

func newFrontPanel() *panel.Panel {
	p := panel.New(holdToStore)
	p.Calling = btReconnecting.Load

	return p
}

// startBootScreen starts here rather than with the other panel drivers: its whole job is
// the window before those exist, and it must also run with -no-audio.
func startBootScreen(ctx context.Context, disp *display.Manager, set *settings.Store, log *slog.Logger) {
	goSafe("bootScreen", func() {
		panel.RunBootScreen(ctx, disp, panel.BootStatus{
			Up:      daemonUp.Load,
			Link:    func() hw.Link { return hw.ReadLink(func() bool { return outboundIP() != "" }) },
			Update:  updatePercent,
			NetCard: bco.Flashing,
			Faulted: func() bool { return ampFaulted() },
			Clock:   panel.Clock(set),
		}, log.With("comp", "oled-boot"))
	})
}

// updatePercent is how far an install is, or -1 when none runs.
func updatePercent() int {
	if !firmwareActive.Load() {
		return -1
	}

	return int(firmwareProgress.Load())
}

func displayPath() string {
	if devMode.Load() {
		return "" // the host's own framebuffer is not a speaker's panel
	}

	if board.OLED != "" && oled.Available(board.OLED) {
		return board.OLED
	}

	if boardErr != nil {
		return "" // an unidentified board gets no panel either (refuseUnknownHardware)
	}

	return "/dev/fb0"
}
