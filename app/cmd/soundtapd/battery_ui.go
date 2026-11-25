// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stein155/soundtap/app/internal/hw/battery"
	"github.com/stein155/soundtap/app/internal/panel"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/oled/screen"
)

// onBatteryIdleOff is stock's OnBatteryTimeout: a Portable on battery that plays nothing for
// this long goes to standby, so a forgotten speaker does not drain its pack.
const onBatteryIdleOff = 20 * time.Minute

const (
	// batteryUIPoll is how often runBatteryUI reads the pack.
	batteryUIPoll = 5 * time.Second
	// batteryMessageShown is how long a battery message stays on the panel.
	batteryMessageShown = 4 * time.Second
)

// batteryUI turns battery readings into what the owner sees and the idle switch-off.
type batteryUI struct {
	seen      bool
	onDC, low bool
	idleSince time.Time
}

// batteryAction is what one reading asks for: a message on the panel, a standby.
type batteryAction struct {
	message string
	standby bool
}

// step takes one reading and the speaker's state at now.
func (u *batteryUI) step(now time.Time, reading battery.Status, status *speakerstate.StatusSnapshot) batteryAction {
	var action batteryAction
	if !reading.Present {
		return action
	}

	if u.seen && reading.OnDC != u.onDC {
		if reading.OnDC {
			action.message = "Charging"
		} else {
			action.message = fmt.Sprintf("On battery %d%%", reading.Level)
		}
	}

	if reading.Low && (!u.low || !u.seen) {
		action.message = fmt.Sprintf("Battery low %d%%", reading.Level)
	}

	u.seen, u.onDC, u.low = true, reading.OnDC, reading.Low

	idle := !status.Standby && status.PlayStatus != "PLAY_STATE" && status.PlayStatus != "BUFFERING_STATE"
	switch {
	case reading.OnDC || !idle:
		u.idleSince = time.Time{}
	case u.idleSince.IsZero():
		u.idleSince = now
	case now.Sub(u.idleSince) >= onBatteryIdleOff:
		u.idleSince = time.Time{}
		action.standby = true
	default: // idle on battery, not yet for long
	}

	return action
}

// runBatteryUI shows the Portable's charger changes and a low battery on the panel, and puts
// an idle speaker on battery in standby after stock's 20 minutes.
func runBatteryUI(ctx context.Context, speaker *speakerstate.Speaker, log *slog.Logger) {
	var state batteryUI

	ticker := time.NewTicker(batteryUIPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		reading, ok := batteryStatus()
		if !ok {
			continue
		}

		snapshot := speaker.StatusSnapshot()

		action := state.step(time.Now(), reading, &snapshot)
		if action.message != "" {
			msg := action.message

			frontPanel.Show(batteryMessageShown, func() *screen.Frame { return panel.MessageScreen(msg) })
		}

		if action.standby {
			log.Info("battery: idle on battery, standby", "after", onBatteryIdleOff)

			_ = speaker.EnterStandby(ctx)
		}
	}
}
