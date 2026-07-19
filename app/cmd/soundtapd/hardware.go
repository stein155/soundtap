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
var amplifier = hw.NewAmp(board.Name, envDefault("MUTE_GPIO", board.MuteGPIO), envDefault("AMP_POWER_GPIO", board.AmpPowerGPIO))

// ampLines is how runPlayer and trackOwnership drive the mute and amp-power lines. A
// variable so a test can record the order of the edges, which is what a pop is read
// from.
var ampLines = struct {
	mute  func(muted bool, log *slog.Logger)
	power func(powered bool, log *slog.Logger) bool
}{
	func(muted bool, log *slog.Logger) { amplifier.SetMute(muted, log) },
	func(powered bool, log *slog.Logger) bool { return amplifier.SetPowered(powered, log) },
}

// ampFault watches the amplifier's fault input, where the model has one with hardware
// evidence behind it.
var ampFault = hw.NewAmpFault(board.AmpFaultGPIO)

// ampFaulted reports whether the amp fault is latched. Read by trackOwnership on every
// mute decision, by the panel, and surfaced in the web health status. A variable so a
// test can assert a fault.
var ampFaulted = ampFault.Active

// runAmpFault watches the fault input; a fault mutes at once.
func runAmpFault(ctx context.Context, log *slog.Logger) {
	ampFault.Run(ctx, board.Name, func() { amplifier.SetMute(true, log) }, log)
}

// daemonUp is false until the HTTP listener is bound — the same moment the boot
// watchdog is disarmed. The LED loop starts long before that, and "booting" is exactly
// the state the front panel had no way to show.
var daemonUp atomic.Bool

// firmwareActive is set while a firmware install is writing to NAND, so the lamps and the
// panel can show a distinct pattern instead of the normal source/standby state. It is set
// from the web package through web.FirmwareOptions.Active (firmwareOptions).
var firmwareActive atomic.Bool

// firmwareProgress is how far that install is, in percent.
var firmwareProgress atomic.Int64

// lampStatus is what the lamps report: the speaker's state and the daemon's own.
type lampStatus struct{ *speakerstate.Speaker }

func (lampStatus) Up() bool         { return daemonUp.Load() }
func (lampStatus) Confirming() bool { return confirmPending.Load() || chordCounting.Load() }
func (lampStatus) Updating() bool   { return firmwareActive.Load() }
func (lampStatus) HaveIP() bool     { return outboundIP() != "" }

// runLamps drives the front lamps of this model. The Wireless Link's panel is two
// bi-colour lamps with a PWM channel, which the seven-lamp loop cannot describe: same
// inputs, its own loop.
func runLamps(ctx context.Context, sp *speakerstate.Speaker, log *slog.Logger) {
	if board.Name == model.WLA {
		hw.RunWLALEDs(ctx, lampStatus{sp}, log)
		return
	}
	hw.RunLEDs(ctx, board.LEDDir, lampStatus{sp}, log)
}
