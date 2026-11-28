// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// The design's text: titles in Montserrat 700 at the largest of a list of sizes that fits,
// small lines in 600 at 11 px, labels in Silkscreen at 8 px (pix8).
const (
	pix8 = "pix-8"
	// smallSize and smallWeight are the small lines' type; smallLevel their grey.
	smallSize   = 11
	smallWeight = 600
	smallLevel  = 12
	titleWeight = 700
	// textWidth is how wide a centred line may run: the panel less 4 px each side.
	textWidth = 120
	// gap is the space between the blocks of a stack.
	gap = 8
	// lineHeight is a title's line pitch as a multiple of its size (flow), blockHeight the
	// one-shot title's (block).
	lineHeight  = 1.15
	blockHeight = 1.12
	// baselineAt and blockBaseline put a line's baseline that far down its line pitch.
	baselineAt    = 0.8
	blockBaseline = 0.78
	// centreX is the panel's middle column.
	centreX = 64
)

// montserrat is the atlas face of Montserrat at weight and size: "m700-18".
func montserrat(weight int, size float64) string {
	return "m" + strconv.Itoa(weight) + "-" + strconv.FormatFloat(size, 'f', -1, 64)
}

// jsRound is JavaScript's Math.round: halves go up.
func jsRound(x float64) float64 { return math.Floor(x + half) }

// ellipsis is the design's ell(): s shortened a letter at a time, with "…", until it is at
// most limit wide.
func ellipsis(faceName, s string, limit float64) string {
	if Width(faceName, s) <= limit {
		return s
	}

	runes := []rune(s)
	for len(runes) > 1 && Width(faceName, string(runes)+"…") > limit {
		runes = runes[:len(runes)-1]
	}

	return string(runes) + "…"
}

// noSpaces is a script written without spaces between words, which wrap breaks between
// letters instead: Japanese, Chinese and Thai.
func noSpaces(word string) bool {
	for _, r := range word {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai) {
			return true
		}
	}

	return false
}

// wrap is the design's wrap(): words joined while the line stays width wide. A word wider
// than a line is left whole, so the fit falls to a smaller size or an ellipsis, except in
// a script without spaces, which breaks between letters.
func wrap(faceName, text string, width float64) []string {
	var lines []string

	cur := ""

	for word := range strings.SplitSeq(text, " ") {
		try := word
		if cur != "" {
			try = cur + " " + word
		}

		switch {
		case Width(faceName, try) <= width:
			cur = try
		case noSpaces(word) && Width(faceName, word) > width:
			parts := breakWord(faceName, word, width)
			if cur != "" && Width(faceName, cur+" "+parts[0]) <= width {
				parts[0] = cur + " " + parts[0]
			} else if cur != "" {
				lines = append(lines, cur)
			}

			lines = append(lines, parts[:len(parts)-1]...)
			cur = parts[len(parts)-1]
		default:
			if cur != "" {
				lines = append(lines, cur)
			}

			cur = word
		}
	}

	if cur != "" {
		lines = append(lines, cur)
	}

	return lines
}

// breakWord splits a word wider than width between letters. A combining mark stays with
// its letter.
func breakWord(faceName, word string, width float64) []string {
	var out []string

	line := []rune{}
	for _, r := range word {
		if len(line) > 0 && !unicode.Is(unicode.Mn, r) && Width(faceName, string(append(line, r))) > width {
			out, line = append(out, string(line)), nil
		}

		line = append(line, r)
	}

	return append(out, string(line))
}

// fitted is a title laid out: its size and lines.
type fitted struct {
	size  float64
	lines []string
}

// fitAll is every line no wider than width.
func fitAll(faceName string, lines []string, width float64) bool {
	for _, line := range lines {
		if Width(faceName, line) > width {
			return false
		}
	}

	return true
}

// rescueSizes are the title sizes a text that fits none of its own sizes gets before an
// ellipsis: a long compound (German "Einrichtungsmodus", Danish "Opsætningstilstand") is a
// word no wrap can break, and cut it says nothing. The design's own texts never need them.
//
//nolint:gochecknoglobals // a constant list
var rescueSizes = []float64{13, 12, 11}

// fitText is the design's fitText(): one line at the largest size that holds it, then two,
// up to maxLines; then a title in the smaller rescueSizes; else the smallest size, cut to
// maxLines with an ellipsis.
func fitText(text string, width float64, sizes []float64, weight, maxLines int) fitted {
	for n := 1; n <= maxLines; n++ {
		for _, size := range sizes {
			lines := wrap(montserrat(weight, size), text, width)
			if len(lines) <= n && fitAll(montserrat(weight, size), lines, width) {
				return fitted{size: size, lines: lines}
			}
		}
	}

	size := sizes[len(sizes)-1]

	if weight == titleWeight {
		for _, rescue := range rescueSizes {
			if rescue >= size {
				continue
			}

			lines := wrap(montserrat(weight, rescue), text, width)
			if len(lines) <= maxLines && fitAll(montserrat(weight, rescue), lines, width) {
				return fitted{size: rescue, lines: lines}
			}
		}
	}

	name := montserrat(weight, size)

	lines := wrap(name, text, width)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ellipsis(name, lines[maxLines-1]+" …", width)
	}

	for i := range lines {
		lines[i] = ellipsis(name, lines[i], width)
	}

	return fitted{size: size, lines: lines}
}

// fitBlock is the design's fit(): the largest size whose wrap takes at most two lines, else
// one line of the smallest with an ellipsis.
func fitBlock(text string, width float64, sizes []float64, weight int) fitted {
	for _, size := range sizes {
		lines := wrap(montserrat(weight, size), text, width)
		if len(lines) <= 2 && fitAll(montserrat(weight, size), lines, width) {
			return fitted{size: size, lines: lines}
		}
	}

	size := sizes[len(sizes)-1]

	return fitted{size: size, lines: []string{ellipsis(montserrat(weight, size), text, width)}}
}

// TitleLines is text laid out as a notice's title: the lines it takes at the size that fits.
// A line that ends in an ellipsis text does not have says text was cut.
func TitleLines(text string) []string {
	return fitText(text, textWidth, []float64{18, 16, 14}, titleWeight, titleLines).lines
}

// block draws a one-shot title centred on (cx, cy): the design's block().
func (f *Frame) block(text string, middleX, middleY, width float64, sizes []float64, level int) {
	fit := fitBlock(text, width, sizes, titleWeight)
	pitch := jsRound(fit.size * blockHeight)
	top := middleY - float64(len(fit.lines))*pitch*half

	for row, line := range fit.lines {
		f.Text(line, middleX, jsRound(top+pitch*(float64(row)+blockBaseline)),
			Font{Face: montserrat(titleWeight, fit.size), Level: level, Align: Center, Max: 0})
	}
}

// item is one block of a stack: a title or small line, an icon, the dots, or anything of a
// known height.
type item struct {
	text     string
	sizes    []float64
	weight   int
	level    int
	maxLines int

	icon      Icon
	iconLevel int

	dots, dotOn int

	height float64
	draw   func(y float64)
}

// blank is an item of nothing, to set the fields of one kind on.
func blank() item {
	return item{
		text: "", sizes: nil, weight: 0, level: 0, maxLines: 0, icon: IconNone, iconLevel: 0,
		dots: 0, dotOn: 0, height: 0, draw: nil,
	}
}

// textItem is a line or two of text.
func textItem(text string, sizes []float64, weight, level, maxLines int) item {
	entry := blank()
	entry.text, entry.sizes, entry.weight, entry.level, entry.maxLines = text, sizes, weight, level, maxLines

	return entry
}

// titleLines is how many lines a title may take.
const titleLines = 2

// title is a title item at sizes, up to two lines.
func title(text string, sizes ...float64) item {
	return textItem(text, sizes, titleWeight, maxLevel, titleLines)
}

// small is a small line, one or two lines.
func small(text string, maxLines int) item {
	return textItem(text, []float64{smallSize}, smallWeight, smallLevel, maxLines)
}

// iconItem is an icon at level.
func iconItem(kind Icon, level int) item {
	entry := blank()
	entry.icon, entry.iconLevel = kind, level

	return entry
}

// dotsItem is n dots, the lit-th lit.
func dotsItem(n, lit int) item {
	entry := blank()
	entry.dots, entry.dotOn = n, lit

	return entry
}

// drawn is an item of height that draw paints at its top.
func drawn(height float64, draw func(top float64)) item {
	entry := blank()
	entry.height, entry.draw = height, draw

	return entry
}

// iconHeight and dotsHeight are what an icon and the dots take in a stack.
const (
	iconHeight = 22
	dotsHeight = 2
	iconMiddle = 11
)

// placed is an item measured for a stack: its title's fit and line pitch.
type placed struct {
	item

	fit   fitted
	pitch float64
}

// measure is entry measured: a text's fit and height, an icon's or the dots' height.
func measure(entry *item) placed {
	out := placed{item: *entry, fit: fitted{size: 0, lines: nil}, pitch: 0}

	switch {
	case entry.text != "":
		out.fit = fitText(entry.text, textWidth, entry.sizes, entry.weight, entry.maxLines)
		out.pitch = jsRound(out.fit.size * lineHeight)
		out.height = float64(len(out.fit.lines)) * out.pitch
	case entry.icon != IconNone:
		out.height = iconHeight
	case entry.dots > 0:
		out.height = dotsHeight
	default: // drawn: its height is its own
	}

	return out
}

// flow stacks items with gap between them, centred between top and bottom: the design's
// flow().
func (f *Frame) flow(items []item, top, bottom float64) {
	all := make([]placed, len(items))
	total := float64(gap * (len(items) - 1))

	for index := range items {
		all[index] = measure(&items[index])
		total += all[index].height
	}

	cursor := jsRound(top + (bottom-top-total)/2)

	for index := range all {
		entry := &all[index]

		switch {
		case entry.text != "":
			for row, line := range entry.fit.lines {
				f.Text(line, centreX, jsRound(cursor+entry.pitch*(float64(row)+baselineAt)),
					Font{Face: montserrat(entry.weight, entry.fit.size), Level: entry.level, Align: Center, Max: 0})
			}
		case entry.icon != IconNone:
			f.Icon(entry.icon, centreX, cursor+iconMiddle, entry.iconLevel)
		case entry.dots > 0:
			f.dots(entry.dots, entry.dotOn, cursor)
		case entry.draw != nil:
			entry.draw(cursor)
		default: // an empty item takes its height alone
		}

		cursor += entry.height + gap
	}
}

// dots draws n small bars centred on the panel at y, the on-th lit: the design's dots().
//
//nolint:mnd // the design's sizes and grey levels (design-screens.js)
func (f *Frame) dots(n, lit int, top float64) {
	width := float64(n*6 - 2)
	left := jsRound(frameWidth/2 - width/2)

	for dot := range n {
		grey := 5
		if dot == lit {
			grey = maxLevel
		}

		f.Rect(left+float64(dot*6), top, 4, 2, grey)
	}
}

// label draws s in the pixel font, upper case, full white unless level says otherwise.
func (f *Frame) label(text string, x, baseline float64, level int, align Align) {
	f.Text(strings.ToUpper(text), x, baseline, Font{Face: pix8, Level: level, Align: align, Max: 0})
}
