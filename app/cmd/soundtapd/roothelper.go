// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"

	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/childproc"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/rootd"
	"github.com/stein155/soundtap/app/oled"
)

// rootHelper is soundtap-root (ADR 0066), which init.d/soundtap starts beside soundtapd
// when soundtapd runs as its own user. nil when soundtapd does the work itself: a
// development run, a test, or an OS that starts it as root.
var rootHelper *rootd.Client

// useRootHelper drops the ambient capabilities soundtapd arrived with, so no child inherits
// them, and points every root-only step at soundtap-root when init named it. It runs
// before anything is started.
func useRootHelper(log *slog.Logger) {
	if os.Geteuid() != 0 {
		if err := childproc.ClearAmbient(); err != nil {
			log.Warn("ambient capabilities stay", "err", err)
		}
	}
	h := rootd.FromEnv()
	if h == nil || devMode.Load() {
		return
	}
	rootHelper = h
	log.Info("root steps go through soundtap-root", "socket", h.Path, "uid", os.Geteuid())
	flash.VerifyDir = rootd.MountDir
	hw.ExportGPIO = h.ExportGPIO
	oled.ExportGPIO = h.ExportGPIO
	bco.Power = func() (func(on bool) error, func(), error) { return h.BCOPower, func() {}, nil }
	setHostname = func(b []byte) error { return h.SetHostname(string(b)) }
}

// rebootSystem reboots the speaker: through soundtap-root, or BusyBox's reboot as root.
func rebootSystem() error {
	if rootHelper != nil {
		return rootHelper.Reboot()
	}
	return exec.CommandContext(context.Background(), "reboot").Run()
}

// runBTCtlVerb runs bt-ctl.sh verb within ctx: through soundtap-root, which as root reads
// BlueZ's pairing files the way stock's list did, or here. A non-zero exit is an error
// that has ExitCode.
func runBTCtlVerb(ctx context.Context, verb string) error {
	if rootHelper != nil {
		return rootHelper.BTCtl(verb)
	}
	return exec.CommandContext(ctx, "sh", btCtl, verb).Run()
}

// exitCode is the exit status err carries, or -1.
func exitCode(err error) int {
	var e interface{ ExitCode() int }
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	return -1
}

// mayChown reports whether this process can hand files to another user: root can,
// soundtapd's own user cannot, and init.d/soundtap hands the media daemons their trees.
func mayChown() bool { return os.Geteuid() == 0 }
