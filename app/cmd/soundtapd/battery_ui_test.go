// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/hw/battery"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestBatteryUIMessagesAndIdleOff(t *testing.T) {
	t.Parallel()

	var state batteryUI

	now := time.Unix(0, 0)
	playing := tuneinSnapshot("PLAY_STATE")
	paused := tuneinSnapshot("PAUSE_STATE")
	charging := batteryReading(80, true, false)
	bat := batteryReading(79, false, false)

	if a := state.step(now, charging, &playing); a.message != "" || a.standby {
		t.Fatalf("first reading: %+v", a)
	}

	if a := state.step(now, bat, &playing); a.message != "On battery 79%" {
		t.Fatalf("unplugged: %+v", a)
	}

	if a := state.step(now, charging, &playing); a.message != "Charging" {
		t.Fatalf("plugged in: %+v", a)
	}

	low := batteryReading(19, false, true)
	if a := state.step(now, low, &playing); a.message != "Battery low 19%" {
		t.Fatalf("low: %+v", a)
	}

	if a := state.step(now, low, &playing); a.message != "" {
		t.Fatalf("low repeated: %+v", a)
	}
	// Paused on battery: standby after 20 minutes, not before; playing resets the clock.
	state.step(now, low, &paused)

	if a := state.step(now.Add(19*time.Minute), low, &paused); a.standby {
		t.Fatal("standby before 20 minutes")
	}

	if a := state.step(now.Add(20*time.Minute), low, &paused); !a.standby {
		t.Fatal("no standby after 20 idle minutes on battery")
	}

	state.step(now.Add(21*time.Minute), low, &playing)

	if a := state.step(now.Add(45*time.Minute), charging, &paused); a.standby {
		t.Fatal("standby on DC")
	}
}

// tuneinSnapshot is a speaker on TuneIn in the given play state.
func tuneinSnapshot(playStatus string) speakerstate.StatusSnapshot {
	return speakerstate.StatusSnapshot{
		Source: "TUNEIN", PlayStatus: playStatus, Standby: false, Volume: 0, Muted: false, Bass: 0,
		Notification: false, EmptyPreset: false, EmptySlot: 0, TransportAck: false, IP: "",
	}
}

// batteryReading is a pack at level percent, on the charger or not, low or not.
func batteryReading(level int, onDC, low bool) battery.Status {
	return battery.Status{
		Present: true, Maker: "", Level: level, MilliVolts: 0, MilliAmps: 0, Celsius: 0,
		OnDC: onDC, Charging: false, Full: false, Low: low, Fault: 0,
	}
}
