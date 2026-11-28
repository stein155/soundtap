// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import (
	"math"
	"strconv"
)

// Every screen below is the function of the same name in app/oled/design/design-screens.js,
// with the design's sample text turned into parameters. Words arrive translated.

// Corner is what a playing screen keeps in its top corners. While music plays nothing stays
// lit in one place for long on the OLED: no clock, source or room. A pause or stop icon,
// a preset's number, the radio's badge, a stereo pair's side and a pack's level are drawn,
// and only when they apply.
type Corner struct {
	// State is StatePaused or StateStopped, drawn top left; "" while playing.
	State string
	// Preset is the preset slot that holds what plays, boxed top left; 0 for none.
	Preset int
	// Badge is LIVE or BUFFERING, top right in the pixel font, at BadgeLevel.
	Badge      string
	BadgeLevel int
	// Mark is "L" or "R" on half of a stereo pair, boxed top right.
	Mark string
	// Battery is the pack's level in percent on a speaker with one (the Portable), drawn
	// top right in stock's bands; 0 draws nothing. Charging adds a plus.
	Battery  int
	Charging bool
}

// Corner.State values.
const (
	StatePaused  = "paused"
	StateStopped = "stopped"
)

// occupied says the corner draws something over the top of the screen.
func (c *Corner) occupied() bool {
	return c.State != "" || c.Mark != "" || c.Battery > 0
}

// cornerTop is the top of the corner marks: 4 px in from the panel's edges, so the burn-in
// orbit's pixel never takes them to the edge, where the ST20's panel looked to clip them.
const cornerTop = 4.0

//nolint:mnd // the design's coordinates and sizes (design-screens.js corner())
func (f *Frame) corner(marks *Corner) {
	switch {
	case marks.State == StatePaused:
		f.Icon(IconPause, 10, 9, maxLevel)
	case marks.State == StateStopped:
		f.Icon(IconStop, 10, 9, maxLevel)
	case marks.Preset > 0:
		f.label(strconv.Itoa(marks.Preset), 7, 12, maxLevel, Left)
		f.StrokeRect(4.5, cornerTop+half, 10, 10, 1, false, maxLevel)
	default: // playing, no preset: nothing top left
	}

	right := 121.0
	if marks.Battery > 0 {
		left := f.battery(right-2, marks.Battery)
		if marks.Charging {
			left = f.chargingPlus(left)
		}

		right = left - 4
	}

	if marks.Badge != "" {
		f.label(marks.Badge, right, 12, marks.BadgeLevel, Right)
	}

	if marks.Mark != "" {
		f.label(marks.Mark, right-3, 12, maxLevel, Center)
		f.StrokeRect(right-8.5, cornerTop+half, 11, 10, 1, false, maxLevel)
	}
}

// battery draws the battery icon with its right edge at right and returns its left edge.
//
//nolint:mnd // the icon's coordinates, sizes and grey levels
func (f *Frame) battery(right float64, percent int) float64 {
	width, height, top := 16.0, 8.0, cornerTop+2
	left := right - width
	f.Rect(left, top, width, 1, 12)
	f.Rect(left, top+height-1, width, 1, 12)
	f.Rect(left, top, 1, height, 12)
	f.Rect(right-1, top, 1, height, 12)
	f.Rect(right, top+2, 1.5, height-4, 12) // the terminal

	// stock's battery icon bands: one bar per band reached, up to four
	bands := [...]int{20, 40, 60, 75}
	for i, band := range bands {
		if percent > band || i == 0 && percent > 0 {
			f.Rect(left+2+float64(i)*3.25, top+2, 2.5, height-4, maxLevel)
		}
	}

	return left
}

// chargingPlus draws the charging plus left of the battery icon whose left edge is at
// left, and returns the plus's left edge.
//
//nolint:mnd // the icon's coordinates, sizes and grey levels
func (f *Frame) chargingPlus(left float64) float64 {
	const top = cornerTop + 2

	left -= 7
	f.Rect(left+1, top+3.5, 5, 1, maxLevel)
	f.Rect(left+3, top+1.5, 1, 5, maxLevel)

	return left
}

// NoticeOpt is a notice's optional parts.
type NoticeOpt struct {
	// IconLevel is the icon's grey; 0 is full white.
	IconLevel int
	// Small sets the title in the smaller sizes, for a long one (a device's name).
	Small bool
	// Dots, when above 0, adds three dots under it with the Dots-th lit.
	Dots int
}

// Notice is the design's notice(): an icon, a title and a small line stacked in the middle.
// A IconNone or "" line leaves its block out.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js notice())
func Notice(kind Icon, titleText, line string, opt NoticeOpt) *Frame {
	frame := newFrame()

	var items []item

	if kind != IconNone {
		level := opt.IconLevel
		if level == 0 {
			level = maxLevel
		}

		items = append(items, iconItem(kind, level))
	}

	if opt.Small {
		items = append(items, title(titleText, 16, 14))
	} else {
		items = append(items, title(titleText, 18, 16, 14))
	}

	if line != "" {
		items = append(items, small(line, 2))
	}

	if opt.Dots > 0 {
		items = append(items, dotsItem(3, opt.Dots-1))
	}

	frame.flow(items, 2, 98)

	return frame
}

// Message is a short message alone: a notice without an icon.
func Message(text string) *Frame {
	return Notice(IconNone, text, "", NoticeOpt{IconLevel: 0, Small: false, Dots: 0})
}

// ring is the circle a held key fills: grey 4, its progress white from the top.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js ring())
func (f *Frame) ring(progress float64) {
	const ringY, radius = 38, 21

	f.Arc(centreX, ringY, radius, 0, fullTurn, 3, RoundCap, 4)

	if progress > 0 {
		start := -math.Pi / 2
		f.Arc(centreX, ringY, radius, start, start+math.Min(1, progress)*fullTurn, 3, RoundCap, maxLevel)
	}
}

// Countdown is a held key combination: the seconds left in a ring that fills, and what it
// does on one line under it.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js countdown())
func Countdown(seconds int, progress float64, word string) *Frame {
	frame := newFrame()
	frame.ring(progress)
	frame.Text(strconv.Itoa(seconds), centreX, 47, Font{Face: "m800-24", Level: maxLevel, Align: Center, Max: 0})

	label := title(word, 14, 13, 12, 11)
	label.maxLines = 1
	frame.flow([]item{label}, 64, 98)

	return frame
}

// Holding is a preset key held down: its number in a ring that fills to storing.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js holding())
func Holding(word string, n int, progress float64) *Frame {
	frame := newFrame()
	frame.ring(math.Max(0, progress))
	frame.Text(strconv.Itoa(n), centreX, 47, Font{Face: "m800-24", Level: maxLevel, Align: Center, Max: 0})
	frame.flow([]item{title(word, 14, 13, 12)}, 62, 100)

	return frame
}

// Saved confirms a stored preset: the full ring with a check.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js saved())
func Saved(word string) *Frame {
	frame := newFrame()
	frame.ring(1)
	frame.Icon(IconCheck, centreX, 38, maxLevel)
	frame.flow([]item{title(word, 14)}, 62, 100)

	return frame
}

// Station is the radio's station page: the name large, a warning and a line under it
// when the station failed, and the page dots (0 or 1 lit; -1 for none) when it alternates
// with the now-playing page.
//
// value as the other screens take what they draw; warn is part of what the screen shows
//
//nolint:mnd,gocritic,revive // the design's sizes (design-screens.js station()); the corner by
func Station(marks Corner, name, line string, warn bool, pageDots int) *Frame {
	frame := newFrame()
	frame.corner(&marks)

	items := []item{title(name, 20, 18, 16, 14)}
	if warn {
		items = append(items, iconItem(IconWarn, maxLevel))
	}

	if line != "" {
		items = append(items, small(line, 2))
	}

	bottom := 98.0
	if pageDots >= 0 {
		bottom = 92
	}

	frame.flow(items, 12, bottom)

	if pageDots >= 0 {
		frame.dots(2, pageDots, 95)
	}

	return frame
}

// RadioNow is the radio's now-playing page: what the station says plays (its stream's
// title and artist), with the second page dot lit.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js radioNow())
func RadioNow(titleText, artist string) *Frame {
	frame := newFrame()

	items := []item{title(titleText, 20, 18, 16)}
	if artist != "" {
		items = append(items, small(artist, 2))
	}

	frame.flow(items, 4, 92)
	frame.dots(2, 1, 95)

	return frame
}

// secondsPerMinute splits Clock's seconds into minutes.
const secondsPerMinute = 60

// Clock is m:ss.
func Clock(s int) string {
	tens, ones := s%secondsPerMinute/decimalBase, s%decimalBase

	return strconv.Itoa(s/secondsPerMinute) + ":" + string(rune('0'+tens)) + string(rune('0'+ones))
}

// Song is a track on any source that plays them: the title, the artist, one thin progress
// bar and its times. A zero total leaves the bar and times out (a stream with no length).
//
//nolint:mnd,gocritic // the design's sizes (design-screens.js song()); the corner by value
func Song(marks Corner, titleText, artist string, elapsed float64, total int) *Frame {
	frame := newFrame()
	frame.corner(&marks)

	top, bottom := 2.0, 98.0
	if marks.occupied() {
		top = 12
	}

	if total > 0 {
		bottom = 72
	}

	items := []item{title(titleText, 20, 18, 16, 14)}
	if artist != "" {
		items = append(items, small(artist, 1))
	}

	frame.flow(items, top, bottom)

	if total > 0 {
		done := math.Min(1, math.Max(0, elapsed)/float64(total))

		frame.Rect(4, 76, 120, 3, 4)
		frame.Rect(4, 76, jsRound(120*done), 3, maxLevel)
		frame.label(Clock(int(math.Min(elapsed, float64(total)))), 4, 92, maxLevel, Left)
		frame.label(Clock(total), 124, 92, maxLevel, Right)
	}

	return frame
}

// volumeSteps is how many bars the volume screen has.
const volumeSteps = 20

// Volume is the volume overlay, percent 0..100: the number and 20 bars.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js vol())
func Volume(percent int) *Frame {
	frame := newFrame()
	frame.Text(strconv.Itoa(percent), centreX, 56, Font{Face: "m800-44", Level: maxLevel, Align: Center, Max: 0})

	lit := int(jsRound(float64(percent) / 5))

	for step := range volumeSteps {
		grey := 3
		if step < lit {
			grey = maxLevel
		}

		frame.Rect(4+float64(step)*6, 72, 4, 12, grey)
	}

	return frame
}

// Preset is the overlay after a preset press: PRESET, the number and the station.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js preset())
func Preset(word string, n int, station string) *Frame {
	frame := newFrame()
	frame.label(word, centreX, 12, maxLevel, Center)
	frame.Text(strconv.Itoa(n), centreX, 58, Font{Face: "m800-46", Level: maxLevel, Align: Center, Max: 0})

	if station != "" {
		frame.block(station, centreX, 82, textWidth, []float64{16}, maxLevel)
	}

	return frame
}

// PresetEmpty is a press on an empty preset: its number dark and the word under it.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js presetEmpty())
func PresetEmpty(n int, word string) *Frame {
	frame := newFrame()
	frame.Text(strconv.Itoa(n), centreX, 52, Font{Face: "m800-40", Level: 6, Align: Center, Max: 0})
	frame.block(word, centreX, 80, textWidth, []float64{16}, maxLevel)

	return frame
}

// Ambient is a playing screen dimmed: its title alone at grey 8, moved by (dx, dy) against
// burn-in.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js ambient())
func Ambient(titleText string, driftX, driftY int) *Frame {
	frame := newFrame()
	frame.block(titleText, centreX+float64(driftX), 52+float64(driftY), 112, []float64{16, 14}, 8)

	return frame
}

// Standby is the standby clock at grey 7, with AM/PM under it on a 12-hour clock (marker;
// "" for none), moved by (dx, dy) from its place against burn-in.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js clock())
func Standby(clock, marker string, driftX, driftY int) *Frame {
	frame := newFrame()

	baseline := 60.0
	if marker != "" {
		baseline = 54
	}

	x, y := centreX+float64(driftX), baseline+float64(driftY)
	frame.Text(clock, x, y, Font{Face: "m600-30", Level: 7, Align: Center, Max: 0})

	if marker != "" {
		frame.Text(marker, x, y+14, Font{Face: montserrat(smallWeight, smallSize), Level: 7, Align: Center, Max: 0})
	}

	return frame
}

// Progress is an install: what runs, its percentage, a bar and a small line.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js progress())
func Progress(titleText string, percent int, line string) *Frame {
	frame := newFrame()
	percent = max(0, min(100, percent))

	head := title(titleText, 14, 13, 12)
	head.maxLines = 1
	number := textItem(strconv.Itoa(percent)+"%", []float64{22}, 800, maxLevel, 1)
	bar := drawn(4, func(top float64) {
		frame.Rect(14, top, 100, 4, 4)
		frame.Rect(14, top, float64(percent), 4, maxLevel)
	})

	frame.flow([]item{head, number, bar, small(line, 1)}, 0, 100)

	return frame
}

// stepFont is the setup screen's step line: 11 px bold, full white.
func stepFont() Font {
	return Font{Face: montserrat(titleWeight, smallSize), Level: maxLevel, Align: Center, Max: 0}
}

// SetupJoin is the setup screen's first page: which network to join.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js setupPage())
func SetupJoin(step, network string) *Frame {
	frame := newFrame()
	frame.Text(step, centreX, 14, stepFont())
	frame.block(network, centreX, 46, textWidth, []float64{16, 14}, maxLevel)
	frame.dots(2, 0, 90)

	return frame
}

// SetupOpen is the setup screen's second page: the address to open, and the name.
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js setupPage())
func SetupOpen(step, address, name string) *Frame {
	frame := newFrame()
	frame.Text(step, centreX, 14, stepFont())
	frame.Text(address, centreX, 52, Font{Face: "m800-17", Level: maxLevel, Align: Center, Max: textWidth})
	frame.Text(name, centreX, 72,
		Font{Face: montserrat(smallWeight, smallSize), Level: smallLevel, Align: Center, Max: textWidth})
	frame.dots(2, 1, 90)

	return frame
}

// Confirm asks for a key held on the speaker: CONFIRM, what it confirms, how, and the dots
// (the on-th lit).
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js confirm())
func Confirm(word, question, how string, on int) *Frame {
	frame := newFrame()
	frame.label(word, centreX, 12, maxLevel, Center)
	frame.flow([]item{title(question, 20, 18, 16, 14), small(how, 2), dotsItem(3, on)}, 16, 100)

	return frame
}
