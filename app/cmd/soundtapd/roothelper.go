// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"fmt"
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
//
//nolint:gochecknoglobals // process-wide state: set once at start-up, read by every root-only step
var rootHelper *rootd.Client

// useRootHelper drops the ambient capabilities soundtapd arrived with, so no child inherits
// them, and points every root-only step at soundtap-root when init named it. It runs
// before anything is started.
func useRootHelper(log *slog.Logger) {
	if os.Geteuid() != 0 {
		err := childproc.ClearAmbient()
		if err != nil {
			log.Warn("ambient capabilities stay", "err", err)
		}
	}

	helper := rootd.FromEnv()
	if helper == nil || devMode.Load() {
		return
	}

	rootHelper = helper
	log.Info("root steps go through soundtap-root", "socket", helper.Path, "uid", os.Geteuid())

	flash.VerifyDir = rootd.MountDir
	hw.ExportGPIO = helper.ExportGPIO
	oled.ExportGPIO = helper.ExportGPIO
	bco.Power = func() (func(on bool) error, func(), error) { return helper.BCOPower, func() {}, nil }
	setHostname = func(b []byte) error { return helper.SetHostname(string(b)) }
}

// rebootSystem reboots the speaker: through soundtap-root, or BusyBox's reboot as root.
func rebootSystem() error {
	return rebootSystemContext(context.Background())
}

// rebootSystemContext is rebootSystem with BusyBox's reboot run within ctx.
func rebootSystemContext(ctx context.Context) error {
	if rootHelper != nil {
		err := rootHelper.Reboot()
		if err != nil {
			return fmt.Errorf("reboot through soundtap-root: %w", err)
		}

		return nil
	}

	err := exec.CommandContext(ctx, "reboot").Run()
	if err != nil {
		return fmt.Errorf("reboot: %w", err)
	}

	return nil
}

// runBTCtlVerb runs bt-ctl.sh verb within ctx: through soundtap-root, which as root reads
// BlueZ's pairing files the way stock's list did, or here. A non-zero exit is an error
// that has ExitCode.
func runBTCtlVerb(ctx context.Context, verb string) error {
	if rootHelper != nil {
		err := rootHelper.BTCtl(verb)
		if err != nil {
			return fmt.Errorf("bt-ctl %s through soundtap-root: %w", verb, err)
		}

		return nil
	}

	err := exec.CommandContext(ctx, "sh", btCtl, verb).Run()
	if err != nil {
		return fmt.Errorf("bt-ctl %s: %w", verb, err)
	}

	return nil
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
