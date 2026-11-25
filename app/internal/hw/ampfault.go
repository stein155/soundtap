// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// AmpFault watches the amplifier's fault input and latches a fault.
type AmpFault struct {
	gpio   int           // sysfs number of the fault input; 0 disables the monitor
	every  time.Duration // poll interval; tests reduce it
	active atomic.Bool
}

// NewAmpFault watches fault input gpio (model.Model.AmpFaultGPIO), or the line the init
// script resolved into AMP_FAULT_GPIO.
func NewAmpFault(gpio int) *AmpFault {
	if n, err := strconv.Atoi(os.Getenv("AMP_FAULT_GPIO")); err == nil {
		gpio = n
	}

	return &AmpFault{gpio: gpio, every: 500 * time.Millisecond}
}

// ampFaultReadFailLimit is how many consecutive read failures are tolerated before
// the monitor gives up. Without a limit, a model that does not wire this GPIO logs a
// warning on every tick forever into a RAM-backed log — the same failure mode
// Amp's mute cache exists to prevent on the mute GPIO.
const ampFaultReadFailLimit = 5

// ampFaultClearReads is how many healthy reads in a row it takes to release the latch:
// 2 s at the production poll. A fault line that flickers would otherwise mute and unmute
// the amp every poll, and each release is a pop.
const ampFaultClearReads = 4

// ampFaultLatch asserts on the first faulted read and releases only after
// ampFaultClearReads healthy reads in a row. healthy is the running healthy count.
func ampFaultLatch(was, fault bool, healthy int) (bool, int) {
	if fault {
		return true, 0
	}

	if !was {
		return false, 0
	}

	healthy++
	if healthy >= ampFaultClearReads {
		return false, 0
	}

	return true, healthy
}

// Active reports whether the amp fault GPIO is currently asserted. Read by
// trackOwnership on every mute decision and surfaced in the web health status.
func (f *AmpFault) Active() bool { return f.active.Load() }

// Run polls the amp fault GPIO (active low = fault) until ctx ends. On a rising edge it
// calls mute immediately rather than waiting for trackOwnership's next tick, and after
// ampFaultClearReads healthy reads it clears the latch so trackOwnership can restore the
// normal source state. modelName is for the log. Call in a goroutine.
func (f *AmpFault) Run(ctx context.Context, modelName string, mute func(), log *slog.Logger) {
	if f.gpio <= 0 {
		log.Info("ampfault: no fault GPIO for this model; monitor disabled", "model", modelName)

		return
	}

	gpioDir, err := exportGPIO(f.gpio)
	if err != nil {
		log.Warn("ampfault: export gpio", "gpio", f.gpio, "err", err)

		return
	}
	// Allow udev a moment to create the attributes of a line exported just now.
	select {
	case <-ctx.Done():
		return
	case <-time.After(50 * time.Millisecond):
	}

	valuePath := filepath.Join(gpioDir, "value")

	t := time.NewTicker(f.every)
	defer t.Stop()

	fails, healthy := 0, 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		raw, err := os.ReadFile(valuePath)
		if err != nil {
			fails++
			if fails >= ampFaultReadFailLimit {
				log.Warn("ampfault: giving up after repeated read failures",
					"path", valuePath, "attempts", fails, "err", err)

				return
			}

			log.Warn("ampfault: read gpio", "path", valuePath, "err", err)

			continue
		}

		fails = 0
		// Active low: a healthy amplifier holds the line high, as stock's idle ST20 shows.
		was := f.active.Load()

		var fault bool

		fault, healthy = ampFaultLatch(was, len(raw) > 0 && raw[0] == '0', healthy)
		f.active.Store(fault)

		switch {
		case fault && !was:
			// Mute now for latency; trackOwnership keeps it muted while the latch is set.
			log.Error("amp fault asserted: muting output")
			mute()
		case !fault && was:
			// Leave the unmute to trackOwnership, which knows whether a source is
			// playing and whether PCM is actually flowing.
			log.Warn("amp fault cleared")
		}
	}
}
