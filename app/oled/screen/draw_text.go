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
	fc := faceOf(faceName)
	w, prev := 0.0, rune(0)
	for _, r := range s {
		w += fc.kerning(prev, r) + fc.glyphFor(r, 0).adv
		prev = r
	}
	return w
}

// kerning is the pair adjustment in pixels between a and b.
func (fc face) kerning(a, b rune) float64 {
	return float64(fc.kern[[2]rune{a, b}]) * fc.scale
}

// glyphFor is r at sub-pixel phase 0..phases-1; a rune the face lacks draws as '?'.
func (fc face) glyphFor(r rune, phase int) glyph {
	g, ok := fc.glyphs[r]
	if !ok {
		g = fc.glyphs['?']
	}
	if phase >= len(g) {
		return glyph{}
	}
	return g[phase]
}

// Fit shortens s the way the design's text() does until it is at most max wide.
func Fit(faceName, s string, limit float64) string {
	r := []rune(s)
	for Width(faceName, string(r)) > limit && len(r) > 1 {
		r = append(r[:max0(len(r)-2)], '…')
	}
	return string(r)
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// Text is fillText at baseline y.
func (f *Frame) Text(s string, x, y float64, ft Font) {
	if ft.Max > 0 {
		s = Fit(ft.Face, s, ft.Max)
	}
	switch ft.Align {
	case Center:
		x -= Width(ft.Face, s) / 2
	case Right:
		x -= Width(ft.Face, s)
	}
	fc := faceOf(ft.Face)
	pen, prev := 0.0, rune(0)
	for _, r := range s {
		pen += fc.kerning(prev, r)
		prev = r
		at := x + pen
		whole := math.Floor(at)
		phase := int(math.Round((at - whole) * float64(fc.phases)))
		if phase == fc.phases {
			whole, phase = whole+1, 0
		}
		g := fc.glyphFor(r, phase)
		ox := int(whole) + g.left
		oy := int(math.Round(y)) - g.top
		for gy := range g.h {
			for gx := range g.w {
				a := atlas.GrayAt(g.x+gx, g.y+gy).Y
				f.paint(ox+gx, oy+gy, ft.Level, float64(a)/255)
			}
		}
		pen += g.adv
	}
}

// TextMid is the design's textMid: text vertically centred on cy by its ink.
func (f *Frame) TextMid(s string, x, cy float64, ft Font) {
	fc := faceOf(ft.Face)
	asc, desc := 0, 0
	for _, r := range s {
		g := fc.glyphFor(r, 0)
		asc, desc = max(asc, g.top), max(desc, g.h-g.top)
	}
	f.Text(s, x, math.Floor(cy+float64(asc-desc)/2+0.5), ft)
}
