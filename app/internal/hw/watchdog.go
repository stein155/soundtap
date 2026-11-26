// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// The hardware watchdog stays armed for the whole life of the speaker (ADR 0052).
// omap_wdt.early_enable starts it before Linux; the kernel feeds it until a process opens
// /dev/watchdog, for at most CONFIG_WATCHDOG_OPEN_TIMEOUT (180 s), and from then on only
// soundtapd does, and only while it is healthy. It is never closed with the magic 'V': a
// crashed or restarting soundtapd leaves it counting, and the next soundtapd has
// watchdogTimeout to take it over. A kernel hang, an OOM livelock or a wedged soundtapd
// now ends in a reset instead of a speaker that needs unplugging.
const (
	watchdogTimeout = 120 * time.Second // also covers an update restart and a slow start
	watchdogPet     = 10 * time.Second
	// watchdogStrikes failed checks in a row stop the feeding: one slow answer while a
	// firmware install keeps the CPU busy is not a wedge.
	watchdogStrikes = 3
)

// The watchdog device, under its legacy name and its numbered one.
const (
	watchdogDev  = "/dev/watchdog"
	watchdogDev0 = "/dev/watchdog0"
)

// openWatchdog opens the first watchdog device that opens, or reports the last failure.
func openWatchdog() (*os.File, error) {
	var err error

	for _, path := range []string{watchdogDev, watchdogDev0} {
		var dev *os.File

		dev, err = os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil {
			return dev, nil
		}
	}

	return nil, fmt.Errorf("open watchdog: %w", err)
}

// RunWatchdog feeds the hardware watchdog while healthy reports nil, until ctx ends.
func RunWatchdog(ctx context.Context, healthy func(context.Context) error, log *slog.Logger) {
	dev, err := openWatchdog()
	if err != nil {
		log.Info("hardware watchdog unavailable", "err", err)

		return
	}
	// Closed without 'V' on purpose: see above.
	defer dev.Close()

	err = setWatchdogTimeout(dev, watchdogTimeout)
	if err != nil {
		log.Warn("watchdog timeout not set; the driver's default applies", "err", err)
	}

	log.Info("hardware watchdog fed by soundtapd", "timeout", watchdogTimeout)

	strikes := 0

	ticker := time.NewTicker(watchdogPet)
	defer ticker.Stop()

	for {
		err = healthy(ctx)
		if err != nil {
			strikes++
			log.Warn("health check failed", "strike", strikes, "of", watchdogStrikes, "err", err)
		} else {
			strikes = 0
		}

		if strikes < watchdogStrikes {
			_, writeErr := dev.Write([]byte{0})
			if writeErr != nil {
				log.Warn("watchdog write", "err", writeErr)
			}
		} else if strikes == watchdogStrikes {
			log.Error("not feeding the watchdog; the speaker resets unless health returns", "within", watchdogTimeout)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
