// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import "math"

// Align is canvas textAlign.
type Align int

const (
	Left Align = iota
	Center
	Right
)

// Font is a text style: Face names an atlas face such as "j800-15" (Jakarta, weight,
// pixel size) or "pix-7" (Silkscreen).
type Font struct {
	Face  string
	Level int
	Align Align
	Max   float64 // > 0 truncates with an ellipsis, as the design's text() does
}

// Width is measureText(s).width.
func Width(faceName, s string) float64 {
	fontFace := faceOf(faceName)

	width, prev := 0.0, rune(0)
	for _, char := range s {
		width += fontFace.kerning(prev, char) + fontFace.glyphFor(char, 0).adv
		prev = char
	}

	return width
}

// kerning is the pair adjustment in pixels between a and b.
func (fc face) kerning(a, b rune) float64 {
	return float64(fc.kern[[2]rune{a, b}]) * fc.scale
}

// glyphFor is r at sub-pixel phase 0..phases-1; a rune the face lacks draws as '?'.
func (fc face) glyphFor(r rune, phase int) glyph {
	phased, ok := fc.glyphs[r]
	if !ok {
		phased = fc.glyphs['?']
	}

	if phase >= len(phased) {
		return glyph{x: 0, y: 0, w: 0, h: 0, left: 0, top: 0, adv: 0}
	}

	return phased[phase]
}

// fitDrop is how many runes each step of Fit takes off the end before it adds the
// ellipsis: the last one (the ellipsis, once there) and the one before it.
const fitDrop = 2

// Fit shortens s the way the design's text() does until it is at most max wide.
func Fit(faceName, s string, limit float64) string {
	runes := []rune(s)
	for Width(faceName, string(runes)) > limit && len(runes) > 1 {
		runes = append(runes[:max0(len(runes)-fitDrop)], '…')
	}

	return string(runes)
}

func max0(n int) int {
	if n < 0 {
		return 0
	}

	return n
}

// Text is fillText at baseline y.
func (f *Frame) Text(text string, anchorX, baseline float64, font Font) {
	if font.Max > 0 {
		text = Fit(font.Face, text, font.Max)
	}

	// Left leaves anchorX where the text starts.
	if font.Align == Center {
		anchorX -= Width(font.Face, text) * half
	}

	if font.Align == Right {
		anchorX -= Width(font.Face, text)
	}

	fontFace := faceOf(font.Face)

	pen, prev := 0.0, rune(0)
	for _, char := range text {
		pen += fontFace.kerning(prev, char)
		prev = char
		at := anchorX + pen
		whole := math.Floor(at)

		phase := int(math.Round((at - whole) * float64(fontFace.phases)))
		if phase == fontFace.phases {
			whole, phase = whole+1, 0
		}

		drawn := fontFace.glyphFor(char, phase)
		originX := int(whole) + drawn.left

		originY := int(math.Round(baseline)) - drawn.top
		for row := range drawn.h {
			for col := range drawn.w {
				alpha := fontAtlas.img.GrayAt(drawn.x+col, drawn.y+row).Y
				f.paint(originX+col, originY+row, font.Level, float64(alpha)/fullScale)
			}
		}

		pen += drawn.adv
	}
}

// TextMid is the design's textMid: text vertically centred on centreY by its ink.
func (f *Frame) TextMid(text string, anchorX, centreY float64, font Font) {
	fontFace := faceOf(font.Face)
	asc, desc := 0, 0

	for _, char := range text {
		drawn := fontFace.glyphFor(char, 0)
		asc, desc = max(asc, drawn.top), max(desc, drawn.h-drawn.top)
	}

	f.Text(text, anchorX, math.Floor(centreY+float64(asc-desc)*half+half), font)
}
