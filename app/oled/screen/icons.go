// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import "math"

// Icon is one of the design's pictures that say what a word would.
type Icon int

// The icons, as design-screens.js's icon() draws them.
const (
	IconNone Icon = iota
	IconWifi
	IconWifiOff
	IconCable
	IconBluetooth
	IconBluetoothOff
	IconCheck
	IconCross
	IconWarn
	IconPause
	IconStop
	IconMute
	IconLock
	IconAux
	IconSkip
	IconGroup
)

// iconStroke is the icons' line width; offLevel the grey of an icon struck through.
const (
	iconStroke = 2
	offLevel   = 6
)

// Icon draws kind centred on (middleX, middleY) at level, 2 px strokes with round caps and joins.
//
//nolint:mnd,cyclop,funlen // the design's coordinates (design-screens.js icon()), one case an icon
func (f *Frame) Icon(kind Icon, middleX, middleY float64, level int) {
	line := func(pts ...[2]float64) {
		path := make([]Pt, len(pts))
		for i, p := range pts {
			path[i] = Pt{middleX + p[0], middleY + p[1]}
		}

		f.Line(path, iconStroke, RoundCap, level)
	}

	switch kind { //nolint:exhaustive // IconNone draws nothing: the default
	case IconWifi:
		for _, radius := range []float64{13, 8} {
			f.Arc(middleX, middleY+6, radius, math.Pi*1.25, math.Pi*1.75, iconStroke, RoundCap, level)
		}

		f.Disk(middleX, middleY+5, 2, level)
	case IconWifiOff:
		f.Icon(IconWifi, middleX, middleY, offLevel)
		line([2]float64{-10, -8}, [2]float64{10, 10})
	case IconCable:
		f.StrokeRect(middleX-6.5, middleY-5.5, 13, 9, iconStroke, true, level)
		line([2]float64{-3, 4}, [2]float64{-3, 8})
		line([2]float64{3, 4}, [2]float64{3, 8})
		line([2]float64{0, -6}, [2]float64{0, -11})
	case IconBluetoothOff:
		f.Icon(IconBluetooth, middleX, middleY, offLevel)
		line([2]float64{-9, -9}, [2]float64{9, 9})
	case IconBluetooth:
		line([2]float64{-5, -5}, [2]float64{5, 5}, [2]float64{0, 10}, [2]float64{0, -10},
			[2]float64{5, -5}, [2]float64{-5, 5})
	case IconCheck:
		line([2]float64{-8, 0}, [2]float64{-3, 6}, [2]float64{8, -6})
	case IconCross:
		line([2]float64{-6, -6}, [2]float64{6, 6})
		line([2]float64{6, -6}, [2]float64{-6, 6})
	case IconWarn:
		line([2]float64{0, -10}, [2]float64{10, 8}, [2]float64{-10, 8}, [2]float64{0, -10})
		line([2]float64{0, -3}, [2]float64{0, 2})
		f.Rect(middleX-1, middleY+4, 2, 2, level)
	case IconPause:
		f.Rect(middleX-4, middleY-4, 3, 9, level)
		f.Rect(middleX+1, middleY-4, 3, 9, level)
	case IconStop:
		f.Rect(middleX-4, middleY-4, 8, 8, level)
	case IconMute:
		line([2]float64{-9, -3}, [2]float64{-5, -3}, [2]float64{0, -8}, [2]float64{0, 8},
			[2]float64{-5, 3}, [2]float64{-9, 3}, [2]float64{-9, -3})
		line([2]float64{4, -4}, [2]float64{10, 4})
		line([2]float64{10, -4}, [2]float64{4, 4})
	case IconLock:
		f.StrokeRect(middleX-6.5, middleY-1.5, 13, 10, iconStroke, true, level)
		f.Arc(middleX, middleY-2, 4.5, math.Pi, fullTurn, iconStroke, RoundCap, level)
	case IconAux:
		f.Arc(middleX, middleY, 9, 0, fullTurn, iconStroke, RoundCap, level)
		f.Disk(middleX, middleY, 3, level)
	case IconSkip:
		line([2]float64{-7, -6}, [2]float64{1, 0}, [2]float64{-7, 6}, [2]float64{-7, -6})
		line([2]float64{5, -6}, [2]float64{5, 6})
	case IconGroup:
		f.StrokeRect(middleX-10.5, middleY-4.5, 8, 12, iconStroke, true, level)
		f.StrokeRect(middleX+2.5, middleY-7.5, 8, 15, iconStroke, true, level)
	default: // IconNone: nothing
	}
}
