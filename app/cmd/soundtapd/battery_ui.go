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
func (u *batteryUI) step(now time.Time, b battery.Status, st speakerstate.StatusSnapshot) batteryAction {
	var a batteryAction
	if !b.Present {
		return a
	}
	if u.seen && b.OnDC != u.onDC {
		if b.OnDC {
			a.message = "Charging"
		} else {
			a.message = fmt.Sprintf("On battery %d%%", b.Level)
		}
	}
	if b.Low && (!u.low || !u.seen) {
		a.message = fmt.Sprintf("Battery low %d%%", b.Level)
	}
	u.seen, u.onDC, u.low = true, b.OnDC, b.Low
	idle := !st.Standby && st.PlayStatus != "PLAY_STATE" && st.PlayStatus != "BUFFERING_STATE"
	switch {
	case b.OnDC || !idle:
		u.idleSince = time.Time{}
	case u.idleSince.IsZero():
		u.idleSince = now
	case now.Sub(u.idleSince) >= onBatteryIdleOff:
		u.idleSince = time.Time{}
		a.standby = true
	}
	return a
}

// runBatteryUI shows the Portable's charger changes and a low battery on the panel, and puts
// an idle speaker on battery in standby after stock's 20 minutes.
func runBatteryUI(ctx context.Context, sp *speakerstate.Speaker, log *slog.Logger) {
	var u batteryUI
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b, ok := batteryStatus()
		if !ok {
			continue
		}
		a := u.step(time.Now(), b, sp.StatusSnapshot())
		if a.message != "" {
			msg := a.message
			frontPanel.Show(4*time.Second, func() *screen.Frame { return panel.MessageScreen(msg) })
		}
		if a.standby {
			log.Info("battery: idle on battery, standby", "after", onBatteryIdleOff)
			_ = sp.EnterStandby(ctx)
		}
	}
}
