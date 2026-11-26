// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Atlas faces: Plus Jakarta Sans weight and pixel size, or Silkscreen (pix).
const (
	j500s7p5 = "j500-7.5"
	j500s8   = "j500-8"
	j600s7p5 = "j600-7.5"
	j600s8   = "j600-8"
	j600s9   = "j600-9"
	j700s10  = "j700-10"
	j500s32  = "j500-32"
	j700s9   = "j700-9"
	j800s11  = "j800-11"
	j800s12  = "j800-12"
	j800s13  = "j800-13"
	j800s15  = "j800-15"
	j800s26  = "j800-26"
	j800s34  = "j800-34"
	j800s40  = "j800-40"
	j800s8p5 = "j800-8.5"
	pix7     = "pix-7"
	pix8     = "pix-8"
)

// Every screen below is the function of the same name in app/oled/design/design-screens.js,
// with the design's sample text turned into parameters. Words arrive translated.

// Bar is the status line along the top. Offline draws all of it; the playing screens only
// its State, Mark and Battery (corner), so no clock or name stays lit while music plays.
type Bar struct {
	Source string // "", "bt", "aux", or a source name drawn as text ("Spotify")
	Room   string // room name, or "+1" for a group
	Time   string
	// State is StatePaused or StateStopped, drawn as an icon in the source's place, as
	// stock does; "" while playing.
	State string
	// Mark is "L" or "R" on half of a stereo pair, drawn left of the time.
	Mark string
	// Battery is the pack's level in percent on a speaker with one (the Portable), drawn
	// left of the time in stock's bands; 0 draws nothing. Charging adds a plus.
	Battery  int
	Charging bool
}

// battery draws the battery icon with its right edge at right and returns its left edge.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func (f *Frame) battery(right float64, percent int) float64 {
	width, height, top := 16.0, 8.0, 4.0
	left := right - width
	f.Rect(left, top, width, 1, 8)
	f.Rect(left, top+height-1, width, 1, 8)
	f.Rect(left, top, 1, height, 8)
	f.Rect(right-1, top, 1, height, 8)
	f.Rect(right, top+2, 1.5, height-4, 8) // the terminal

	// stock's battery icon bands: one bar per band reached, up to four
	bands := [...]int{20, 40, 60, 75}
	for i, band := range bands {
		if percent > band || i == 0 && percent > 0 {
			f.Rect(left+2+float64(i)*3.25, top+2, 2.5, height-4, 12)
		}
	}

	return left
}

// chargingPlus draws the charging plus left of the battery icon whose left edge is at
// left, and returns the plus's left edge.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func (f *Frame) chargingPlus(left float64) float64 {
	const top = 4.0

	left -= 7
	f.Rect(left+1, top+3.5, 5, 1, 12)
	f.Rect(left+3, top+1.5, 1, 5, 12)

	return left
}

// Source values with an icon or pixel-font label instead of their name.
const (
	SourceBT  = "bt"
	SourceAux = "aux"
)

// Bar.State values.
const (
	StatePaused  = "paused"
	StateStopped = "stopped"
)

//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func (f *Frame) statusBar(bar *Bar) {
	roomX := 4.0

	switch bar.State {
	case StatePaused:
		f.Rect(5, 4, 2, 8, 12)
		f.Rect(9, 4, 2, 8, 12)

		roomX = 15
	case StateStopped:
		f.Rect(5, 4, 7, 7, 12)

		roomX = 15
	default:
		f.sourceLabel(bar.Source, &roomX)
	}

	if bar.Room != "" {
		f.Text(bar.Room, roomX, 11, Font{Face: j600s8, Level: 8, Align: Left, Max: 80})
	}

	f.Text(bar.Time, 124, 11, Font{Face: j600s8, Level: 8, Align: Right, Max: 0})

	right := 124 - Width(j600s8, bar.Time) - 4
	if bar.Battery > 0 {
		left := f.battery(right-2, bar.Battery)
		if bar.Charging {
			left = f.chargingPlus(left)
		}

		right = left - 4
	}

	if bar.Mark != "" {
		markX := right - 8
		f.RRect(markX-2, 3, 10, 10, 2, 4)
		f.TextMid(bar.Mark, markX+3, 8, Font{Face: pix7, Level: 15, Align: Center, Max: 0})
	}

	f.Rect(4, 16, 120, 1, 2)
}

// corner is what the playing screens keep of the status bar. While music plays nothing
// stays lit in one place for long on the OLED: no clock, source or room. A pause or stop
// icon, a stereo pair's L or R and a pack's level are drawn, and only when they apply.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func (f *Frame) corner(bar *Bar) {
	switch bar.State {
	case StatePaused:
		f.Rect(5, 4, 2, 8, 12)
		f.Rect(9, 4, 2, 8, 12)
	case StateStopped:
		f.Rect(5, 4, 7, 7, 12)
	default:
		// Playing: nothing in the corner.
	}

	right := 124.0
	if bar.Battery > 0 {
		left := f.battery(right-2, bar.Battery)
		if bar.Charging {
			left = f.chargingPlus(left)
		}

		right = left - 4
	}

	if bar.Mark != "" {
		markX := right - 8
		f.RRect(markX-2, 3, 10, 10, 2, 4)
		f.TextMid(bar.Mark, markX+3, 8, Font{Face: pix7, Level: 15, Align: Center, Max: 0})
	}
}

// titleLines is how many lines a playing screen's title may take.
const titleLines = 2

// title draws a playing screen's title in up to two lines and returns the baseline of the
// last: one line sits at 34, two at 26 and 43 (the design's title()).
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func (f *Frame) title(s string) float64 {
	lines := Wrap(j800s15, s, 116, titleLines)
	first := 34 - 8*float64(len(lines)-1)

	for i, line := range lines {
		f.Text(line, 6, first+17*float64(i), Font{Face: j800s15, Level: 15, Align: Left, Max: 0})
	}

	return first + 17*float64(len(lines)-1)
}

// sourceLabel draws the source at the bar's left and moves next past it.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func (f *Frame) sourceLabel(source string, next *float64) {
	switch source {
	case "":
	case SourceBT:
		f.iconBT(5, 3, 1, 8)

		*next = 15
	case SourceAux:
		f.Text("AUX", 4, 11, Font{Face: pix8, Level: 8, Align: Left, Max: 0})

		*next = 24
	default:
		f.Text(source, 4, 11, Font{Face: j600s8, Level: 8, Align: Left, Max: 0})
		// The design puts the room at 40, right of "Spotify"; a longer name pushes it on.
		*next = math.Max(40, math.Ceil(4+Width(j600s8, source)+7))
	}
}

// iconSpeaker at (x, y) scaled by s.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func (f *Frame) iconSpeaker(x, y, s float64, grey, waves int) {
	p := func(px, py float64) Pt { return Pt{x + px*s, y + py*s} }
	f.Poly([]Pt{p(0, 3), p(2.5, 3), p(5.5, 0), p(5.5, 9), p(2.5, 6), p(0, 6)}, grey)

	for i := range waves {
		f.Arc(x+6*s, y+4.5*s, (2.5+float64(i)*2.2)*s, -0.9, 0.9, s, grey)
	}
}

//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func (f *Frame) iconBT(x, y, s float64, grey int) {
	p := func(px, py float64) Pt { return Pt{x + px*s, y + py*s} }
	f.Line([]Pt{p(0, 2.5), p(5, 7), p(2.5, 9.5), p(2.5, 0), p(5, 2.5), p(0, 7)}, 1.1*s, ButtCap, grey)
}

// Boot is a bar alone at progress 0..1, centred on the panel as stock draws it. The
// design's boot screen also has a wordmark and a word; ours is the bar without text.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Boot(progress float64) *Frame {
	frame := newFrame()
	frame.RRect(24, 48, 80, 4, 2, 3)
	frame.RRect(24, 48, math.Max(4, math.Min(1, progress)*80), 4, 2, 15)

	return frame
}

// Station is radio and buffering: preset 0 leaves the preset badge out, badge is
// "LIVE" (level 10) or "BUFFEREN" (level 7) in the pixel font.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Station(
	bar Bar, //nolint:gocritic // hugeParam: the exported screens take the bar by value
	name, tagline string, preset int, badge string, badgeLevel int,
) *Frame {
	frame := newFrame()
	frame.corner(&bar)
	last := frame.title(name)
	frame.Text(tagline, 6, last+14, Font{Face: j500s8, Level: 8, Align: Left, Max: 116})

	if preset > 0 {
		frame.RRect(6, 75, 14, 14, 7, 3)
		frame.TextMid(strconv.Itoa(preset), 13, 82, Font{Face: j800s8p5, Level: 14, Align: Center, Max: 0})
	}

	if badge != "" {
		frame.TextMid(badge, 122, 82, Font{Face: pix7, Level: badgeLevel, Align: Right, Max: 0})
	}

	return frame
}

// Track is the Spotify screen and every other source that plays tracks. A zero total
// leaves the bar and times out (streams with no length).
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Track(
	bar Bar, //nolint:gocritic // hugeParam: the exported screens take the bar by value
	title, sub string, elapsed float64, total int,
) *Frame {
	frame := newFrame()
	frame.corner(&bar)
	last := frame.title(title)
	frame.Text(sub, 6, last+14, Font{Face: j500s8, Level: 8, Align: Left, Max: 116})

	if total > 0 {
		done := math.Min(1, elapsed/float64(total))

		frame.RRect(6, 70, 116, 3, 1.5, 3)
		frame.RRect(6, 70, math.Max(3, done*116), 3, 1.5, 13)
		frame.Text(Clock(int(elapsed)), 6, 84, Font{Face: j600s7p5, Level: 8, Align: Left, Max: 0})
		frame.Text(Clock(total), 122, 84, Font{Face: j600s7p5, Level: 8, Align: Right, Max: 0})
	}

	return frame
}

// secondsPerMinute splits Clock's seconds into minutes.
const secondsPerMinute = 60

// Clock is m:ss.
func Clock(s int) string {
	tens, ones := s%secondsPerMinute/decimalBase, s%decimalBase

	return strconv.Itoa(s/secondsPerMinute) + ":" + string(rune('0'+tens)) + string(rune('0'+ones))
}

// Volume is the volume overlay, percent 0..100.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Volume(percent int) *Frame {
	frame := newFrame()

	waves := 2
	if percent == 0 {
		waves = 0
	} else if percent < 40 {
		waves = 1
	}

	frame.iconSpeaker(64-11, 12, 2, 10, waves)

	label := strconv.Itoa(percent)
	if percent == 0 {
		label = "—"
	}

	frame.Text(label, 64, 62, Font{Face: j800s34, Level: 15, Align: Center, Max: 0})

	lit := int(math.Round(float64(percent) / 100 * 20))

	for segment := range 20 {
		grey := 3
		if segment < lit {
			grey = 15
		}

		frame.RRect(6+float64(segment)*6, 76, 4, 10, 1, grey)
	}

	return frame
}

// Mute is the muted overlay.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Mute(word string) *Frame {
	frame := newFrame()
	frame.iconSpeaker(57, 26, 1, 15, 0)
	frame.Line([]Pt{{65, 28}, {71, 34}}, 1.4, ButtCap, 15)
	frame.Line([]Pt{{71, 28}, {65, 34}}, 1.4, ButtCap, 15)
	frame.Text(word, 64, 60, Font{Face: j800s13, Level: 15, Align: Center, Max: 0})

	return frame
}

// Preset is the overlay after a preset press.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Preset(word string, n int, station string) *Frame {
	frame := newFrame()
	frame.Text(word, 64, 26, Font{Face: pix8, Level: 7, Align: Center, Max: 0})
	frame.Text(strconv.Itoa(n), 64, 64, Font{Face: j800s40, Level: 15, Align: Center, Max: 0})
	frame.Text(station, 64, 84, Font{Face: j700s10, Level: 12, Align: Center, Max: 116})

	return frame
}

// Bluetooth is the Bluetooth source with its connected device.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Bluetooth(
	bar Bar, //nolint:gocritic // hugeParam: the exported screens take the bar by value
	title, device string,
) *Frame {
	frame := newFrame()
	frame.corner(&bar)
	frame.iconBT(58, 18, 1.4, 15)
	frame.Text(title, 64, 52, Font{Face: j800s15, Level: 15, Align: Center, Max: 120})
	frame.Text(device, 64, 66, Font{Face: j500s8, Level: 8, Align: Center, Max: 120})

	return frame
}

// Holding is a preset button held down, progress 0..1 to storing.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Holding(word string, n int, progress float64) *Frame {
	frame := newFrame()
	done := math.Max(0, math.Min(1, progress))

	frame.Text(word, 64, 22, Font{Face: j700s9, Level: 10, Align: Center, Max: 0})
	frame.Text(strconv.Itoa(n), 64, 66, Font{Face: j800s40, Level: 15, Align: Center, Max: 0})
	frame.RRect(10, 80, 108, 6, 3, 3)
	frame.RRect(10, 80, math.Max(6, done*108), 6, 3, 15)

	return frame
}

// Saved confirms a stored preset.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Saved(word string) *Frame {
	frame := newFrame()
	frame.Line([]Pt{{51, 40}, {60, 49}, {77, 31}}, 3, RoundCap, 15)
	frame.Text(word, 64, 72, Font{Face: j700s9, Level: 15, Align: Center, Max: 0})

	return frame
}

// Aux is the AUX source.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Aux(
	bar Bar, //nolint:gocritic // hugeParam: the exported screens take the bar by value
	sub string,
) *Frame {
	frame := newFrame()
	frame.corner(&bar)
	frame.Text("AUX", 64, 50, Font{Face: j800s26, Level: 15, Align: Center, Max: 0})
	frame.Text(sub, 64, 66, Font{Face: j500s8, Level: 8, Align: Center, Max: 120})

	return frame
}

// Pairing is Bluetooth discoverable.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Pairing(title, sub string) *Frame {
	frame := newFrame()
	frame.Arc(64, 36, 12, 0, fullTurn, 1, 6)
	frame.iconBT(61, 31, 1, 15)
	frame.Text(title, 64, 70, Font{Face: j800s12, Level: 15, Align: Center, Max: 0})
	frame.Text(sub, 64, 83, Font{Face: j500s7p5, Level: 8, Align: Center, Max: 120})

	return frame
}

// Standby is the dimmed clock: the time alone, as stock's, with AM/PM under it on a
// 12-hour clock (marker; "" for none). minute drives the anti burn-in drift.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Standby(clock, marker string, minute int) *Frame {
	frame := newFrame()
	drift := minute % 4
	dx, dy := float64(drift%2*2-1), float64(drift/2*2)
	frame.Text(clock, 64+dx, 54+dy, Font{Face: j500s32, Level: 10, Align: Center, Max: 0})

	if marker != "" {
		frame.Text(marker, 64+dx, 76+dy, Font{Face: j600s9, Level: 8, Align: Center, Max: 0})
	}

	return frame
}

// Offline is no network; step moves the dots (one step per 300 ms).
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Offline(
	bar Bar, //nolint:gocritic // hugeParam: the exported screens take the bar by value
	title, sub string, step int,
) *Frame {
	frame := newFrame()
	frame.statusBar(&bar)
	frame.Text(title, 64, 48, Font{Face: j800s11, Level: 15, Align: Center, Max: 124})
	frame.Text(sub, 64, 62, Font{Face: j500s7p5, Level: 8, Align: Center, Max: 124})

	for dot := range 3 {
		grey := 4
		if step%3 == dot {
			grey = 15
		}

		frame.Rect(57+float64(dot)*6, 76, 3, 3, grey)
	}

	return frame
}

// The screens below are not in the design; they are its type and spacing applied to the
// panel's other messages.

// Message is a short message as the design's Offline title: Plus Jakarta Sans 800 at 11 px, level
// 15, wrapped to three centred lines.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Message(text string) *Frame {
	frame := newFrame()
	lines := Wrap(j800s11, text, 120, 3)

	top := 55 - float64(len(lines)-1)*13/2
	for i, line := range lines {
		frame.Text(line, 64, top+float64(i)*13, Font{Face: j800s11, Level: 15, Align: Center, Max: 0})
	}

	return frame
}

// Notice is a title with lines of detail under it and, with progress >= 0, the boot bar.
// The title is Offline's (level 15, up to two lines), the lines are its subline (level 8,
// 14 px apart); a line that is a value under its label, as the setup screen's network
// name, is level 15. The block is centred on the panel.
//
//nolint:mnd // the design's coordinates, sizes and grey levels (design-screens.js)
func Notice(title string, lines []string, progress float64) *Frame {
	frame := newFrame()
	titleLines := Wrap(j800s11, title, 120, 2)

	top := 55 - 7*float64(len(lines)) - 6.5*float64(len(titleLines)-1)
	for i, line := range titleLines {
		frame.Text(line, 64, top+float64(i)*13, Font{Face: j800s11, Level: 15, Align: Center, Max: 0})
	}

	top += float64(len(titleLines)-1) * 13

	for row, line := range lines {
		grey := 8
		if row%2 == 1 {
			grey = 15
		}

		frame.Text(line, 64, top+float64(row+1)*14, Font{Face: j500s7p5, Level: grey, Align: Center, Max: 124})
	}

	if progress >= 0 {
		barY := top + float64(len(lines))*14 + 10
		frame.RRect(24, barY, 80, 4, 2, 3)
		frame.RRect(24, barY, math.Max(4, math.Min(1, progress)*80), 4, 2, 15)
	}

	return frame
}

// breakWord splits a word wider than width between letters: Japanese, Chinese and Thai
// write without spaces. A combining mark stays with its letter.
func breakWord(face, word string, width float64) []string {
	var out []string

	line := []rune{}
	for _, r := range word {
		if len(line) > 0 && !unicode.Is(unicode.Mn, r) && Width(face, string(append(line, r))) > width {
			out, line = append(out, string(line)), nil
		}

		line = append(line, r)
	}

	return append(out, string(line))
}

// Wrap breaks text into at most maxLines lines no wider than width; the last one is
// shortened with an ellipsis when the text does not fit.
func Wrap(face, text string, width float64, maxLines int) []string {
	var lines []string
	for word := range strings.FieldsSeq(text) {
		if len(lines) > 0 && Width(face, lines[len(lines)-1]+" "+word) <= width {
			lines[len(lines)-1] += " " + word

			continue
		}

		lines = append(lines, breakWord(face, word, width)...)
	}

	if len(lines) > maxLines {
		lines = append(lines[:maxLines-1], strings.Join(lines[maxLines-1:], " "))
	}

	for i := range lines {
		lines[i] = Fit(face, lines[i], width)
	}

	return lines
}
