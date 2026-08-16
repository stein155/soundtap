// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"
	_ "time/tzdata" // the rootfs ships no zoneinfo

	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/weather"
	"github.com/stein155/soundtap/app/oled"
	"github.com/stein155/soundtap/app/oled/screen"
)

type weatherFetcher interface {
	Configure(lat, lon float64)
	Fetch(context.Context) (*weather.Current, error)
}

type weatherRefresh struct {
	cfg settings.Display
	cur *weather.Current
	err error
}

var clockScreenTick = time.Second

// weatherEvery is how often a reading is refreshed. weatherRetry is the wait instead
// while there is no reading at all: the ST20 starts this loop before Wi-Fi has a route,
// so the first fetch fails, and waiting the full interval left the line empty for 15
// minutes after every boot.
var (
	weatherEvery = 15 * time.Minute
	weatherRetry = time.Minute
)

func acceptWeatherRefresh(result weatherRefresh, cfg settings.Display) (*weather.Current, bool, error) {
	if result.cfg != cfg {
		return nil, false, nil
	}
	return result.cur, true, result.err
}

// RunClockScreen drives the ST20 standby screen: a big clock with an optional
// weather line under it, shown while the speaker is in standby (the display manager
// only paints standby content in standby). No-op without the panel.
//
// It re-registers the screen only when the rendered content actually changes — once a
// minute for the clock, every ~15 min for the weather — so the frame stays byte-stable
// between updates. That is what keeps the old flicker (frame churning every tick) away:
// soundtapd is the sole writer to /dev/fb0, so an unchanging frame simply never flickers.
//
// cfg is read every tick rather than captured, so a settings change lands on the panel
// without a restart. With the clock switched off, or no timezone known, the standby screen is dropped
// entirely, restoring the framebuffer that existed before SoundTap drew on it.
//
//nolint:gocognit,cyclop,funlen // the loop is unchanged; only the language parameter went
func RunClockScreen(ctx context.Context, disp *display.Manager, wx weatherFetcher, cfg func() settings.Display, log *slog.Logger) {
	if !disp.Available() {
		return
	}
	log.Info("oled standby screen up")

	var cur *weather.Current
	var lastCfg settings.Display
	failing := false // only log the first failure of a run, not one every 15 minutes
	refreshed := make(chan weatherRefresh)
	pending := 0 // fetches whose result has not come back yet
	refresh := func(d settings.Display) {
		if wx == nil || !d.WeatherEnabled() {
			return
		}
		pending++
		safego.Go("weather", func() {
			c, err := wx.Fetch(ctx)
			select {
			case refreshed <- weatherRefresh{cfg: d, cur: c, err: err}:
			case <-ctx.Done():
			}
		})
	}

	wxTick := time.NewTicker(weatherEvery)
	defer wxTick.Stop()
	tick := time.NewTicker(clockScreenTick) // catch the minute rollover promptly
	defer tick.Stop()

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
			// Consent and coordinates changed: re-point the service and drop a reading
			// taken under the old location before it can be shown under the new one.
			if wx != nil {
				wx.Configure(d.Lat, d.Lon)
			}
			cur, failing, lastCfg = nil, false, d
			refresh(d)
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
			if draw, key := standbyFrame(at, d.Hour12, cur); key != last {
				disp.SetStandby(display.Content{Frame: dimFrame(draw(), d.Brightness)})
				last, shown = key, true
			}
		}

		select {
		case <-ctx.Done():
			return
		case result := <-refreshed:
			pending--
			updated, current, err := acceptWeatherRefresh(result, lastCfg)
			if !current {
				continue
			}
			if err != nil {
				if !failing {
					log.Warn("weather fetch failed; showing clock only", "err", err)
					failing = true
				}
				if cur == nil {
					wxTick.Reset(weatherRetry)
				}
				continue
			}
			if failing || cur == nil {
				wxTick.Reset(weatherEvery)
			}
			failing = false
			cur = updated
		case <-wxTick.C:
			// A fetch slower than weatherRetry must not have a second one started behind it.
			if pending == 0 {
				refresh(lastCfg)
			}
		case <-tick.C:
		}
	}
}

// standbyFrame is the design's standby clock: the time, the date under it and, once a
// reading is in, the outside temperature beside the date. A 12-hour clock has no room for
// AM/PM beside it on the panel, so that goes after the date.
//
// Against burn-in the whole group walks over the panel as stock's walking clock does: one
// pixel diagonally every walkStep, bouncing off the edges, so every lit pixel of a digit
// is dark for a stretch every few minutes. Stock moves its clock this way and dims
// nothing (its brightness is on or off); the brightness setting here comes on top.
// It returns the frame as a func, so the caller draws it only when key changed.
func standbyFrame(at time.Time, hour12 bool, cur *weather.Current) (draw func() []byte, key string) {
	clock := clockText(at, hour12)
	long, short := Dates(at)
	if hour12 {
		long += " " + at.Format("PM")
		short += " " + at.Format("PM")
	}
	temp := ""
	if cur != nil {
		temp = fmt.Sprintf("%d°", cur.TempC)
	}
	step := int(at.Unix() / int64(walkStep/time.Second))
	key = clock + "|" + long + "|" + temp + "|" + strconv.Itoa(step)
	return func() []byte { return walkFrame(screen.Standby(clock, long, short, temp, 0).Pix(15), step) }, key
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
