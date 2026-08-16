// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package screen draws the SoundTouch 20 panel screens of the project's design
// (app/oled/design/design-screens.js) the way a browser canvas does: anti-aliased shapes and
// text composited on a black 128x100 surface, then rounded to the panel's 16 grey levels.
// Tests hold every screen to Chromium's rendering of the design.
package screen

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"sync"
)

const (
	W = 128
	H = 100
)

// Frame is one screen being drawn: linear light, 0..255 per pixel.
type Frame struct{ lum [W * H]float64 }

func level(l int) float64 { return float64(l * 17) }

// paint composites colour level l over the pixel at the given coverage, as canvas
// source-over does.
func (f *Frame) paint(x, y int, l int, cover float64) {
	if x < 0 || y < 0 || x >= W || y >= H || cover <= 0 {
		return
	}
	if cover > 1 {
		cover = 1
	}
	p := &f.lum[y*W+x]
	*p = *p*(1-cover) + level(l)*cover
}

// ss is the supersampling grid per axis for shapes.
const ss = 8

// Pixel classes for fill's quick test: wholly outside, sampled, wholly inside.
const (
	pxOut = iota - 1
	pxSample
	pxIn
)

// sampleReach is how far a pixel's outermost sample lies from its centre.
var sampleReach = math.Sqrt2 * (0.5 - 0.5/ss)

// fill paints every pixel in the box whose samples fall inside the shape. class, when
// not nil, settles a pixel from its centre before sampling it 64 times: the ST20's CPU
// took ~100 ms a frame sampling every pixel of a bar or an icon.
func (f *Frame) fill(x0, y0, x1, y1 float64, l int, class func(cx, cy float64) int, inside func(x, y float64) bool) {
	f.fillMask(coverage(x0, y0, x1, y1, class, inside), l)
}

// covered is one pixel of a shape and how much of it the shape covers.
type covered struct {
	x, y  int
	cover float64
}

func (f *Frame) fillMask(mask []covered, l int) {
	for _, c := range mask {
		f.paint(c.x, c.y, l, c.cover)
	}
}

// strokes keeps the coverage of the icons' strokes and rounded bars: the same few shapes
// on every screen, and the slowest thing the ST20 draws (a Bluetooth logo took ~80 ms).
var strokes struct {
	sync.Mutex
	m map[string][]covered
}

// fillCached is fill for a shape that key names completely.
func (f *Frame) fillCached(key string, x0, y0, x1, y1 float64, l int, class func(cx, cy float64) int, inside func(x, y float64) bool) {
	strokes.Lock()
	mask, ok := strokes.m[key]
	strokes.Unlock()
	if !ok {
		mask = coverage(x0, y0, x1, y1, class, inside)
		strokes.Lock()
		if strokes.m == nil || len(strokes.m) > 256 { // crude bound; progress bars add one shape per width
			strokes.m = map[string][]covered{}
		}
		strokes.m[key] = mask
		strokes.Unlock()
	}
	f.fillMask(mask, l)
}

// samples counts the pixel's samples inside the shape, of ss*ss.
func samples(px, py int, inside func(x, y float64) bool) int {
	n := 0
	for sy := range ss {
		for sx := range ss {
			if inside(float64(px)+(float64(sx)+0.5)/ss, float64(py)+(float64(sy)+0.5)/ss) {
				n++
			}
		}
	}
	return n
}

func coverage(x0, y0, x1, y1 float64, class func(cx, cy float64) int, inside func(x, y float64) bool) []covered {
	var mask []covered
	for py := int(math.Floor(y0)); py < int(math.Ceil(y1)); py++ {
		for px := int(math.Floor(x0)); px < int(math.Ceil(x1)); px++ {
			if class != nil {
				switch class(float64(px)+0.5, float64(py)+0.5) {
				case pxOut:
					continue
				case pxIn:
					mask = append(mask, covered{px, py, 1})
					continue
				}
			}
			if n := samples(px, py, inside); n > 0 {
				mask = append(mask, covered{px, py, float64(n) / (ss * ss)})
			}
		}
	}
	return mask
}

// convex settles a pixel of a convex shape: one whose corner samples are all inside is
// wholly inside.
func convex(inside func(x, y float64) bool) func(cx, cy float64) int {
	d := 0.5 - 0.5/ss
	return func(cx, cy float64) int {
		if inside(cx-d, cy-d) && inside(cx+d, cy-d) && inside(cx-d, cy+d) && inside(cx+d, cy+d) {
			return pxIn
		}
		return pxSample
	}
}

// byDistance settles a pixel by its centre's distance dist to a stroke's middle: further
// than half the width plus a pixel's reach, no sample can be inside; nearer than half the
// width less that reach (only when solid, i.e. no cut caps or arc ends), all are.
func byDistance(dist func(cx, cy float64) float64, hw float64, solid bool) func(cx, cy float64) int {
	return func(cx, cy float64) int {
		d := dist(cx, cy)
		switch {
		case d > hw+sampleReach:
			return pxOut
		case solid && d < hw-sampleReach:
			return pxIn
		}
		return pxSample
	}
}

// Rect is fillRect.
func (f *Frame) Rect(x, y, w, h float64, l int) {
	inside := func(px, py float64) bool { return px >= x && px < x+w && py >= y && py < y+h }
	f.fill(x, y, x+w, y+h, l, convex(inside), inside)
}

// RRect is roundRect + fill.
func (f *Frame) RRect(x, y, w, h, r float64, l int) {
	r = math.Min(r, math.Min(w, h)/2)
	inside := func(px, py float64) bool {
		if px < x || px >= x+w || py < y || py >= y+h {
			return false
		}
		cx := math.Max(x+r, math.Min(px, x+w-r))
		cy := math.Max(y+r, math.Min(py, y+h-r))
		return (px-cx)*(px-cx)+(py-cy)*(py-cy) <= r*r
	}
	f.fillCached(fmt.Sprint("R", x, y, w, h, r), x, y, x+w, y+h, l, convex(inside), inside)
}

// Pt is a point in panel pixels.
type Pt struct{ X, Y float64 }

// Poly fills a closed polygon (non-zero rule; the design's are simple).
func (f *Frame) Poly(pts []Pt, l int) {
	x0, y0, x1, y1 := bounds(pts, 0)
	f.fillCached(fmt.Sprint("P", pts), x0, y0, x1, y1, l, nil, func(px, py float64) bool {
		in := false
		for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
			a, b := pts[i], pts[j]
			if (a.Y > py) != (b.Y > py) && px < (b.X-a.X)*(py-a.Y)/(b.Y-a.Y)+a.X {
				in = !in
			}
		}
		return in
	})
}

// Line strokes an open polyline. round gives round caps; joins are always round, which
// is what the design's two multi-segment strokes ask for.
func (f *Frame) Line(pts []Pt, width float64, round bool, l int) {
	hw := width / 2
	x0, y0, x1, y1 := bounds(pts, hw+1)
	dist := func(px, py float64) float64 {
		best := math.Inf(1)
		for i := 1; i < len(pts); i++ {
			a, b := pts[i-1], pts[i]
			dx, dy := b.X-a.X, b.Y-a.Y
			t := math.Max(0, math.Min(1, ((px-a.X)*dx+(py-a.Y)*dy)/(dx*dx+dy*dy)))
			best = math.Min(best, math.Hypot(px-(a.X+t*dx), py-(a.Y+t*dy)))
		}
		return best
	}
	f.fillCached(fmt.Sprint("L", pts, width, round), x0, y0, x1, y1, l, byDistance(dist, hw, round), func(px, py float64) bool {
		for i := 1; i < len(pts); i++ {
			a, b := pts[i-1], pts[i]
			dx, dy := b.X-a.X, b.Y-a.Y
			t := ((px-a.X)*dx + (py-a.Y)*dy) / (dx*dx + dy*dy)
			capped := (i == 1 && t < 0) || (i == len(pts)-1 && t > 1)
			if capped && !round {
				continue
			}
			t = math.Max(0, math.Min(1, t))
			ex, ey := px-(a.X+t*dx), py-(a.Y+t*dy)
			if ex*ex+ey*ey <= hw*hw {
				return true
			}
		}
		return false
	})
}

// Arc strokes a circle arc from a0 to a1 radians (clockwise, as canvas), butt caps.
func (f *Frame) Arc(cx, cy, r, a0, a1, width float64, l int) {
	hw := width / 2
	full := a1-a0 >= 2*math.Pi
	ring := func(px, py float64) float64 { return math.Abs(math.Hypot(px-cx, py-cy) - r) }
	f.fillCached(fmt.Sprint("A", cx, cy, r, a0, a1, width), cx-r-hw, cy-r-hw, cx+r+hw+1, cy+r+hw+1, l, byDistance(ring, hw, full), func(px, py float64) bool {
		d2 := (px-cx)*(px-cx) + (py-cy)*(py-cy)
		if d2 > (r+hw)*(r+hw) || (r > hw && d2 < (r-hw)*(r-hw)) {
			return false
		}
		if full {
			return true
		}
		a := math.Atan2(py-cy, px-cx)
		for a < a0 {
			a += 2 * math.Pi
		}
		return a <= a1
	})
}

func bounds(pts []Pt, pad float64) (x0, y0, x1, y1 float64) {
	x0, y0, x1, y1 = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		x0, y0 = math.Min(x0, p.X), math.Min(y0, p.Y)
		x1, y1 = math.Max(x1, p.X), math.Max(y1, p.Y)
	}
	return x0 - pad, y0 - pad, x1 + pad + 1, y1 + pad + 1
}

// Pix is the frame as the panel shows it: 16 levels, each written as level*17. dim is
// the brightest level allowed (15 for none); every level is scaled down to it.
func (f *Frame) Pix(dim int) []byte {
	out := make([]byte, W*H)
	for i, v := range f.lum {
		q := int(math.Round(v / 255 * 15))
		if dim < 15 {
			q = int(math.Round(float64(q*dim) / 15))
		}
		out[i] = byte(q * 17) //nolint:gosec // q is a grey level, 0..15
	}
	return out
}

// --- text -----------------------------------------------------------------------------

//go:embed assets/st20-atlas.png
var atlasPNG []byte

//go:embed assets/st20-atlas.txt.gz
var atlasTxt []byte

type glyph struct {
	x, y, w, h, left, top int
	adv                   float64 // pixels
}

// face holds each glyph at phases sub-pixel offsets: glyph i of a rune is drawn i/phases
// of a pixel to the right.
type face struct {
	phases int
	glyphs map[rune][]glyph
	kern   map[[2]rune]int // pair adjustment in font units
	scale  float64         // pixels per font unit
}

var (
	atlasOnce sync.Once
	atlas     *image.Gray
	faces     map[string]face
)

func loadAtlas() {
	img, err := png.Decode(bytes.NewReader(atlasPNG))
	if err != nil {
		panic("st20 atlas: " + err.Error())
	}
	g, ok := img.(*image.Gray)
	if !ok {
		panic("st20 atlas is not greyscale")
	}
	zr, err := gzip.NewReader(bytes.NewReader(atlasTxt))
	if err != nil {
		panic("st20 atlas: " + err.Error())
	}
	txt, err := io.ReadAll(zr)
	if err != nil {
		panic("st20 atlas: " + err.Error())
	}
	atlas, faces = g, map[string]face{}
	parseMetrics(txt)
}

// parseMetrics reads the atlas metrics: ~86,000 lines, which strings.Fields and strconv
// took 1.7 s to parse on the ST20, so it splits and converts the bytes itself.
func parseMetrics(txt []byte) {
	var cur face
	kerns, upems := map[string]map[[2]rune]int{}, map[string]float64{}
	var kern map[[2]rune]int
	var f [10][]byte
	for len(txt) > 0 {
		line := txt
		if i := bytes.IndexByte(txt, '\n'); i >= 0 {
			line, txt = txt[:i], txt[i+1:]
		} else {
			txt = nil
		}
		n := fields(line, &f)
		switch {
		case n == 4 && f[0][0] == 'w': // w <font file> <weight> <units per em>
			kern = map[[2]rune]int{}
			kerns[string(f[1])+string(f[2])] = kern
			upems[string(f[1])+string(f[2])] = number(f[3])
		case n == 4 && f[0][0] == 'k': // k <left> <right> <font units>
			kern[[2]rune{hex(f[1]), hex(f[2])}] = int(number(f[3]))
		case n == 6 && f[0][0] == 'f': // f <name> <phases> <font file> <weight> <pixel size>
			cur = face{phases: max(1, int(number(f[2]))), glyphs: map[rune][]glyph{}, kern: kerns[string(f[3])+string(f[4])]}
			if u := upems[string(f[3])+string(f[4])]; u > 0 {
				cur.scale = number(f[5]) / u
			}
			faces[string(f[1])] = cur
		case n == 10 && f[0][0] == 'g': // g <rune> <phase> <x> <y> <w> <h> <left> <top> <advance>
			r := hex(f[1])
			cur.glyphs[r] = append(cur.glyphs[r], glyph{int(number(f[3])), int(number(f[4])), int(number(f[5])),
				int(number(f[6])), int(number(f[7])), int(number(f[8])), number(f[9])})
		}
	}
}

// fields splits line at spaces into f, returning how many it found (at most len(f)).
func fields(line []byte, f *[10][]byte) int {
	n := 0
	for len(line) > 0 && n < len(f) {
		i := bytes.IndexByte(line, ' ')
		if i < 0 {
			i = len(line)
		}
		if i > 0 {
			f[n] = line[:i]
			n++
		}
		line = line[min(i+1, len(line)):]
	}
	return n
}

// number reads a decimal such as -12 or 4.5625. Dividing the digits by a power of ten
// rounds once, as strconv.ParseFloat does, so the result is the same.
func number(b []byte) float64 {
	neg := len(b) > 0 && b[0] == '-'
	if neg {
		b = b[1:]
	}
	v, scale := 0.0, 1.0
	frac := false
	for _, c := range b {
		switch {
		case c == '.':
			frac = true
		case c >= '0' && c <= '9':
			v = v*10 + float64(c-'0')
			if frac {
				scale *= 10
			}
		}
	}
	if neg {
		v = -v
	}
	return v / scale
}

func hex(b []byte) rune {
	var v rune
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | rune(c-'a'+10)
		}
	}
	return v
}

func faceOf(name string) face {
	atlasOnce.Do(loadAtlas)
	return faces[name]
}
