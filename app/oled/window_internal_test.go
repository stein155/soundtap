// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oled

import (
	"bytes"
	"testing"
)

// A changed pixel at an odd x widens the window to its pair: the panel takes two pixels a
// byte, and the window's columns count pairs.
func TestChangedWindowOddEdges(t *testing.T) {
	t.Parallel()

	last := make([]byte, Width*Height)

	pix := append([]byte(nil), last...)
	pix[10*Width+5] = 0xff  // x 5: column 2
	pix[12*Width+10] = 0x10 // x 10: column 5
	pix[11*Width+7] = 0x20  // x 7: column 3

	win, changed := changedWindow(last, pix)
	if !changed || win != (window{col0: 2, col1: 5, row0: 10, row1: 12}) {
		t.Fatalf("window = %+v, %v; want columns 2-5, rows 10-12", win, changed)
	}

	if _, changed := changedWindow(pix, pix); changed {
		t.Fatal("an unchanged frame has a window")
	}

	if win, changed := changedWindow(nil, pix); !changed || win != fullWindow() {
		t.Fatalf("nothing drawn yet: window = %+v, want the whole panel", win)
	}

	edge := append([]byte(nil), last...)
	edge[Width*Height-1] = 1
	edge[0] = 1

	if win, _ := changedWindow(last, edge); win != fullWindow() {
		t.Fatalf("corners: window = %+v, want the whole panel", win)
	}

	odd := append([]byte(nil), last...)
	odd[50*Width+127] = 1 // the last pixel of a row: the last column

	if win, _ := changedWindow(last, odd); win != (window{col0: 63, col1: 63, row0: 50, row1: 50}) {
		t.Fatalf("x 127: window = %+v, want column 63 alone", win)
	}
}

// packWindow sends a window's pairs row by row, even pixel in the low nibble, and matches
// the full frame's bytes where the two overlap.
func TestPackWindow(t *testing.T) {
	t.Parallel()

	pix := make([]byte, Width*Height)
	for i := range pix {
		pix[i] = byte(i * 7)
	}

	win := window{col0: 3, col1: 4, row0: 20, row1: 21}
	got := packWindow(pix, win)
	full := packFrame(pix)

	var want []byte
	for row := 20; row <= 21; row++ {
		want = append(want, full[row*Width/2+3], full[row*Width/2+4])
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("window bytes %x, want %x", got, want)
	}

	at := 20*Width + 6 // pixels 6 and 7 of row 20: column 3
	if got[0] != pix[at]>>4|pix[at+1]&0xf0 {
		t.Fatalf("first byte %x does not hold pixels 6 and 7 of row 20", got[0])
	}
}

// The first frame sends everything and switches the display on; a later one only the
// window that changed; a fade only the contrast; an unchanged frame nothing.
func TestDrawSteps(t *testing.T) {
	t.Parallel()

	state := panelState{latest: nil, contrast: FullContrast, on: false}
	frame := make([]byte, Width*Height)

	steps := drawSteps(&state, frame, FullContrast)
	if len(steps) != 3 || steps[0].data || !steps[1].data || len(steps[1].buf) != Width*Height/2 ||
		!bytes.Equal(steps[2].buf, []byte{cmdOn}) {
		t.Fatalf("first frame: %d steps", len(steps))
	}

	if !bytes.Equal(steps[0].buf, []byte{cmdColumns, 0, 63, cmdRows, 0, 99}) {
		t.Fatalf("first frame window %x", steps[0].buf)
	}

	state.drawn(frame, FullContrast)

	if again := drawSteps(&state, frame, FullContrast); len(again) != 0 {
		t.Fatalf("unchanged frame: %d steps", len(again))
	}

	dots := append([]byte(nil), frame...)
	dots[95*Width+61], dots[96*Width+66] = 0xff, 0xff

	steps = drawSteps(&state, dots, FullContrast)
	if len(steps) != 2 || !bytes.Equal(steps[0].buf, []byte{cmdColumns, 30, 33, cmdRows, 95, 96}) ||
		len(steps[1].buf) != 8 {
		t.Fatalf("dots: %+v", steps)
	}

	state.drawn(dots, FullContrast)

	steps = drawSteps(&state, dots, 40)
	if len(steps) != 1 || !bytes.Equal(steps[0].buf, []byte{cmdContrast, 40}) {
		t.Fatalf("fade: %+v", steps)
	}
}

func TestContrastFor(t *testing.T) {
	t.Parallel()

	if contrastFor(0) != FullContrast || contrastFor(1) != 0 || contrastFor(-1) != FullContrast ||
		contrastFor(0.5) != 67 {
		t.Fatalf("contrast 0, 1, -1, 0.5 = %d %d %d %d",
			contrastFor(0), contrastFor(1), contrastFor(-1), contrastFor(0.5))
	}
}

// A framebuffer without a contrast command fades the frame itself.
func TestFadeFrame(t *testing.T) {
	t.Parallel()

	pix := []byte{0, 100, 255}
	if got := fadeFrame(pix, 0); !bytes.Equal(got, pix) {
		t.Fatalf("no fade: %v", got)
	}

	if got := fadeFrame(pix, 0.5); !bytes.Equal(got, []byte{0, 50, 128}) {
		t.Fatalf("half: %v", got)
	}
}

// Resend makes an unchanged frame go out whole, with display-on, as the minute repaint needs
// to heal a controller that lost its RAM.
func TestResendSendsTheWholeFrameAgain(t *testing.T) {
	t.Parallel()

	buf := NewFramebuffer("spidev:/dev/null")
	buf.spi = new(spiOLED)
	buf.spi.panelState = panelState{latest: nil, contrast: FullContrast, on: false}
	frame := make([]byte, Width*Height)
	buf.spi.drawn(frame, FullContrast)

	if steps := drawSteps(&buf.spi.panelState, frame, FullContrast); len(steps) != 0 {
		t.Fatalf("unchanged frame before Resend: %d steps", len(steps))
	}

	buf.Resend()

	steps := drawSteps(&buf.spi.panelState, frame, FullContrast)
	if len(steps) != 3 || len(steps[1].buf) != Width*Height/2 || !bytes.Equal(steps[2].buf, []byte{cmdOn}) {
		t.Fatalf("after Resend: %+v", steps)
	}
}
