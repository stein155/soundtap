// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oled

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// spiPrefix marks a panel path that is driven over spidev rather than through a
// framebuffer device: "spidev:/dev/spidev1.0".
const spiPrefix = "spidev:"

// parseSPIPath splits a spidev: panel path into the device path it names. The
// second result is false for a framebuffer path or a prefix with nothing after it.
func parseSPIPath(path string) (string, bool) {
	if !strings.HasPrefix(path, spiPrefix) {
		return "", false
	}

	path, _, _ = strings.Cut(strings.TrimPrefix(path, spiPrefix), "?")

	return path, path != ""
}

// ExportGPIO, when set, exports a panel line and makes its attributes this process's:
// soundtapd sets it to soundtap-root's gpio.export (ADR 0066). nil exports through sysfs
// directly, as root does.
//
//nolint:gochecknoglobals // soundtapd's start-up sets it (cmd/soundtapd/roothelper.go), as internal/hw's
var ExportGPIO func(n int) error

// The AM335x's GPIO controllers: four banks of 32 lines.
const (
	gpioBankCount    = 4
	gpioLinesPerBank = 32
)

// errNotGPIOLine is a panel path naming a pin that is not a gpioB_N line.
var errNotGPIOLine = errors.New("is not a gpioB_N line")

// gpioLine is one sysfs GPIO as the SoC names it: bank and line, gpio2_24 = {2, 24}.
type gpioLine struct{ bank, line int }

// spiPins are the panel's chip enable, reset and data/command lines.
type spiPins struct{ ce, reset, dc gpioLine }

// st20Pins are the ST20's, from stock spotty_scm's ssdspi node; a path that names none
// gets these.
func st20Pins() spiPins {
	return spiPins{ce: gpioLine{2, 24}, reset: gpioLine{2, 18}, dc: gpioLine{1, 27}}
}

// PanelLines are the panel's chip enable, reset and data/command lines for a spidev path,
// each as bank and line: what soundtap-root must let soundtapd export (internal/rootd).
func PanelLines(path string) ([][2]int, error) {
	p, err := parseSPIPins(path)
	if err != nil {
		return nil, err
	}

	return [][2]int{{p.ce.bank, p.ce.line}, {p.reset.bank, p.reset.line}, {p.dc.bank, p.dc.line}}, nil
}

// parseSPIPins reads the panel's GPIOs from a spidev path. Another board names all three
// after a '?': "spidev:/dev/spidev1.0?ce=gpio0_12&reset=gpio2_9&dc=gpio2_12".
func parseSPIPins(path string) (spiPins, error) {
	_, query, found := strings.Cut(path, "?")
	if !found {
		return st20Pins(), nil
	}

	values, err := url.ParseQuery(query)
	if err != nil {
		return spiPins{}, fmt.Errorf("oled: panel GPIOs: %w", err)
	}

	var pins spiPins
	for name, line := range map[string]*gpioLine{"ce": &pins.ce, "reset": &pins.reset, "dc": &pins.dc} {
		var bank, num int

		_, err = fmt.Sscanf(values.Get(name), "gpio%d_%d", &bank, &num)
		if err != nil || bank < 0 || bank >= gpioBankCount || num < 0 || num >= gpioLinesPerBank {
			return spiPins{}, fmt.Errorf("oled: %s=%q %w", name, values.Get(name), errNotGPIOLine)
		}

		*line = gpioLine{bank, num}
	}

	return pins, nil
}

// The panel's wire format packs two pixels into a byte, a nibble each.
const (
	pixelsPerByte = 2
	nibbleBits    = 4
	highNibble    = 0xf0
)

// The controller's commands soundtapd sends after the init sequence (oledInit), SSD1327 style.
const (
	cmdOff      = 0xae // display off
	cmdColumns  = 0x15 // column window: first, last, in units of two pixels
	cmdRows     = 0x75 // row window: first, last
	cmdContrast = 0x81 // contrast: one level byte
	cmdOn       = 0xaf // display on
	// FullContrast is the contrast the init sequence sets: the panel at full brightness.
	FullContrast = 0x85
)

// packFrame converts an 8-bit grayscale frame to the panel's wire format: two
// horizontally-adjacent pixels per byte, each as the high nibble of its level.
// The even pixel lands in the low nibble, the odd pixel in the high nibble.
func packFrame(pix []byte) []byte {
	wire := make([]byte, len(pix)/pixelsPerByte)
	for i := range wire {
		wire[i] = pix[pixelsPerByte*i]>>nibbleBits | pix[pixelsPerByte*i+1]&highNibble
	}

	return wire
}

// window is a rectangle of the panel's memory: columns in units of two pixels (column c
// holds pixels 2c and 2c+1), rows in pixels, both ends included. A window starts at an
// even pixel and ends at an odd one, because a byte holds a pair.
type window struct{ col0, col1, row0, row1 int }

// fullWindow is the whole panel.
func fullWindow() window {
	return window{col0: 0, col1: Width/pixelsPerByte - 1, row0: 0, row1: Height - 1}
}

// changedWindow is the smallest window that holds every pixel where pix differs from
// last; false when nothing differs. A last of another size (nothing drawn yet) differs
// everywhere.
func changedWindow(last, pix []byte) (window, bool) {
	if len(last) != len(pix) {
		return fullWindow(), true
	}

	minX, minY, maxX, maxY := Width, Height, -1, -1

	for i := range pix {
		if pix[i] == last[i] {
			continue
		}

		x, y := i%Width, i/Width
		minX, maxX = min(minX, x), max(maxX, x)
		minY, maxY = min(minY, y), max(maxY, y)
	}

	if maxX < 0 {
		return window{col0: 0, col1: 0, row0: 0, row1: 0}, false
	}

	return window{col0: minX / pixelsPerByte, col1: maxX / pixelsPerByte, row0: minY, row1: maxY}, true
}

// packWindow is the part of pix inside win in the wire format, row by row, as the
// controller fills a window.
func packWindow(pix []byte, win window) []byte {
	cols := win.col1 - win.col0 + 1
	wire := make([]byte, 0, cols*(win.row1-win.row0+1))

	for row := win.row0; row <= win.row1; row++ {
		for col := win.col0; col <= win.col1; col++ {
			at := row*Width + pixelsPerByte*col
			wire = append(wire, pix[at]>>nibbleBits|pix[at+1]&highNibble)
		}
	}

	return wire
}

// wireStep is one SPI transfer: command bytes with D/C low, or frame data with it high.
type wireStep struct {
	data bool
	buf  []byte
}

// panelState is what the panel shows: the last frame, its contrast, and whether the
// display was switched on.
type panelState struct {
	latest   []byte
	contrast int
	on       bool
}

// drawSteps are the transfers that take the panel from st to pix at contrast: the
// contrast command when it changed, and the window of changed pixels with only their
// data. A full frame took ~207 ms on the ST20 (6400 bytes at 2 MHz plus the line
// settling); a station's dots or a progress bar are a few hundred bytes. The display is
// switched on after the first frame, as stock does, and left on.
func drawSteps(state *panelState, pix []byte, contrast int) []wireStep {
	var cmds []byte

	if contrast != state.contrast {
		cmds = append(cmds, cmdContrast, byte(contrast)) //nolint:gosec // a contrast level, 0..255
	}

	win, changed := changedWindow(state.latest, pix)
	if changed {
		cmds = append(cmds, cmdColumns, byte(win.col0), byte(win.col1), //nolint:gosec // panel columns, 0..63
			cmdRows, byte(win.row0), byte(win.row1)) //nolint:gosec // panel rows, 0..99
	}

	var steps []wireStep
	if len(cmds) > 0 {
		steps = append(steps, wireStep{data: false, buf: cmds})
	}

	if changed {
		steps = append(steps, wireStep{data: true, buf: packWindow(pix, win)})
	}

	if !state.on {
		steps = append(steps, wireStep{data: false, buf: []byte{cmdOn}})
	}

	return steps
}

// drawn records that drawSteps' transfers went out.
func (st *panelState) drawn(pix []byte, contrast int) {
	st.latest = append(st.latest[:0], pix...)
	st.contrast, st.on = contrast, true
}

// oledInit is the panel controller's start-up command sequence. It starts with display off;
// the first frame switches it on (drawSteps).
func oledInit() []byte {
	return []byte{
		cmdOff, 0x15, 0x00, 0x3f, 0x75, 0x00, 0x63, cmdContrast,
		FullContrast, 0xa0, 0x53, 0xa2, 0x1c, 0xa1, 0x00, 0xa4,
		0xa8, 0x63, 0xab, 0x01, 0xb1, 0x16, 0xb3, 0x90,
		0xb6, 0x0f, 0xbc, 0x07, 0xbe, 0x00, 0xd5, 0x63,
		0xb8, 0x03, 0x06, 0x08, 0x0b, 0x0e, 0x12, 0x16,
		0x1b, 0x1f, 0x24, 0x29, 0x2e, 0x33, 0x39, 0x3f,
	}
}

// panelStart is how opening the panel starts its controller: whether it pulses the reset
// line, and the commands it sends. A dark panel gets the reset and the whole init. A panel
// that is lit already (lit: its reset line was left driven high, by U-Boot's boot frame or an
// earlier soundtapd) keeps its picture: no reset, which clears the controller's memory and
// switches the display off, and the init without its display-off, all of it the same settings
// U-Boot sent. The first frame then replaces U-Boot's on a panel that never goes dark
// (the ST20 blanked for a moment between U-Boot's bar and soundtapd's, 2026-11-28).
//
//nolint:revive // flag-parameter: the one fact that decides how the panel starts
func panelStart(lit bool) (bool, []byte) {
	if lit {
		return false, oledInit()[1:]
	}

	return true, oledInit()
}

// drivenHigh is whether the sysfs GPIO in dir is an output driven high: "out" in its
// direction and "1" in its value. A line that cannot be read is not.
func drivenHigh(dir string) bool {
	direction, err := os.ReadFile(filepath.Join(dir, "direction"))
	if err != nil || string(bytes.TrimSpace(direction)) != "out" {
		return false
	}

	value, err := os.ReadFile(filepath.Join(dir, "value"))

	return err == nil && string(bytes.TrimSpace(value)) == "1"
}
