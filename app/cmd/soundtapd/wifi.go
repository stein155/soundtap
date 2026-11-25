// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/wifi"
)

// wifiMgr is the speaker's Wi-Fi manager (internal/wifi): set on a flashed speaker whose
// main processor owns the radio, nil off the speaker and on an ST20 whose stock BCO app
// keeps Wi-Fi.
//
//nolint:gochecknoglobals // process-wide state: startWifi sets it, the web handlers restart it
var wifiMgr atomic.Pointer[wifi.Manager]

// errNoWifi is a box whose Wi-Fi soundtapd does not manage: a development run, or stock's
// BCO app.
var errNoWifi = errors.New("Wi-Fi is not managed by soundtapd here")

// initScript is the boot script; its sync-clock step syncs the clock after a join.
const initScript = "/etc/init.d/soundtap"

// startWifi starts the Wi-Fi manager. It brings up the station or the setup AP, keeps
// what a previous soundtapd left running, and stops with ctx; the network daemons stay.
func startWifi(ctx context.Context, wifiConf string, log *slog.Logger) {
	if !onFirmware() || devMode.Load() || os.Getenv("SOUNDTAP_WIFI_MODE") != "scm" {
		return
	}

	log = log.With("comp", "wifi")

	// The interfaces, the variant, the paths, the timing and the host name stay unset: the
	// manager's own defaults for a speaker.
	var cfg wifi.Config

	cfg.Conf = wifiConf
	cfg.Baked = "/etc/soundtap-wifi.conf"
	cfg.Sys = wifi.OS{}
	cfg.SetupSSID = speakerstate.SetupSSID
	cfg.Ready = daemonUp.Load
	cfg.Joined = func() { syncClockAfterJoin(log) } //nolint:contextcheck // the clock sync outlives the join
	cfg.Problem = wifiProblem
	cfg.Log = log

	manager := wifi.New(cfg)
	wifiMgr.Store(manager)
	goSafe("wifi", func() { manager.Run(ctx) })
}

// wifiProblem shows what keeps Wi-Fi from working in /api/health until it works.
func wifiProblem(text string) {
	if text == "" {
		problems.clear("wifi")

		return
	}

	problems.set("wifi", "Wi-Fi: "+text)
}

// restartWifi reads wifi.conf again and joins, and returns at once: the caller is usually
// an HTTP handler whose answer must reach the browser before the network goes.
func restartWifi(log *slog.Logger) error {
	manager := wifiMgr.Load()
	if manager == nil {
		log.Debug("wifi restart skipped", "err", errNoWifi)

		return errNoWifi
	}

	manager.Restart()

	return nil
}

// restartWifiAfter is restartWifi after delay.
func restartWifiAfter(delay time.Duration, log *slog.Logger) {
	goSafe("wifiRestart", func() {
		time.Sleep(delay)

		_ = restartWifi(log)
	})
}

// openSetupAP opens the setup AP and keeps the saved network.
func openSetupAP(log *slog.Logger) error {
	manager := wifiMgr.Load()
	if manager == nil {
		log.Debug("setup AP skipped", "err", errNoWifi)

		return errNoWifi
	}

	manager.OpenSetup()

	return nil
}

// syncClockAfterJoin sets the clock from NTP now that there is a network, through the boot
// script's clock step (clock.sh, ADR 0125): the boot's own retry would get there within a
// minute or two. It runs in the background and logs to the boot log.
func syncClockAfterJoin(log *slog.Logger) {
	if rootHelper != nil {
		// The clock and its file on /mnt/nv are root's (ADR 0066).
		err := rootHelper.SyncClock()
		if err != nil {
			log.Warn("clock sync after the join not started", "err", err)
		}

		return
	}

	_, err := os.Stat(initScript)
	if err != nil {
		return
	}

	_, _, err = (wifi.OS{}).Start(initScript, "sync-clock")
	if err != nil {
		log.Warn("clock sync after the join not started", "err", err)
	}
}
