// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oled

import (
	"errors"
	"fmt"
	"net/url"
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
