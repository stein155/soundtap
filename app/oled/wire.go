// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oled

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// How long the panel's lines settle around a transfer.
//
// The controller (SSD1327 class) samples D/C with each byte's last bit and needs chip enable
// and D/C set up and held for tens of nanoseconds, not milliseconds; U-Boot's boot frame
// (ADR 0131) drives the same panel with nothing between its register writes. A write to
// spidev returns only once its transfer is out, and the panel's chip enable is a GPIO the
// kernel never touches (the McSPI's own chip select is not on a pad), so each line change
// comes after the bytes before it. lineSettle is margin on top of that: 4 us is one byte at
// 2 MHz. legacyLineSettle is what every transfer used to wait (a frame took ~207 ms, so the
// panel managed about 5 frames a second), kept as the fallback after a failed transfer.
const (
	lineSettle       = 200 * time.Microsecond
	legacyLineSettle = 10 * time.Millisecond
)

// spiChunk is the most bytes one write hands spidev (its default buffer size).
const spiChunk = 4096

// The data/command line's level: low for a command, high for frame data.
const (
	dcCommand = gpioLow
	dcData    = gpioHigh
)

// gpioLevel is what a sysfs GPIO's value attribute is set to.
type gpioLevel string

const (
	gpioLow  gpioLevel = "0"
	gpioHigh gpioLevel = "1"
)

var errShortWrite = errors.New("short write")

// panelBus is the panel's wire: its D/C and chip enable lines and the SPI writes.
type panelBus interface {
	setDC(level gpioLevel) error
	setCE(level gpioLevel) error
	write(buf []byte) (int, error)
}

// lineTiming is whether the panel's lines fell back to the legacy settling. The Framebuffer
// keeps it, so the fallback lasts for the run, across a reopen of the panel.
type lineTiming struct{ slow bool }

// wire sends wireSteps over a panelBus.
type wire struct {
	bus    panelBus
	timing *lineTiming
	sleep  func(time.Duration)
	// warn reports the fall back to the legacy settling; it runs once a run.
	warn func(err error)
	// dc is the level the D/C line was last driven to; empty when unknown.
	dc gpioLevel
}

func newWire(bus panelBus, timing *lineTiming) *wire {
	return &wire{
		bus:    bus,
		timing: timing,
		sleep:  time.Sleep,
		warn: func(err error) {
			slog.Warn("oled: SPI transfer failed, the panel's lines settle 10 ms from now on", "err", err)
		},
		dc: "",
	}
}

func stepLevel(step wireStep) gpioLevel {
	if step.data {
		return dcData
	}

	return dcCommand
}

// send runs steps. Fast, they share one chip-enable cycle: D/C is driven only where it
// changes, each change lineSettle after the last byte before it and lineSettle before the next.
// A failed transfer releases chip enable, and from then on every step is sent the legacy
// way, its own D/C and chip-enable cycle with legacyLineSettle around each line change.
func (w *wire) send(steps []wireStep) error {
	if len(steps) == 0 {
		return nil
	}

	var err error
	if w.timing.slow {
		err = w.sendLegacy(steps)
	} else {
		err = w.sendFast(steps)
	}

	if err != nil {
		w.dc = "" // the line may be half way: drive it again next time
		if !w.timing.slow {
			w.timing.slow = true
			w.warn(err)
		}
	}

	return err
}

func (w *wire) sendFast(steps []wireStep) error {
	for index, step := range steps {
		level := stepLevel(step)
		if level != w.dc {
			err := w.bus.setDC(level)
			if err != nil {
				return w.release(fmt.Errorf("dc: %w", err), index > 0)
			}

			w.dc = level
			w.sleep(lineSettle)
		}

		if index == 0 {
			err := w.bus.setCE(gpioLow)
			if err != nil {
				return w.release(fmt.Errorf("ce: %w", err), true)
			}

			w.sleep(lineSettle)
		}

		err := w.writeAll(step.buf)
		if err != nil {
			return w.release(err, true)
		}

		w.sleep(lineSettle)
	}

	err := w.bus.setCE(gpioHigh)
	if err != nil {
		return fmt.Errorf("ce: %w", err)
	}

	w.sleep(lineSettle)

	return nil
}

// sendLegacy is how every transfer went before the fast path: each step its own cycle.
func (w *wire) sendLegacy(steps []wireStep) error {
	for _, step := range steps {
		level := stepLevel(step)

		err := w.bus.setDC(level)
		if err != nil {
			return fmt.Errorf("dc: %w", err)
		}

		w.dc = level
		w.sleep(legacyLineSettle)

		err = w.bus.setCE(gpioLow)
		if err != nil {
			return w.release(fmt.Errorf("ce: %w", err), true)
		}

		w.sleep(legacyLineSettle)

		err = w.writeAll(step.buf)
		if err != nil {
			return w.release(err, true)
		}

		w.sleep(legacyLineSettle)

		err = w.bus.setCE(gpioHigh)
		if err != nil {
			return fmt.Errorf("ce: %w", err)
		}

		w.sleep(legacyLineSettle)
	}

	return nil
}

// release lets chip enable go after err, when it may be asserted, and returns err.
//
//nolint:revive // flag-parameter: whether chip enable may be asserted
func (w *wire) release(err error, asserted bool) error {
	if asserted {
		_ = w.bus.setCE(gpioHigh)
	}

	return err
}

// writeAll hands buf to the bus in writes of at most spiChunk bytes; chip enable stays
// asserted across them.
func (w *wire) writeAll(buf []byte) error {
	for len(buf) > 0 {
		chunk := min(len(buf), spiChunk)

		written, err := w.bus.write(buf[:chunk])
		if err != nil {
			return fmt.Errorf("spi: %w", err)
		}

		if written != chunk {
			return fmt.Errorf("%w: %d of %d", errShortWrite, written, chunk)
		}

		buf = buf[chunk:]
	}

	return nil
}
