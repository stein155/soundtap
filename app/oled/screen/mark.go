// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import "math"

// The SoundTap mark (docs/assets/logo.svg): an open note head, its stem and the grille
// holes the sound leaves by, in the logo's 256-unit square.
//
//nolint:gochecknoglobals // a constant table
var markHoles = [...][3]float64{
	{152, 76, 15}, {168, 100, 15}, {176, 128, 15}, // the near row and its grey
	{172, 48, 9}, {196, 76, 9}, {210, 108, 9}, {214, 142, 9}, // the far row
}

// The mark's parts in logo units.
const (
	markStemX   = 124
	markStemTop = 60
	markStemBot = 176
	markHeadX   = 96
	markHeadY   = 176
	markHeadR   = 28
	nearHoles   = 3
)

// ease is the design's ease-in-out: quadratic in, quadratic out, clamped to 0..1.
//
//nolint:mnd // the curve's constants
func ease(along float64) float64 {
	switch {
	case along < 0:
		return 0
	case along > 1:
		return 1
	case along < half:
		return 2 * along * along
	default:
		rest := 2 - 2*along

		return 1 - rest*rest/2
	}
}

// clamp01 is x clamped to 0..1.
func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }

// The boot bar: an outline at grey 6 that soundtapd fills, centred on the panel.
const (
	bootX, bootY, bootW, bootH = 32.0, 48.0, 64.0, 5.0
	bootOutline                = 6
)

// Boot is the power-on screen at progress 0..1: an empty rounded bar outline that fills.
// U-Boot paints Boot(0) the moment the speaker gets power (ADR 0131).
//
//nolint:mnd // the design's coordinates and sizes (design-screens.js boot())
func Boot(progress float64) *Frame {
	frame := newFrame()
	frame.StrokeRRect(bootX+half, bootY+half, bootW-1, bootH-1, 2, 1, bootOutline)

	if progress > 0 {
		frame.RRect(bootX+1, bootY+1, math.Max(3, (bootW-2)*math.Min(1, progress)), bootH-2, 1.5, maxLevel)
	}

	return frame
}

// BootMarkFor is how long BootMark runs: after it the panel has faded out.
const BootMarkFor = 4.0

// bootFadeFrom is when the mark starts to fade out, in seconds after the full bar.
const bootFadeFrom = 3.6

// BootMark is the power-on screen at seconds after the bar filled: the bar slides into the
// note's stem, the stem grows, the head draws round, the holes appear near ones first and
// "SoundTap" comes under it. fade is how far the panel is faded out: a whole-screen change
// the panel's contrast makes, so it is not drawn.
//
//nolint:mnd // the design's coordinates, sizes and timing (design-screens.js boot())
func BootMark(seconds float64) (*Frame, float64) {
	frame := newFrame()

	const scale, offX, offY, stroke = 0.25, 32.0, 4.0, 4.0

	unitX := func(x float64) float64 { return offX + x*scale }
	unitY := func(y float64) float64 { return offY + y*scale }
	stemX, stemTop, stemBot := unitX(markStemX), unitY(markStemTop), unitY(markStemBot)

	if seconds < 0.35 {
		k := ease(seconds / 0.35)
		width := bootW + (stroke-bootW)*k
		left := bootX + (stemX-stroke/2-bootX)*k
		frame.RRect(left, bootY+(stemBot-2-bootY)*k, width, bootH-k, 2, maxLevel)

		return frame, 0
	}

	grow := clamp01((seconds - 0.35) / 0.4)
	top := stemBot - (stemBot-stemTop)*ease(grow)
	frame.Line([]Pt{{stemX, stemBot}, {stemX, top}}, stroke, RoundCap, maxLevel)

	if head := clamp01((seconds - 0.75) / 0.35); head > 0 {
		frame.Arc(unitX(markHeadX), unitY(markHeadY), markHeadR*scale, 0, head*fullTurn, stroke, RoundCap, maxLevel)
	}

	for i, hole := range markHoles {
		if shown := clamp01((seconds - 1.1 - float64(i)*0.12) / 0.12); shown > 0 {
			frame.Disk(unitX(hole[0]), unitY(hole[1]), 2.2, int(jsRound(hole[2]*shown)))
		}
	}

	if name := clamp01((seconds - 2.0) / 0.4); name > 0 {
		font := Font{Face: montserrat(titleWeight, 14), Level: int(jsRound(maxLevel * name)), Align: Center, Max: 0}
		frame.Text("SoundTap", centreX, 90, font)
	}

	return frame, clamp01((seconds - bootFadeFrom) / (BootMarkFor - bootFadeFrom))
}

// Timing of the network card screen, in seconds.
const (
	netcardBeat   = 0.8 // the mark hops once a beat
	netcardRipple = 1.6 // the holes ripple once a loop
	netcardWord   = 2.4 // a greeting stays this long
	netcardFadeIn = 0.1 // of a greeting's time, fading in and again out
)

// Netcard is the ST20's network card being written, seconds into it: the small mark hops on
// a beat while its holes ripple outward, a greeting and "almost done" in every language in
// turn (Hello), and a thin bar at the bottom at percent. It has no words from the panel's
// tables: the speaker's language is often not known yet.
//
//nolint:mnd // the design's coordinates, sizes and timing (design-screens.js netcard())
func Netcard(seconds float64, percent int) *Frame {
	frame := newFrame()

	beat := math.Mod(seconds, netcardBeat) / netcardBeat
	hop := jsRound(math.Max(0, math.Sin(beat*fullTurn)) * 2)

	const scale = 0.16

	offX, offY := centreX-140*scale, 2-hop
	unitX := func(x float64) float64 { return offX + x*scale }
	unitY := func(y float64) float64 { return offY + y*scale }

	stem := []Pt{{unitX(markStemX), unitY(markStemBot)}, {unitX(markStemX), unitY(markStemTop)}}
	frame.Line(stem, 3, RoundCap, maxLevel)
	frame.Arc(unitX(markHeadX), unitY(markHeadY), markHeadR*scale, 0, fullTurn, 3, RoundCap, maxLevel)

	phase := math.Mod(seconds, netcardRipple) / netcardRipple

	for index, hole := range markHoles {
		row := 0.0
		if index >= nearHoles {
			row = 1
		}

		d := phase - row*0.25 - float64(index%nearHoles)*0.05
		glow := math.Max(0, 1-math.Abs(d-0.2)*4)
		frame.Disk(unitX(hole[0]), unitY(hole[1]), 1.8, int(jsRound(4+(hole[2]-4)*glow)))
	}

	words := Hello[int(seconds/netcardWord)%len(Hello)]
	into := math.Mod(seconds, netcardWord) / netcardWord

	shown := 1.0
	if into < netcardFadeIn {
		shown = into / netcardFadeIn
	} else if into > 1-netcardFadeIn {
		shown = (1 - into) / netcardFadeIn
	}

	greeting, done := title(words[0], 20, 18), small(words[1], 1)
	greeting.maxLines, greeting.level = 1, int(jsRound(maxLevel*shown))
	done.level = int(jsRound(smallLevel * shown))
	// Between the mark's foot (row 36 at rest) and the bar (row 96): the greeting's capitals
	// sit as far under the mark as the line under it sits above the bar.
	frame.flow([]item{greeting, done}, 36, 95)

	percent = max(0, min(100, percent))

	frame.Rect(14, 96, 100, 2, 4)
	frame.Rect(14, 96, float64(percent), 2, maxLevel)

	return frame
}
