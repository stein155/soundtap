// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"context"
	"log/slog"
	"strconv"
	"time"
	_ "time/tzdata" // the rootfs ships no zoneinfo

	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/oled/screen"
)

// clockScreenTick is how often the standby screen looks at the clock: often enough to
// catch the minute rollover promptly.
const clockScreenTick = time.Second

// RunClockScreen drives the ST20 standby screen: the time alone, shown while the speaker is
// in standby (the display manager only paints standby content in standby). No-op without
// the panel.
//
// It re-registers the screen only when the rendered content actually changes — once a
// minute — so the frame stays byte-stable between updates. That is what
// keeps the old flicker (frame churning every tick) away: soundtapd is the sole writer to
// /dev/fb0, so an unchanging frame simply never flickers.
//
// cfg is read every tick rather than captured, so a settings change lands on the panel
// without a restart. With the clock switched off, or no timezone known, the standby screen
// is dropped entirely, restoring the framebuffer that existed before SoundTap drew on it.
func RunClockScreen(ctx context.Context, disp *display.Manager, cfg func() settings.Display, log *slog.Logger) {
	clockScreenEvery(ctx, disp, cfg, log, clockScreenTick)
}

// clockScreenEvery is RunClockScreen looking at the clock every period.
func clockScreenEvery(
	ctx context.Context, disp *display.Manager, cfg func() settings.Display, log *slog.Logger, period time.Duration,
) {
	if !disp.Available() {
		return
	}

	log.Info("oled standby screen up")

	tick := time.NewTicker(period)
	defer tick.Stop()

	var lastCfg settings.Display

	last := ""
	// Whether a standby frame is registered. Kept apart from last, which a settings
	// change resets to force a repaint: turning the clock off is such a change, and the
	// frame it left was never cleared.
	shown := false
	// The zone is loaded on a settings change only: LoadLocation parses the embedded
	// zoneinfo, and this loop ran it every second. "" is UTC, as clockZone has it.
	zone := time.UTC

	for {
		now := cfg()
		if now != lastCfg {
			zone = clockZone(now.Timezone)
			lastCfg = now
			last = "" // force a repaint in the new format
		}
		// Like stock, no clock before the first NTP sync: the one restored from flash can
		// be hours behind after a power cut.
		if !now.ClockOn() || !clocksync.Synced() {
			if shown {
				disp.ClearStandby()

				shown = false
			}

			last = ""
		} else {
			at := time.Now().In(zone)
			if draw, key := standbyFrame(at, now.Hour12); key != last {
				disp.SetStandby(display.Content{Frame: draw(), Fade: 0})

				last, shown = key, true
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// standbyFrame is the standby clock: the time alone at grey 7, as stock's, so as few pixels
// as possible stay lit on the OLED. A 12-hour clock has no room for AM/PM beside it on the
// panel, so that goes on the line under it.
//
// Against burn-in the clock moves to the next place of driftPath every minute, never more
// than 4 px from the middle: every lit pixel of a digit is dark for most of the time. The
// place follows from the minute alone: a restart or a repaint lands on the same pixel. It
// returns the frame as a func, so the caller draws it only when key changed.
//
//nolint:revive // flag-parameter: hour12 is the owner's clock setting, passed through as data
func standbyFrame(now time.Time, hour12 bool) (func() []byte, string) {
	clock := clockText(now, hour12)

	marker := ""
	if hour12 {
		marker = now.Format("PM")
	}

	step := int(now.Unix() / int64(time.Minute/time.Second))
	key := clock + "|" + marker + "|" + strconv.Itoa(step%len(driftPath))
	dx, dy := driftAt(step)

	return func() []byte { return screen.Standby(clock, marker, dx, dy).Pix(fullLevel) }, key
}

// clockZone resolves the town's IANA zone; SetDisplay already refused unknown names.
func clockZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}

	return loc
}
