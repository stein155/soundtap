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
	"github.com/stein155/soundtap/app/oled"
	"github.com/stein155/soundtap/app/oled/screen"
)

var clockScreenTick = time.Second

// RunClockScreen drives the ST20 standby screen: the time alone, shown while the speaker is
// in standby (the display manager only paints standby content in standby). No-op without
// the panel.
//
// It re-registers the screen only when the rendered content actually changes — once a
// walk step or a minute — so the frame stays byte-stable between updates. That is what
// keeps the old flicker (frame churning every tick) away: soundtapd is the sole writer to
// /dev/fb0, so an unchanging frame simply never flickers.
//
// cfg is read every tick rather than captured, so a settings change lands on the panel
// without a restart. With the clock switched off, or no timezone known, the standby screen
// is dropped entirely, restoring the framebuffer that existed before SoundTap drew on it.
func RunClockScreen(ctx context.Context, disp *display.Manager, cfg func() settings.Display, log *slog.Logger) {
	if !disp.Available() {
		return
	}
	log.Info("oled standby screen up")

	tick := time.NewTicker(clockScreenTick) // catch the minute rollover promptly
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
		d := cfg()
		if d != lastCfg {
			zone = clockZone(d.Timezone)
			lastCfg = d
			last = "" // force a repaint in the new format
		}
		// Like stock, no clock before the first NTP sync: the one restored from flash can
		// be hours behind after a power cut.
		if !d.ClockOn() || !clocksync.Synced() {
			if shown {
				disp.ClearStandby()
				shown = false
			}
			last = ""
		} else {
			at := time.Now().In(zone)
			if draw, key := standbyFrame(at, d.Hour12); key != last {
				disp.SetStandby(display.Content{Frame: dimFrame(draw(), d.Brightness)})
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

// standbyFrame is the standby clock: the time alone, as stock's, so as few pixels as
// possible stay lit on the OLED. A 12-hour clock has no room for AM/PM beside it on the
// panel, so that goes on the line under it.
//
// Against burn-in the whole group walks over the panel as stock's walking clock does: one
// pixel diagonally every walkStep, bouncing off the edges, so every lit pixel of a digit
// is dark for a stretch every few minutes. Stock moves its clock this way and dims
// nothing (its brightness is on or off); the brightness setting here comes on top.
// It returns the frame as a func, so the caller draws it only when key changed.
func standbyFrame(at time.Time, hour12 bool) (draw func() []byte, key string) {
	clock := clockText(at, hour12)
	marker := ""
	if hour12 {
		marker = at.Format("PM")
	}
	step := int(at.Unix() / int64(walkStep/time.Second))
	key = clock + "|" + marker + "|" + strconv.Itoa(step)
	return func() []byte { return walkFrame(screen.Standby(clock, marker, 0).Pix(15), step) }, key
}

// walkStep is how long the standby clock stays put before it moves one pixel: about the
// 4.9 s of stock's walking clock (a 5 s interval on a 120 ms tick).
const walkStep = 5 * time.Second

// walkMargin keeps the walk off the panel's outermost pixels; walkBottom off its bottom
// two rows, which are the panel's edge.
const walkMargin, walkBottom = 1, 2

// walkFrame moves what pix shows to the step-th place of the walk: the lit area's corner
// runs back and forth over the free width and height at one pixel per step on both axes,
// so the path is the diagonal bounce of stock's clock. The place follows from the step
// alone: a restart or a repaint lands on the same pixel.
func walkFrame(pix []byte, step int) []byte {
	const w, h = oled.Width, oled.Height
	minX, minY, maxX, maxY := w, h, -1, -1
	for i, v := range pix {
		if v == 0 {
			continue
		}
		x, y := i%w, i/w
		minX, maxX = min(minX, x), max(maxX, x)
		minY, maxY = min(minY, y), max(maxY, y)
	}
	if maxX < 0 {
		return pix
	}
	x := walkMargin + bounce(step, w-2*walkMargin-1-(maxX-minX))
	y := walkMargin + bounce(step, h-walkMargin-walkBottom-1-(maxY-minY))
	out := make([]byte, len(pix))
	for sy := minY; sy <= maxY; sy++ {
		copy(out[(y+sy-minY)*w+x:(y+sy-minY)*w+x+maxX-minX+1], pix[sy*w+minX:sy*w+maxX+1])
	}
	return out
}

// bounce is step folded back and forth over 0..room.
func bounce(step, room int) int {
	if room <= 0 {
		return 0
	}
	p := step % (2 * room)
	if p > room {
		p = 2*room - p
	}
	return p
}

// dimFrame scales the standby frame to the chosen brightness, as stock's clock has a
// brightness setting: 80, the default, is the design's own level. 0 or 80 leaves it as
// drawn. It writes in place, which standbyFrame's fresh frame allows.
func dimFrame(pix []byte, percent int) []byte {
	if percent == 0 || percent == 80 {
		return pix
	}
	for i, v := range pix {
		pix[i] = byte(min(int(v)*percent/80, 255)) //nolint:gosec // clamped, and percent is not negative
	}
	return pix
}

// clockZone resolves the town's IANA zone; SetDisplay already refused unknown names.
func clockZone(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.UTC
}
