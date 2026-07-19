// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/oplock"
)

// bcoInstallDelay lets the boot settle before the BCO install step starts.
var bcoInstallDelay = 30 * time.Second

// bcoFlashLimit bounds one BCO flash attempt. Power cycle and loader menus, the TFTP upload
// (6 min), its confirmation, the XMODEM fallback and the new app's start fit in it. The boot
// confirmation waits for the attempt on the ST20, so this is part of its bound (ADR 0010).
const bcoFlashLimit = 15 * time.Minute

// bcoRebootWait bounds how long the reboot after a first install of our BCO app waits for the
// boot confirmation, so that a good slot is not charged a boot for it.
var bcoRebootWait = 2 * time.Minute

// wireBCOFlash installs Core's own BCO image on the ST20's front CPU on tty, once per image,
// and marks the gate once that step is over.
func wireBCOFlash(ctx context.Context, tty, wifiConf string, gate *bootGate, log *slog.Logger) {
	// The transcript goes to /run: the LAN is down while it runs, so a failed flash
	// is diagnosed afterwards, from this file.
	flashBCO := func(ctx context.Context, image []byte) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, bcoFlashLimit)
		defer cancel()
		f, err := os.Create("/run/soundtap/bco-flash.log")
		if err != nil {
			return "", err
		}
		defer f.Close()
		ver, err := bco.Flash(ctx, tty, image, f)
		if err != nil {
			counters.BCOFlashFailures.Add(1) // /api/health (ADR 0127)
		}
		return ver, err
	}
	// The own app (ADR 0031) is the only BCO firmware that brings up soundtap_bco's link.
	ownBCOApp := func() bool { _, err := os.Stat("/sys/class/net/bcolink0"); return err == nil }
	// The operations lock, not a one-time look at firmwareActive: a Core install, reboot
	// or app update starting during a flash must be refused.
	var op oplock.Lock
	lock := func() (func(), bool) {
		if !op.TryLock() {
			return nil, false
		}
		return op.Unlock, true
	}
	first, wasOwn := true, true
	attempt := func(ctx context.Context) (bool, error) {
		if first {
			first, wasOwn = false, ownBCOApp()
			if !wasOwn {
				// Bose's app is about to go, and the network it joined with it.
				adoptStockBCOWifi(ctx, wifiConf, func(ctx context.Context) (bco.StockProfile, error) {
					return bco.ReadStockProfile(ctx, tty)
				}, log)
			}
		}
		return bco.AutoInstall(ctx, bcoImage, bcoImageMarker, ownBCOApp, flashBCO, os.Stderr)
	}
	// Core's own BCO image (ADR 0031); let the boot settle first.
	goSafe("bcoInstall", func() {
		if !sleepCtx(ctx, bcoInstallDelay) {
			return
		}
		flashed := keepBCOInstalled(ctx, lock, attempt, ownBCOApp, sleepCtx, log)
		gate.bcoDone()
		// This boot found stock's BCO, so init.d/soundtap set up stock's static eth0 and no
		// Wi-Fi. The own app has no use for that: wlan0 stays down and the speaker stays
		// offline until something restarts it. Only a new boot takes the own app's network
		// path. The boot is confirmed first where it can be, or the reboot would count
		// against a slot that just proved itself.
		if flashed && !wasOwn {
			if !gate.awaitAttempt(ctx, bcoRebootWait) {
				log.Warn("rebooting after the BCO install before the boot was confirmed")
			}
			log.Info("BCO now runs the own app, rebooting to start its network")
			if err := rebootSystem(); err != nil {
				log.Error("reboot after BCO install", "err", err)
			}
		}
	})
}

// keepBCOInstalled runs attempt (bco.AutoInstall) until it needs no retry: once when the
// BCO runs our app, and again after bco.RetryDelay for as long as it runs none, since then
// the speaker has no network to protect and gets none without it. Each attempt holds the
// operations lock, so a Core install, reboot or app update meanwhile is refused; between
// attempts they can run. A busy lock is waited out the same way, or, with our app on the
// BCO, left for the next boot. flashed says whether a new image went in.
func keepBCOInstalled(ctx context.Context, lock func() (unlock func(), ok bool), attempt func(context.Context) (bool, error),
	ownApp func() bool, wait func(context.Context, time.Duration) bool, log *slog.Logger) (flashed bool) {
	for retry := 1; ; retry++ {
		if unlock, ok := lock(); ok {
			var err error
			flashed, err = attempt(ctx)
			unlock()
			if err != nil {
				log.Warn("BCO image install", "err", err)
			}
			if !bco.RetryInstall(err, ownApp) {
				return flashed
			}
		} else {
			log.Info("BCO image install", "skipped", "another firmware operation is running")
			if ownApp() {
				return false
			}
		}
		d := bco.RetryDelay(retry)
		log.Warn("BCO runs no own app; installing its image again later", "in", d)
		if !wait(ctx, d) {
			return false
		}
	}
}
