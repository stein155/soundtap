// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oled

import (
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
var ExportGPIO func(n int) error

// gpioLine is one sysfs GPIO as the SoC names it: bank and line, gpio2_24 = {2, 24}.
type gpioLine struct{ bank, line int }

// spiPins are the panel's chip enable, reset and data/command lines.
type spiPins struct{ ce, reset, dc gpioLine }

// st20Pins are the ST20's, from stock spotty_scm's ssdspi node; a path that names none
// gets these.
var st20Pins = spiPins{ce: gpioLine{2, 24}, reset: gpioLine{2, 18}, dc: gpioLine{1, 27}}

// parseSPIPins reads the panel's GPIOs from a spidev path. Another board names all three
// after a '?': "spidev:/dev/spidev1.0?ce=gpio0_12&reset=gpio2_9&dc=gpio2_12".
// PanelLines are the panel's chip enable, reset and data/command lines for a spidev path,
// each as bank and line: what soundtap-root must let soundtapd export (internal/rootd).
func PanelLines(path string) ([][2]int, error) {
	p, err := parseSPIPins(path)
	if err != nil {
		return nil, err
	}
	return [][2]int{{p.ce.bank, p.ce.line}, {p.reset.bank, p.reset.line}, {p.dc.bank, p.dc.line}}, nil
}

func parseSPIPins(path string) (spiPins, error) {
	_, query, found := strings.Cut(path, "?")
	if !found {
		return st20Pins, nil
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return spiPins{}, err
	}
	var pins spiPins
	for name, line := range map[string]*gpioLine{"ce": &pins.ce, "reset": &pins.reset, "dc": &pins.dc} {
		var bank, n int
		if _, err := fmt.Sscanf(values.Get(name), "gpio%d_%d", &bank, &n); err != nil || bank < 0 || bank > 3 || n < 0 || n > 31 {
			return spiPins{}, fmt.Errorf("oled: %s=%q is not a gpioB_N line", name, values.Get(name))
		}
		*line = gpioLine{bank, n}
	}
	return pins, nil
}

// packFrame converts an 8-bit grayscale frame to the panel's wire format: two
// horizontally-adjacent pixels per byte, each as the high nibble of its level.
// The even pixel lands in the low nibble, the odd pixel in the high nibble.
func packFrame(pix []byte) []byte {
	wire := make([]byte, len(pix)/2)
	for i := range wire {
		wire[i] = pix[2*i]>>4 | pix[2*i+1]&0xf0
	}
	return wire
}
