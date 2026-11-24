// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/hw/battery"
)

// The SoundTouch Portable's battery wiring, from stock taigan_scm.dtb and BatteryMonitor.
const (
	batteryBus       = "/dev/i2c-0"
	batteryAddr      = 0x0b
	batteryGPIOBank0 = "44e07000" // gpio0: dcpresent is gpio0_27, low while DC is present
	batteryGPIOBank3 = "481ae000" // gpio3: charge_enable is gpio3_8
	batteryDCLine    = 27
	batteryChargeEn  = 8
	// chargerLevelFile is stock's: the charge enable's active level, '1' or '0'. Read,
	// never written or removed: some units hold '0'.
	chargerLevelFile = "/mnt/nv/charger_active_level"
)

// batteryMonitor is the running monitor, nil until runBattery opened the pack.
var batteryMonitor atomic.Pointer[battery.Monitor]

// batteryStatus is the pack as last read, for /api/health; false on a model without one.
func batteryStatus() (battery.Status, bool) {
	if m := batteryMonitor.Load(); m != nil {
		return m.Status(), true
	}
	return battery.Status{}, false
}

// runBattery watches the Portable's pack until ctx ends. A model without one never calls it.
func runBattery(ctx context.Context, log *slog.Logger) {
	bus, err := battery.OpenSMBus(batteryBus, batteryAddr)
	if err != nil {
		log.Error("battery: no gauge", "err", err)
		return
	}
	defer bus.Close()
	dc, err := hw.BankLine(batteryGPIOBank0, batteryDCLine, "in")
	if err != nil {
		log.Error("battery: no DC-present line", "err", err)
		return
	}
	defer dc.Close()
	activeHigh := true
	if b, err := os.ReadFile(chargerLevelFile); err == nil && strings.TrimSpace(string(b)) == "0" {
		activeHigh = false
	}
	cfg := battery.Config{Bus: bus, DCPresent: dc, ChargeActiveHigh: activeHigh,
		// Stock's protection is the baseline: SHIP on a pack fault, a critically discharged
		// pack and DC above 13 V (docs/hardware/portable/README.md).
		AllowShip: true, Log: log}
	// Opened at the "off" level, so charging starts only once the pack has been read.
	off := "low"
	if !activeHigh {
		off = "high"
	}
	if en, err := hw.BankLine(batteryGPIOBank3, batteryChargeEn, off); err != nil {
		log.Error("battery: no charge enable line; reading only", "err", err)
	} else {
		defer en.Close()
		cfg.ChargeEn = en
	}
	m := battery.New(cfg)
	batteryMonitor.Store(m)
	if err := m.Run(ctx); err != nil {
		log.Error("battery: monitoring stopped", "err", err)
	}
}
