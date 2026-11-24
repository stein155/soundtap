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
	var u batteryUI
	now := time.Unix(0, 0)
	playing := speakerstate.StatusSnapshot{Source: "TUNEIN", PlayStatus: "PLAY_STATE"}
	paused := speakerstate.StatusSnapshot{Source: "TUNEIN", PlayStatus: "PAUSE_STATE"}
	dc := battery.Status{Present: true, OnDC: true, Level: 80}
	bat := battery.Status{Present: true, Level: 79}

	if a := u.step(now, dc, playing); a.message != "" || a.standby {
		t.Fatalf("first reading: %+v", a)
	}
	if a := u.step(now, bat, playing); a.message != "On battery 79%" {
		t.Fatalf("unplugged: %+v", a)
	}
	if a := u.step(now, dc, playing); a.message != "Charging" {
		t.Fatalf("plugged in: %+v", a)
	}
	low := battery.Status{Present: true, Level: 19, Low: true}
	if a := u.step(now, low, playing); a.message != "Battery low 19%" {
		t.Fatalf("low: %+v", a)
	}
	if a := u.step(now, low, playing); a.message != "" {
		t.Fatalf("low repeated: %+v", a)
	}
	// Paused on battery: standby after 20 minutes, not before; playing resets the clock.
	u.step(now, low, paused)
	if a := u.step(now.Add(19*time.Minute), low, paused); a.standby {
		t.Fatal("standby before 20 minutes")
	}
	if a := u.step(now.Add(20*time.Minute), low, paused); !a.standby {
		t.Fatal("no standby after 20 idle minutes on battery")
	}
	u.step(now.Add(21*time.Minute), low, playing)
	if a := u.step(now.Add(45*time.Minute), dc, paused); a.standby {
		t.Fatal("standby on DC")
	}
}
