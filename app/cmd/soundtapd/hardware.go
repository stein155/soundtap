// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// board is the profile the whole daemon reads its hardware paths from; boardErr says why
// the model could not be identified, and then board is model.Unknown.
//
//nolint:gochecknoglobals // process-wide state: read everywhere, swapped by tests for another model
var board, boardErr = model.Current()

func exists(path string) bool {
	if path == "" {
		return false
	}

	_, err := os.Stat(path)

	return err == nil
}

// amplifier is the amplifier's mute and power lines. A variable so -dev and tests can
// replace it.
//
//nolint:gochecknoglobals // a test seam: -dev and tests put an amplifier without lines in
var amplifier = hw.NewAmp(board.Name,
	envDefault("MUTE_GPIO", board.MuteGPIO), envDefault("AMP_POWER_GPIO", board.AmpPowerGPIO))

// ampLines is how runPlayer and trackOwnership drive the mute and amp-power lines. A
// variable so a test can record the order of the edges, which is what a pop is read
// from.
//
//nolint:gochecknoglobals // a test seam: a test records the order of the edges
var ampLines = struct {
	mute  func(muted bool, log *slog.Logger)
	power func(powered bool, log *slog.Logger) bool
}{
	func(muted bool, log *slog.Logger) {
		if !muted && !mayUnmute() {
			muted = true
		}

		amplifier.SetMute(muted, log)
	},
	func(powered bool, log *slog.Logger) bool { return amplifier.SetPowered(powered, log) },
}

// unmuteGate, when set, must agree before the amplifier unmutes: the SA-5's controller says
// when its amplifier is on (sa5.go).
//
//nolint:gochecknoglobals // process-wide state: the SA-5 supervisor sets it, the mute path reads it
var unmuteGate atomic.Pointer[func() bool]

// mayUnmute is the gate's answer. A model whose controller must agree (the SA-5) stays muted
// until the controller is read, and for good when it cannot be.
func mayUnmute() bool {
	if g := unmuteGate.Load(); g != nil {
		return (*g)()
	}

	return !board.STM32Amp
}

// ampFault watches the amplifier's fault input, where the model has one with hardware
// evidence behind it.
//
//nolint:gochecknoglobals // process-wide state: one fault input, watched by runAmpFault, read by ampFaulted
var ampFault = hw.NewAmpFault(board.AmpFaultGPIO)

// ampFaulted reports whether the amp fault is latched. Read by trackOwnership on every
// mute decision, by the panel, and surfaced in the web health status. A variable so a
// test can assert a fault.
//
//nolint:gochecknoglobals // a test seam: a test asserts a fault through it
var ampFaulted = func() bool { return ampFault.Active() || sa5Faulted() }

// runAmpFault watches the fault input; a fault mutes at once.
func runAmpFault(ctx context.Context, log *slog.Logger) {
	ampFault.Run(ctx, board.Name, func() { amplifier.SetMute(true, log) }, log)
}

// daemonUp is false until the HTTP listener is bound — the same moment the boot
// watchdog is disarmed. The LED loop starts long before that, and "booting" is exactly
// the state the front panel had no way to show.
//
//nolint:gochecknoglobals // process-wide state: main sets it, the lamps and the panel read it
var daemonUp atomic.Bool

// firmwareActive is set while a firmware install is writing to NAND, so the lamps and the
// panel can show a distinct pattern instead of the normal source/standby state. It is set
// from the web package through web.FirmwareOptions.Active (firmwareOptions).
//
//nolint:gochecknoglobals // process-wide state: the install sets it, the lamps and the panel read it
var firmwareActive atomic.Bool

// firmwareProgress is how far that install is, in percent.
//
//nolint:gochecknoglobals // process-wide state: the install sets it, the panel reads it
var firmwareProgress atomic.Int64

// bootNetQuiet is set while the ST20's boot screen keeps quiet about a network that is not up
// yet: through the boot, the mark and a few seconds after it (panel.BootStatus.Quiet). The
// Wi-Fi lamp keeps quiet with it. Never set on a model without a panel.
//
//nolint:gochecknoglobals // process-wide state: the boot screen sets it, the lamps read it
var bootNetQuiet atomic.Bool

// lampStatus is what the lamps report: the speaker's state and the daemon's own.
type lampStatus struct{ *speakerstate.Speaker }

func (lampStatus) Up() bool         { return daemonUp.Load() }
func (lampStatus) Confirming() bool { return confirmPending.Load() || chordCounting.Load() }
func (lampStatus) Updating() bool   { return firmwareActive.Load() }
func (lampStatus) HaveIP() bool     { return outboundIP() != "" }
func (lampStatus) NetCard() bool    { return bcoUpdating() }
func (lampStatus) NetQuiet() bool   { return bootNetQuiet.Load() }

// runLamps drives the front lamps of this model. The Wireless Link's panel is two
// bi-colour lamps with a PWM channel, which the seven-lamp loop cannot describe: same
// inputs, its own loop.
func runLamps(ctx context.Context, speaker *speakerstate.Speaker, log *slog.Logger) {
	if board.Name == model.WLA {
		hw.RunWLALEDs(ctx, lampStatus{speaker}, log)

		return
	}

	hw.RunLEDs(ctx, board.LEDDir, lampStatus{speaker}, log)
}
