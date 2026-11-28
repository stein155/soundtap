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

// Font is a text style: Face names an atlas face such as "m700-18" (Montserrat, weight,
// pixel size) or "pix-8" (Silkscreen).
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

// Has says whether the face has a glyph of its own for r; Width and Text draw a rune it
// lacks as '?'.
func Has(faceName string, r rune) bool {
	_, ok := faceOf(faceName).glyphs[r]

	return ok
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

// thaiContext is what the form of a Thai mark depends on: the tall consonant it sits on
// and the upper vowel it stacks on, 0 for none. atlas.py bakes the forms by the same rule.
type thaiContext struct {
	tall, above rune
}

// Thai letters thaiForm tells apart.
const (
	thaiPoPla      = 0x0E1B // ป
	thaiFoFa       = 0x0E1D // ฝ
	thaiFoFan      = 0x0E1F // ฟ
	thaiLoChula    = 0x0E2C // ฬ
	thaiMaiHanAkat = 0x0E31 // ั
	thaiSaraI      = 0x0E34 // ิ, then ี ึ ื
	thaiSaraUee    = 0x0E37 // ื
	thaiPhinthu    = 0x0E3A // ฺ, the last of the vowels below
	thaiMaitaikhu  = 0x0E47 // ็, the first of the marks above after the vowels
	thaiYamakkan   = 0x0E4E // ๎, the last of them
)

// isThaiMark is a combining Thai mark: a vowel above or below, a tone mark or another
// mark above.
func isThaiMark(r rune) bool {
	return r == thaiMaiHanAkat || r >= thaiSaraI && r <= thaiPhinthu || r >= thaiMaitaikhu && r <= thaiYamakkan
}

// isThaiAbove is an upper vowel, which a tone mark stacks on.
func isThaiAbove(r rune) bool {
	return r == thaiMaiHanAkat || r >= thaiSaraI && r <= thaiSaraUee || r == thaiMaitaikhu
}

// isThaiTall is a consonant whose ascender reaches the marks above it.
func isThaiTall(r rune) bool {
	return r == thaiPoPla || r == thaiFoFa || r == thaiFoFan || r == thaiLoChula
}

// thaiForm is char's glyph at phase, in its Thai form after the letters before it, and moves
// ctx past char. A Thai mark is drawn raised above an upper vowel and clear of the ascender of
// ป ฝ ฟ ฬ, as the font's shaping draws it; any other rune is fc.glyphFor(char, phase).
func (fc face) thaiForm(ctx *thaiContext, char rune, phase int) glyph {
	plain := fc.glyphFor(char, phase)
	if !isThaiMark(char) {
		*ctx = thaiContext{tall: 0, above: 0}
		if isThaiTall(char) {
			ctx.tall = char
		}

		return plain
	}

	form, ok := fc.thai[[3]rune{ctx.tall, ctx.above, char}]
	if isThaiAbove(char) {
		ctx.above = char
	}

	if !ok || phase >= len(form) {
		return plain
	}

	drawn := form[phase]
	drawn.adv = plain.adv

	return drawn
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

	faceOf(font.Face).layout(text, anchorX, baseline, func(_ rune, drawn glyph, originX, originY int) {
		for row := range drawn.h {
			for col := range drawn.w {
				alpha := fontAtlas.img.GrayAt(drawn.x+col, drawn.y+row).Y
				f.paint(originX+col, originY+row, font.Level, float64(alpha)/fullScale)
			}
		}
	})
}

// layout places text from anchorX on baseline: it calls place with each rune, the glyph
// drawn for it and the pixel the glyph's top-left corner lands on.
func (fc face) layout(text string, anchorX, baseline float64, place func(r rune, drawn glyph, originX, originY int)) {
	var thai thaiContext

	pen, prev := 0.0, rune(0)
	for _, char := range text {
		pen += fc.kerning(prev, char)
		prev = char
		at := anchorX + pen
		whole := math.Floor(at)

		phase := int(math.Round((at - whole) * float64(fc.phases)))
		if phase == fc.phases {
			whole, phase = whole+1, 0
		}

		drawn := fc.thaiForm(&thai, char, phase)
		place(char, drawn, int(whole)+drawn.left, int(math.Round(baseline))-drawn.top)

		pen += drawn.adv
	}
}

// TextMid is the design's textMid: text vertically centred on centreY by its ink.
func (f *Frame) TextMid(text string, anchorX, centreY float64, font Font) {
	fontFace := faceOf(font.Face)
	asc, desc := 0, 0

	var thai thaiContext

	for _, char := range text {
		drawn := fontFace.thaiForm(&thai, char, 0)
		asc, desc = max(asc, drawn.top), max(desc, drawn.h-drawn.top)
	}

	f.Text(text, anchorX, math.Floor(centreY+float64(asc-desc)*half+half), font)
}
