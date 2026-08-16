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
	j700s32  = "j700-32"
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

// Bar is the status line along the top.
type Bar struct {
	Source string // "", "bt", "aux", or a source name drawn as text ("Spotify")
	Room   string // room name, or "+1" for a group
	Time   string
	// State is StatePaused or StateStopped, drawn as an icon in the source's place, as
	// stock does; "" while playing.
	State string
	// Mark is "L" or "R" on half of a stereo pair, drawn left of the time.
	Mark string
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

func (f *Frame) statusBar(b Bar) {
	x := 4.0
	switch b.State {
	case StatePaused:
		f.Rect(5, 4, 2, 8, 12)
		f.Rect(9, 4, 2, 8, 12)
		x = 15
	case StateStopped:
		f.Rect(5, 4, 7, 7, 12)
		x = 15
	default:
		f.sourceLabel(b.Source, &x)
	}
	if b.Room != "" {
		f.Text(b.Room, x, 11, Font{Face: j600s8, Level: 8, Max: 80})
	}
	f.Text(b.Time, 124, 11, Font{Face: j600s8, Level: 8, Align: Right})
	if b.Mark != "" {
		mx := 124 - Width(j600s8, b.Time) - 12
		f.RRect(mx-2, 3, 10, 10, 2, 4)
		f.TextMid(b.Mark, mx+3, 8, Font{Face: pix7, Level: 15, Align: Center})
	}
	f.Rect(4, 16, 120, 1, 2)
}

// sourceLabel draws the source at the bar's left and moves x past it.
func (f *Frame) sourceLabel(source string, x *float64) {
	switch source {
	case "":
	case SourceBT:
		f.iconBT(5, 3, 1, 8)
		*x = 15
	case SourceAux:
		f.Text("AUX", 4, 11, Font{Face: pix8, Level: 8})
		*x = 24
	default:
		f.Text(source, 4, 11, Font{Face: j600s8, Level: 8})
		// The design puts the room at 40, right of "Spotify"; a longer name pushes it on.
		*x = math.Max(40, math.Ceil(4+Width(j600s8, source)+7))
	}
}

// iconSpeaker at (x, y) scaled by s.
func (f *Frame) iconSpeaker(x, y, s float64, l, waves int) {
	p := func(px, py float64) Pt { return Pt{x + px*s, y + py*s} }
	f.Poly([]Pt{p(0, 3), p(2.5, 3), p(5.5, 0), p(5.5, 9), p(2.5, 6), p(0, 6)}, l)
	for i := range waves {
		f.Arc(x+6*s, y+4.5*s, (2.5+float64(i)*2.2)*s, -0.9, 0.9, s, l)
	}
}

func (f *Frame) iconBT(x, y, s float64, l int) {
	p := func(px, py float64) Pt { return Pt{x + px*s, y + py*s} }
	f.Line([]Pt{p(0, 2.5), p(5, 7), p(2.5, 9.5), p(2.5, 0), p(5, 2.5), p(0, 7)}, 1.1*s, false, l)
}

// Boot is a bar alone at progress 0..1, centred on the panel as stock draws it. The
// design's boot screen also has a wordmark and a word; ours is the bar without text.
func Boot(progress float64) *Frame {
	f := &Frame{}
	f.RRect(24, 48, 80, 4, 2, 3)
	f.RRect(24, 48, math.Max(4, math.Min(1, progress)*80), 4, 2, 15)
	return f
}

// Station is radio and buffering: preset 0 leaves the preset badge out, badge is
// "LIVE" (level 10) or "BUFFEREN" (level 7) in the pixel font.
func Station(b Bar, name, tagline string, preset int, badge string, badgeLevel int) *Frame {
	f := &Frame{}
	f.statusBar(b)
	f.Text(name, 6, 42, Font{Face: j800s15, Level: 15, Max: 116})
	f.Text(tagline, 6, 55, Font{Face: j500s8, Level: 8, Max: 116})
	if preset > 0 {
		f.RRect(6, 75, 14, 14, 7, 3)
		f.TextMid(strconv.Itoa(preset), 13, 82, Font{Face: j800s8p5, Level: 14, Align: Center})
	}
	if badge != "" {
		f.TextMid(badge, 122, 82, Font{Face: pix7, Level: badgeLevel, Align: Right})
	}
	return f
}

// Track is the Spotify screen and every other source that plays tracks. A zero total
// leaves the bar and times out (streams with no length).
func Track(b Bar, title, sub string, elapsed float64, total int) *Frame {
	f := &Frame{}
	f.statusBar(b)
	f.Text(title, 6, 40, Font{Face: j800s13, Level: 15, Max: 116})
	f.Text(sub, 6, 52, Font{Face: j500s8, Level: 8, Max: 116})
	if total > 0 {
		p := math.Min(1, elapsed/float64(total))
		f.RRect(6, 70, 116, 3, 1.5, 3)
		f.RRect(6, 70, math.Max(3, p*116), 3, 1.5, 13)
		f.Text(Clock(int(elapsed)), 6, 84, Font{Face: j600s7p5, Level: 8})
		f.Text(Clock(total), 122, 84, Font{Face: j600s7p5, Level: 8, Align: Right})
	}
	return f
}

// Clock is m:ss.
func Clock(s int) string {
	return strconv.Itoa(s/60) + ":" + string(rune('0'+s%60/10)) + string(rune('0'+s%10))
}

// Volume is the volume overlay, v 0..100.
func Volume(v int) *Frame {
	f := &Frame{}
	waves := 2
	if v == 0 {
		waves = 0
	} else if v < 40 {
		waves = 1
	}
	f.iconSpeaker(64-11, 12, 2, 10, waves)
	n := strconv.Itoa(v)
	if v == 0 {
		n = "—"
	}
	f.Text(n, 64, 62, Font{Face: j800s34, Level: 15, Align: Center})
	on := int(math.Round(float64(v) / 100 * 20))
	for i := range 20 {
		l := 3
		if i < on {
			l = 15
		}
		f.RRect(6+float64(i)*6, 76, 4, 10, 1, l)
	}
	return f
}

// Mute is the muted overlay.
func Mute(word string) *Frame {
	f := &Frame{}
	f.iconSpeaker(57, 26, 1, 15, 0)
	f.Line([]Pt{{65, 28}, {71, 34}}, 1.4, false, 15)
	f.Line([]Pt{{71, 28}, {65, 34}}, 1.4, false, 15)
	f.Text(word, 64, 60, Font{Face: j800s13, Level: 15, Align: Center})
	return f
}

// Preset is the overlay after a preset press.
func Preset(word string, n int, station string) *Frame {
	f := &Frame{}
	f.Text(word, 64, 26, Font{Face: pix8, Level: 7, Align: Center})
	f.Text(strconv.Itoa(n), 64, 64, Font{Face: j800s40, Level: 15, Align: Center})
	f.Text(station, 64, 84, Font{Face: j700s10, Level: 12, Align: Center, Max: 116})
	return f
}

// Bluetooth is the Bluetooth source with its connected device.
func Bluetooth(b Bar, title, device string) *Frame {
	f := &Frame{}
	f.statusBar(b)
	f.iconBT(58, 26, 1.4, 15)
	f.Text(title, 64, 58, Font{Face: j800s13, Level: 15, Align: Center})
	f.Text(device, 64, 71, Font{Face: j500s8, Level: 8, Align: Center})
	return f
}

// Holding is a preset button held down, progress 0..1 to storing.
func Holding(word string, n int, progress float64) *Frame {
	f := &Frame{}
	p := math.Max(0, math.Min(1, progress))
	f.Text(word, 64, 22, Font{Face: j700s9, Level: 10, Align: Center})
	f.Text(strconv.Itoa(n), 64, 66, Font{Face: j800s40, Level: 15, Align: Center})
	f.RRect(10, 80, 108, 6, 3, 3)
	f.RRect(10, 80, math.Max(6, p*108), 6, 3, 15)
	return f
}

// Saved confirms a stored preset.
func Saved(word string) *Frame {
	f := &Frame{}
	f.Line([]Pt{{51, 40}, {60, 49}, {77, 31}}, 3, true, 15)
	f.Text(word, 64, 72, Font{Face: j700s9, Level: 15, Align: Center})
	return f
}

// Aux is the AUX source.
func Aux(b Bar, sub string) *Frame {
	f := &Frame{}
	b.Source = SourceAux
	f.statusBar(b)
	f.Text("AUX", 64, 56, Font{Face: j800s26, Level: 15, Align: Center})
	f.Text(sub, 64, 72, Font{Face: j500s8, Level: 8, Align: Center})
	return f
}

// Pairing is Bluetooth discoverable.
func Pairing(title, sub string) *Frame {
	f := &Frame{}
	f.Arc(64, 36, 12, 0, 2*math.Pi, 1, 6)
	f.iconBT(61, 31, 1, 15)
	f.Text(title, 64, 70, Font{Face: j800s12, Level: 15, Align: Center})
	f.Text(sub, 64, 83, Font{Face: j500s7p5, Level: 8, Align: Center, Max: 120})
	return f
}

// Standby is the dimmed clock. minute drives the anti burn-in drift. With temp set the
// short date goes left under the clock and the temperature right; else the long date
// is centred.
func Standby(clock, date, shortDate, temp string, minute int) *Frame {
	f := &Frame{}
	drift := minute % 4
	dx, dy := float64(drift%2*2-1), float64(drift/2*2)
	f.Text(clock, 64+dx, 54+dy, Font{Face: j700s32, Level: 10, Align: Center})
	if temp != "" {
		// As the design: the line spans the clock. A 12-hour "8:42" would leave the date
		// no room, so a short clock spans as a four-digit one.
		w := Width(j700s32, clock)
		if len(clock) < len("00:00") {
			w = Width(j700s32, "00:00")
		}
		l, r := math.Floor(64-w/2+0.5)+dx, math.Floor(64+w/2+0.5)+dx
		tw := Width(j700s9, temp)
		f.Text(shortDate, l, 76+dy, Font{Face: j600s9, Level: 8, Max: r - l - tw - 4})
		f.Text(temp, r, 76+dy, Font{Face: j700s9, Level: 9, Align: Right})
	} else if date != "" {
		f.Text(date, 64+dx, 76+dy, Font{Face: j600s9, Level: 8, Align: Center})
	}
	return f
}

// Offline is no network; step moves the dots (one step per 300 ms).
func Offline(b Bar, title, sub string, step int) *Frame {
	f := &Frame{}
	f.statusBar(b)
	f.Text(title, 64, 48, Font{Face: j800s11, Level: 15, Align: Center, Max: 124})
	f.Text(sub, 64, 62, Font{Face: j500s7p5, Level: 8, Align: Center, Max: 124})
	for i := range 3 {
		l := 4
		if step%3 == i {
			l = 15
		}
		f.Rect(57+float64(i)*6, 76, 3, 3, l)
	}
	return f
}

// The screens below are not in the design; they are its type and spacing applied to the
// panel's other messages.

// Message is a short message as the design's Offline title: Plus Jakarta Sans 800 at 11 px, level
// 15, wrapped to three centred lines.
func Message(text string) *Frame {
	f := &Frame{}
	lines := Wrap(j800s11, text, 120, 3)
	top := 55 - float64(len(lines)-1)*13/2
	for i, l := range lines {
		f.Text(l, 64, top+float64(i)*13, Font{Face: j800s11, Level: 15, Align: Center})
	}
	return f
}

// Notice is a title with lines of detail under it and, with progress >= 0, the boot bar.
// The title is Offline's (level 15, up to two lines), the lines are its subline (level 8,
// 14 px apart); a line that is a value under its label, as the setup screen's network
// name, is level 15. The block is centred on the panel.
func Notice(title string, lines []string, progress float64) *Frame {
	f := &Frame{}
	tl := Wrap(j800s11, title, 120, 2)
	y := 55 - 7*float64(len(lines)) - 6.5*float64(len(tl)-1)
	for i, l := range tl {
		f.Text(l, 64, y+float64(i)*13, Font{Face: j800s11, Level: 15, Align: Center})
	}
	y += float64(len(tl)-1) * 13
	for i, l := range lines {
		lvl := 8
		if i%2 == 1 {
			lvl = 15
		}
		f.Text(l, 64, y+float64(i+1)*14, Font{Face: j500s7p5, Level: lvl, Align: Center, Max: 124})
	}
	if progress >= 0 {
		by := y + float64(len(lines))*14 + 10
		f.RRect(24, by, 80, 4, 2, 3)
		f.RRect(24, by, math.Max(4, math.Min(1, progress)*80), 4, 2, 15)
	}
	return f
}

// breakWord splits a word wider than width between letters: Japanese, Chinese and Thai
// write without spaces. A combining mark stays with its letter.
func breakWord(face, w string, width float64) []string {
	var out []string
	line := []rune{}
	for _, r := range w {
		if len(line) > 0 && !unicode.Is(unicode.Mn, r) && Width(face, string(append(line, r))) > width {
			out, line = append(out, string(line)), nil
		}
		line = append(line, r)
	}
	return append(out, string(line))
}

// Wrap breaks text into at most n lines no wider than width; the last one is shortened
// with an ellipsis when the text does not fit.
func Wrap(face, text string, width float64, n int) []string {
	var lines []string
	for _, w := range strings.Fields(text) {
		if len(lines) > 0 && Width(face, lines[len(lines)-1]+" "+w) <= width {
			lines[len(lines)-1] += " " + w
			continue
		}
		lines = append(lines, breakWord(face, w, width)...)
	}
	if len(lines) > n {
		lines = append(lines[:n-1], strings.Join(lines[n-1:], " "))
	}
	for i := range lines {
		lines[i] = Fit(face, lines[i], width)
	}
	return lines
}
