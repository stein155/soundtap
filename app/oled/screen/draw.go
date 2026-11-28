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

// The panel's size in pixels.
const (
	frameWidth  = 128
	frameHeight = 100
)

const (
	// maxLevel is the panel's brightest grey level: it shows 16, 0 to 15.
	maxLevel = 15
	// levelStep is a grey level's byte on the panel: level*17, so 15 is 255.
	levelStep = 17
	// fullScale is a pixel's full light, and a glyph's full coverage in the atlas.
	fullScale = 255
	// half is a half: of a width, a side, or a pixel (its centre).
	half = 0.5
	// fullTurn is a whole circle in radians.
	fullTurn = 2 * math.Pi
)

// Frame is one screen being drawn: linear light, 0..255 per pixel.
type Frame struct {
	lum [frameWidth * frameHeight]float64
}

// newFrame is a black frame.
func newFrame() *Frame {
	return &Frame{lum: [frameWidth * frameHeight]float64{}}
}

// Rect is fillRect.
func (f *Frame) Rect(x, y, w, h float64, grey int) {
	inside := func(px, py float64) bool { return px >= x && px < x+w && py >= y && py < y+h }
	f.fill(box{minX: x, minY: y, maxX: x + w, maxY: y + h}, grey, convex(inside), inside)
}

// RRect is roundRect + fill.
func (f *Frame) RRect(left, top, width, height, radius float64, grey int) {
	radius = math.Min(radius, math.Min(width, height)*half)
	inside := func(sampleX, sampleY float64) bool {
		if sampleX < left || sampleX >= left+width || sampleY < top || sampleY >= top+height {
			return false
		}

		nearX := math.Max(left+radius, math.Min(sampleX, left+width-radius))
		nearY := math.Max(top+radius, math.Min(sampleY, top+height-radius))

		return (sampleX-nearX)*(sampleX-nearX)+(sampleY-nearY)*(sampleY-nearY) <= radius*radius
	}
	area := box{minX: left, minY: top, maxX: left + width, maxY: top + height}
	f.fillCached(fmt.Sprint("R", left, top, width, height, radius), area, grey, convex(inside), inside)
}

// Pt is a point in panel pixels.
type Pt struct{ X, Y float64 }

// Poly fills a closed polygon (non-zero rule; the design's are simple).
func (f *Frame) Poly(pts []Pt, grey int) {
	f.fillCached(fmt.Sprint("P", pts), bounds(pts, 0), grey, nil, func(sampleX, sampleY float64) bool {
		odd := false

		for cur, prev := 0, len(pts)-1; cur < len(pts); prev, cur = cur, cur+1 {
			from, to := pts[cur], pts[prev]
			if (from.Y > sampleY) != (to.Y > sampleY) &&
				sampleX < (to.X-from.X)*(sampleY-from.Y)/(to.Y-from.Y)+from.X {
				odd = !odd
			}
		}

		return odd
	})
}

// Cap is how Line ends a stroke.
type Cap int

// Line caps, as canvas lineCap.
const (
	ButtCap  Cap = iota // cut square at the end points
	RoundCap            // a half circle past each end point
)

// Line strokes an open polyline with the given caps; joins are always round, which is what
// the design's two multi-segment strokes ask for.
func (f *Frame) Line(pts []Pt, width float64, caps Cap, grey int) {
	halfWidth := width * half
	round := caps == RoundCap
	dist := func(sampleX, sampleY float64) float64 {
		nearest := math.Inf(1)

		for seg := 1; seg < len(pts); seg++ {
			from, to := pts[seg-1], pts[seg]
			stepX, stepY := to.X-from.X, to.Y-from.Y
			along := math.Max(0, math.Min(1, ((sampleX-from.X)*stepX+(sampleY-from.Y)*stepY)/(stepX*stepX+stepY*stepY)))
			nearest = math.Min(nearest, math.Hypot(sampleX-(from.X+along*stepX), sampleY-(from.Y+along*stepY)))
		}

		return nearest
	}
	inside := func(sampleX, sampleY float64) bool {
		for seg := 1; seg < len(pts); seg++ {
			from, to := pts[seg-1], pts[seg]
			stepX, stepY := to.X-from.X, to.Y-from.Y
			along := ((sampleX-from.X)*stepX + (sampleY-from.Y)*stepY) / (stepX*stepX + stepY*stepY)

			capped := (seg == 1 && along < 0) || (seg == len(pts)-1 && along > 1)
			if capped && !round {
				continue
			}

			along = math.Max(0, math.Min(1, along))

			offX, offY := sampleX-(from.X+along*stepX), sampleY-(from.Y+along*stepY)
			if offX*offX+offY*offY <= halfWidth*halfWidth {
				return true
			}
		}

		return false
	}
	key := fmt.Sprint("L", pts, width, round)
	f.fillCached(key, bounds(pts, halfWidth+1), grey, byDistance(dist, halfWidth, round), inside)
}

// Arc strokes a circle arc from start to end radians (clockwise, as canvas) with the
// given caps.
func (f *Frame) Arc(centreX, centreY, radius, start, end, width float64, caps Cap, grey int) {
	halfWidth := width * half
	full := end-start >= fullTurn
	round := caps == RoundCap && !full
	capAt := func(angle float64) Pt {
		return Pt{centreX + radius*math.Cos(angle), centreY + radius*math.Sin(angle)}
	}
	ends := [2]Pt{capAt(start), capAt(end)}
	nearCap := func(sampleX, sampleY float64) float64 {
		return math.Min(math.Hypot(sampleX-ends[0].X, sampleY-ends[0].Y),
			math.Hypot(sampleX-ends[1].X, sampleY-ends[1].Y))
	}
	ring := func(sampleX, sampleY float64) float64 {
		d := math.Abs(math.Hypot(sampleX-centreX, sampleY-centreY) - radius)
		if round {
			d = math.Min(d, nearCap(sampleX, sampleY))
		}

		return d
	}
	inside := func(sampleX, sampleY float64) bool {
		if round && nearCap(sampleX, sampleY) <= halfWidth {
			return true
		}

		distSq := (sampleX-centreX)*(sampleX-centreX) + (sampleY-centreY)*(sampleY-centreY)
		if distSq > (radius+halfWidth)*(radius+halfWidth) ||
			(radius > halfWidth && distSq < (radius-halfWidth)*(radius-halfWidth)) {
			return false
		}

		if full {
			return true
		}

		angle := math.Atan2(sampleY-centreY, sampleX-centreX)
		for angle < start {
			angle += fullTurn
		}

		return angle <= end
	}
	area := box{
		minX: centreX - radius - halfWidth,
		minY: centreY - radius - halfWidth,
		maxX: centreX + radius + halfWidth + 1,
		maxY: centreY + radius + halfWidth + 1,
	}
	key := fmt.Sprint("A", centreX, centreY, radius, start, end, width, round)
	f.fillCached(key, area, grey, byDistance(ring, halfWidth, full), inside)
}

// Disk fills a circle: arc(x, y, r, 0, 2π) + fill.
func (f *Frame) Disk(centreX, centreY, radius float64, grey int) {
	inside := func(sampleX, sampleY float64) bool {
		return (sampleX-centreX)*(sampleX-centreX)+(sampleY-centreY)*(sampleY-centreY) <= radius*radius
	}
	area := box{minX: centreX - radius, minY: centreY - radius, maxX: centreX + radius + 1, maxY: centreY + radius + 1}
	f.fillCached(fmt.Sprint("D", centreX, centreY, radius), area, grey, convex(inside), inside)
}

// StrokeRect is strokeRect: the rectangle's outline, width wide and centred on its edges,
// with round joins (as the icons draw it) or square ones (canvas's default miter).
//
//nolint:revive // flag-parameter: round is the stroke's join, part of what is drawn
func (f *Frame) StrokeRect(left, top, width, height, lineWidth float64, round bool, grey int) {
	halfWidth := lineWidth * half
	edge := func(sampleX, sampleY float64) float64 {
		outX := math.Max(math.Max(left-sampleX, sampleX-(left+width)), 0)
		outY := math.Max(math.Max(top-sampleY, sampleY-(top+height)), 0)

		if outX > 0 || outY > 0 {
			if !round {
				return math.Max(outX, outY)
			}

			return math.Hypot(outX, outY)
		}

		return math.Min(math.Min(sampleX-left, left+width-sampleX), math.Min(sampleY-top, top+height-sampleY))
	}
	inside := func(sampleX, sampleY float64) bool { return edge(sampleX, sampleY) <= halfWidth }
	area := strokeBox(left, top, width, height, halfWidth)
	f.fillCached(fmt.Sprint("S", left, top, width, height, lineWidth, round), area, grey, nil, inside)
}

// rounded is a rounded rectangle.
type rounded struct{ left, top, width, height, radius float64 }

// holds says the point is inside the rounded rectangle.
func (r rounded) holds(sampleX, sampleY float64) bool {
	if sampleX < r.left || sampleX >= r.left+r.width || sampleY < r.top || sampleY >= r.top+r.height {
		return false
	}

	radius := math.Max(r.radius, 0)
	nearX := math.Max(r.left+radius, math.Min(sampleX, r.left+r.width-radius))
	nearY := math.Max(r.top+radius, math.Min(sampleY, r.top+r.height-radius))

	return (sampleX-nearX)*(sampleX-nearX)+(sampleY-nearY)*(sampleY-nearY) <= radius*radius
}

// strokeBox is the area a rectangle's outline halfWidth either side of its edges covers.
func strokeBox(left, top, width, height, halfWidth float64) box {
	return box{
		minX: left - halfWidth, minY: top - halfWidth,
		maxX: left + width + halfWidth + 1, maxY: top + height + halfWidth + 1,
	}
}

// StrokeRRect is roundRect + stroke: the band between the rounded rectangle grown and shrunk
// by half the line width.
func (f *Frame) StrokeRRect(left, top, width, height, radius, lineWidth float64, grey int) {
	halfWidth := lineWidth * half
	radius = math.Min(radius, math.Min(width, height)*half)
	outer := rounded{left - halfWidth, top - halfWidth, width + lineWidth, height + lineWidth, radius + halfWidth}
	inner := rounded{left + halfWidth, top + halfWidth, width - lineWidth, height - lineWidth, radius - halfWidth}
	inside := func(sampleX, sampleY float64) bool {
		return outer.holds(sampleX, sampleY) && !inner.holds(sampleX, sampleY)
	}
	area := strokeBox(left, top, width, height, halfWidth)
	f.fillCached(fmt.Sprint("O", left, top, width, height, radius, lineWidth), area, grey, nil, inside)
}

// Pix is the frame as the panel shows it: 16 levels, each written as level*17. dim is
// the brightest level allowed (15 for none); every level is scaled down to it.
func (f *Frame) Pix(dim int) []byte {
	out := make([]byte, frameWidth*frameHeight)

	for idx, lum := range &f.lum {
		grey := int(math.Round(lum / fullScale * maxLevel))
		if dim < maxLevel {
			grey = int(math.Round(float64(grey*dim) / maxLevel))
		}

		out[idx] = byte(grey * levelStep) //nolint:gosec // grey is a grey level, 0..15
	}

	return out
}

// paint composites colour level grey over the pixel at the given coverage, as canvas
// source-over does.
func (f *Frame) paint(pixX, pixY, grey int, cover float64) {
	if pixX < 0 || pixY < 0 || pixX >= frameWidth || pixY >= frameHeight || cover <= 0 {
		return
	}

	if cover > 1 {
		cover = 1
	}

	pixel := &f.lum[pixY*frameWidth+pixX]
	*pixel = *pixel*(1-cover) + level(grey)*cover
}

// fill paints every pixel in the box whose samples fall inside the shape. class, when
// not nil, settles a pixel from its centre before sampling it 64 times: the ST20's CPU
// took ~100 ms a frame sampling every pixel of a bar or an icon.
func (f *Frame) fill(area box, grey int, class func(cx, cy float64) int, inside func(x, y float64) bool) {
	f.fillMask(coverage(area, class, inside), grey)
}

func (f *Frame) fillMask(mask []covered, grey int) {
	for _, c := range mask {
		f.paint(c.x, c.y, grey, c.cover)
	}
}

// fillCached is fill for a shape that key names completely.
func (f *Frame) fillCached(
	key string, area box, grey int, class func(cx, cy float64) int, inside func(x, y float64) bool,
) {
	strokes.Lock()
	mask, ok := strokes.m[key]
	strokes.Unlock()

	if !ok {
		mask = coverage(area, class, inside)

		strokes.Lock()
		if strokes.m == nil || len(strokes.m) > maxCachedShapes {
			strokes.m = map[string][]covered{}
		}

		strokes.m[key] = mask
		strokes.Unlock()
	}

	f.fillMask(mask, grey)
}

func level(grey int) float64 { return float64(grey * levelStep) }

// superSamples is the supersampling grid per axis for shapes.
const superSamples = 8

// Pixel classes for fill's quick test: wholly outside, sampled, wholly inside.
const (
	pxOut = iota - 1
	pxSample
	pxIn
)

// sampleInset is how far a pixel's outermost sample lies from its centre along each axis.
const sampleInset = half - half/superSamples

// sampleReach is how far a pixel's outermost sample lies from its centre.
const sampleReach = math.Sqrt2 * sampleInset

// box is the area a shape is drawn in, in panel pixels.
type box struct{ minX, minY, maxX, maxY float64 }

// covered is one pixel of a shape and how much of it the shape covers.
type covered struct {
	x, y  int
	cover float64
}

// maxCachedShapes bounds the stroke cache, crudely: progress bars add one shape per width.
const maxCachedShapes = 256

// strokes keeps the coverage of the icons' strokes and rounded bars: the same few shapes
// on every screen, and the slowest thing the ST20 draws (a Bluetooth logo took ~80 ms).
//
//nolint:gochecknoglobals // one process-wide cache shared by every frame, guarded by its mutex
var strokes struct {
	sync.Mutex

	m map[string][]covered
}

// samples counts the pixel's samples inside the shape, of superSamples squared.
func samples(pixX, pixY int, inside func(x, y float64) bool) int {
	count := 0

	for subY := range superSamples {
		for subX := range superSamples {
			sampleX := float64(pixX) + (float64(subX)+half)/superSamples
			if inside(sampleX, float64(pixY)+(float64(subY)+half)/superSamples) {
				count++
			}
		}
	}

	return count
}

func coverage(area box, class func(cx, cy float64) int, inside func(x, y float64) bool) []covered {
	var mask []covered

	for pixY := int(math.Floor(area.minY)); pixY < int(math.Ceil(area.maxY)); pixY++ {
		for pixX := int(math.Floor(area.minX)); pixX < int(math.Ceil(area.maxX)); pixX++ {
			if class != nil {
				kind := class(float64(pixX)+half, float64(pixY)+half)
				if kind == pxOut {
					continue
				}

				if kind == pxIn {
					mask = append(mask, covered{pixX, pixY, 1})

					continue
				}
			}

			if count := samples(pixX, pixY, inside); count > 0 {
				mask = append(mask, covered{pixX, pixY, float64(count) / (superSamples * superSamples)})
			}
		}
	}

	return mask
}

// convex settles a pixel of a convex shape: one whose corner samples are all inside is
// wholly inside.
func convex(inside func(x, y float64) bool) func(cx, cy float64) int {
	return func(cx, cy float64) int {
		if inside(cx-sampleInset, cy-sampleInset) && inside(cx+sampleInset, cy-sampleInset) &&
			inside(cx-sampleInset, cy+sampleInset) && inside(cx+sampleInset, cy+sampleInset) {
			return pxIn
		}

		return pxSample
	}
}

// byDistance settles a pixel by its centre's distance dist to a stroke's middle: further
// than half the width plus a pixel's reach, no sample can be inside; nearer than half the
// width less that reach (only when solid, i.e. no cut caps or arc ends), all are.
func byDistance(dist func(cx, cy float64) float64, halfWidth float64, solid bool) func(cx, cy float64) int {
	return func(cx, cy float64) int {
		d := dist(cx, cy)
		switch {
		case d > halfWidth+sampleReach:
			return pxOut
		case solid && d < halfWidth-sampleReach:
			return pxIn
		default:
			return pxSample
		}
	}
}

func bounds(pts []Pt, pad float64) box {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, pt := range pts {
		minX, minY = math.Min(minX, pt.X), math.Min(minY, pt.Y)
		maxX, maxY = math.Max(maxX, pt.X), math.Max(maxY, pt.Y)
	}

	return box{minX: minX - pad, minY: minY - pad, maxX: maxX + pad + 1, maxY: maxY + pad + 1}
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
	// thai holds the other forms of Thai marks by {tall consonant, upper vowel, mark}
	// (see thaiForm); a mark without one here is drawn as glyphs has it.
	thai map[[3]rune][]glyph
}

// fontAtlas is the embedded glyph atlas and its faces, decoded on first use.
//
//nolint:gochecknoglobals // the embedded atlas, decoded once and shared by every frame
var fontAtlas struct {
	once  sync.Once
	img   *image.Gray
	faces map[string]face
}

func loadAtlas() {
	img, err := png.Decode(bytes.NewReader(atlasPNG))
	if err != nil {
		panic("st20 atlas: " + err.Error())
	}

	gray, ok := img.(*image.Gray)
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

	fontAtlas.img, fontAtlas.faces = gray, map[string]face{}

	parseMetrics(txt, fontAtlas.faces)
}

// metricFields is the most fields a metrics line has (a Thai mark form).
const metricFields = 11

// parseMetrics reads the atlas metrics into faces: ~91,000 lines, which strings.Fields and
// strconv took 1.7 s to parse on the ST20, so it splits and converts the bytes itself.
func parseMetrics(txt []byte, faces map[string]face) {
	var cur face

	kerns, upems := map[string]map[[2]rune]int{}, map[string]float64{}

	var (
		kern map[[2]rune]int
		cols [metricFields][]byte
	)

	for len(txt) > 0 {
		line := txt
		if i := bytes.IndexByte(txt, '\n'); i >= 0 {
			line, txt = txt[:i], txt[i+1:]
		} else {
			txt = nil
		}

		count := fields(line, &cols)
		switch {
		case count == 4 && cols[0][0] == 'w': // w <font file> <weight> <units per em>
			kern = map[[2]rune]int{}
			kerns[string(cols[1])+string(cols[2])] = kern
			upems[string(cols[1])+string(cols[2])] = number(cols[3])
		case count == 4 && cols[0][0] == 'k': // k <left> <right> <font units>
			kern[[2]rune{hex(cols[1]), hex(cols[2])}] = int(number(cols[3]))
		case count == 6 && cols[0][0] == 'f': // f <name> <phases> <font file> <weight> <pixel size>
			cur = face{
				phases: max(1, int(number(cols[2]))),
				glyphs: map[rune][]glyph{},
				kern:   kerns[string(cols[3])+string(cols[4])],
				scale:  0,
				thai:   map[[3]rune][]glyph{},
			}
			if u := upems[string(cols[3])+string(cols[4])]; u > 0 {
				cur.scale = number(cols[5]) / u
			}

			faces[string(cols[1])] = cur
		case count == 10 && cols[0][0] == 'g': // g <rune> <phase> <x> <y> <w> <h> <left> <top> <advance>
			r := hex(cols[1])
			cur.glyphs[r] = append(cur.glyphs[r], glyph{
				int(number(cols[3])), int(number(cols[4])), int(number(cols[5])),
				int(number(cols[6])), int(number(cols[7])), int(number(cols[8])), number(cols[9]),
			})
		case count == 11 && cols[0][0] == 'v': // v <tall> <upper vowel> <mark> <phase> <x> <y> <w> <h> <left> <top>
			key := [3]rune{hex(cols[1]), hex(cols[2]), hex(cols[3])}
			cur.thai[key] = append(cur.thai[key], glyph{
				int(number(cols[5])), int(number(cols[6])), int(number(cols[7])),
				int(number(cols[8])), int(number(cols[9])), int(number(cols[10])), 0,
			})
		default: // a line of no kind above: nothing to read
		}
	}
}

// fields splits line at spaces into cols, returning how many it found (at most len(cols)).
func fields(line []byte, cols *[metricFields][]byte) int {
	count := 0
	for len(line) > 0 && count < len(cols) {
		end := bytes.IndexByte(line, ' ')
		if end < 0 {
			end = len(line)
		}

		if end > 0 {
			cols[count] = line[:end]
			count++
		}

		line = line[min(end+1, len(line)):]
	}

	return count
}

const (
	// decimalBase is the base of the metrics' decimal numbers.
	decimalBase = 10
	// hexDigitBits is how many bits one hexadecimal digit holds.
	hexDigitBits = 4
	// hexLetterBase is the value of the hexadecimal digit 'a'.
	hexLetterBase = 10
)

// number reads a decimal such as -12 or 4.5625. Dividing the digits by a power of ten
// rounds once, as strconv.ParseFloat does, so the result is the same.
func number(digits []byte) float64 {
	neg := len(digits) > 0 && digits[0] == '-'
	if neg {
		digits = digits[1:]
	}

	value, scale := 0.0, 1.0
	frac := false

	for _, c := range digits {
		switch {
		case c == '.':
			frac = true
		case c >= '0' && c <= '9':
			value = value*decimalBase + float64(c-'0')

			if frac {
				scale *= decimalBase
			}
		default: // no digit: skipped
		}
	}

	if neg {
		value = -value
	}

	return value / scale
}

func hex(digits []byte) rune {
	var value rune

	for _, c := range digits {
		switch {
		case c >= '0' && c <= '9':
			value = value<<hexDigitBits | rune(c-'0')
		case c >= 'a' && c <= 'f':
			value = value<<hexDigitBits | rune(c-'a'+hexLetterBase)
		default: // no hexadecimal digit: skipped
		}
	}

	return value
}

func faceOf(name string) face {
	fontAtlas.once.Do(loadAtlas)

	return fontAtlas.faces[name]
}

// LoadFonts decodes the font atlas now rather than at the first text drawn. On the ST20 that
// takes seconds, and the first text of a power-on is the mark's "SoundTap": decoded there, it
// stalled the boot screen past the whole name (2026-11-28). The boot screen calls it in the
// background while the bar runs.
func LoadFonts() {
	fontAtlas.once.Do(loadAtlas)
}
