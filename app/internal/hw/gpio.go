// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gpioRoot is the sysfs GPIO tree; a variable so tests can point at a fake one.
//
//nolint:gochecknoglobals // tests point it at a fake sysfs tree
var gpioRoot = "/sys/class/gpio"

// gpioLine is one exported GPIO. An interface so the scan logic can be tested against
// a simulated key matrix instead of real hardware.
type gpioLine interface {
	num() int
	read() (int, error)
	write(v int) error
	direction(value string) error
	Close() error
}

// sysfsGPIO holds /sys/class/gpio/gpioN/value and direction open. Both are read and
// written at offset 0, so a sweep costs a few syscalls rather than a few opens. The ST20
// releases its strobes by direction, and reopening that file twice a tick was a fifth of
// soundtapd's CPU with the speaker in standby.
type sysfsGPIO struct {
	n   int
	f   *os.File
	dir *os.File
	buf [8]byte
}

// errEmptyRead is a value attribute that read as nothing.
var errEmptyRead = errors.New("empty read")

// errNoGPIOChip is a GPIO bank that sysfs does not show.
var errNoGPIOChip = errors.New("no gpiochip")

const (
	// gpioWriteOnly is the mode of a sysfs attribute only written, should the write
	// create it: export and direction.
	gpioWriteOnly = 0o200
	// gpioAttrRetries and gpioAttrRetry bound the wait for udev to create a new line's
	// attributes.
	gpioAttrRetries = 20
	gpioAttrRetry   = 10 * time.Millisecond
)

func (g *sysfsGPIO) Close() error {
	_ = g.dir.Close()

	err := g.f.Close()
	if err != nil {
		return fmt.Errorf("gpio%d value: %w", g.n, err)
	}

	return nil
}

func (g *sysfsGPIO) num() int { return g.n }

func (g *sysfsGPIO) read() (int, error) {
	// ReadAt reports io.EOF when it reads fewer bytes than the buffer holds, which is
	// the normal case for a two-byte sysfs attribute. Only a read of nothing is a
	// failure.
	n, err := g.f.ReadAt(g.buf[:], 0)
	if n == 0 {
		if err == nil {
			err = errEmptyRead
		}

		return 0, fmt.Errorf("gpio%d value: %w", g.n, err)
	}

	if g.buf[0] == '0' {
		return 0, nil
	}

	return 1, nil
}

func (g *sysfsGPIO) write(v int) error {
	b := []byte{'0'}
	if v != 0 {
		b[0] = '1'
	}

	_, err := g.f.WriteAt(b, 0)
	if err != nil {
		return fmt.Errorf("gpio%d value: %w", g.n, err)
	}

	return nil
}

func (g *sysfsGPIO) direction(v string) error {
	_, err := g.dir.WriteAt([]byte(v), 0)
	if err != nil {
		return fmt.Errorf("gpio%d direction: %w", g.n, err)
	}

	return nil
}

// gpioBankBase resolves the sysfs numbering base of the GPIO bank at the given physical
// address.
func gpioBankBase(addr string) (int, error) {
	chips, _ := filepath.Glob(filepath.Join(gpioRoot, "gpiochip*"))
	for _, chip := range chips {
		target, err := filepath.EvalSymlinks(chip)
		if err != nil {
			continue
		}

		if !strings.Contains(target, addr) {
			continue
		}

		base, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(chip), "gpiochip"))
		if err != nil {
			continue
		}

		return base, nil
	}

	return 0, fmt.Errorf("%w at %s", errNoGPIOChip, addr)
}

// ExportGPIO, when set, exports sysfs GPIO n and makes its attributes this process's:
// soundtapd sets it to soundtap-root's gpio.export. nil exports here, as root does.
//
//nolint:gochecknoglobals // soundtapd replaces it at startup with soundtap-root's route (ADR 0066)
var ExportGPIO func(n int) error

// exportGPIO exports sysfs GPIO number unless it already is, and returns its directory.
// The attributes inside may appear a moment later (udev); callers wait or retry.
func exportGPIO(number int) (string, error) {
	dir := filepath.Join(gpioRoot, fmt.Sprintf("gpio%d", number))
	if ExportGPIO != nil {
		// soundtapd's user may not export a line, nor write the attributes of one it did:
		// soundtap-root does both, for the lines soundtapd drives (ADR 0066). Also for a
		// line init exported, whose attributes are root's.
		err := ExportGPIO(number)
		if err != nil {
			return "", fmt.Errorf("export gpio%d: %w", number, err)
		}

		return dir, nil
	}

	_, err := os.Stat(dir)
	if err == nil {
		return dir, nil
	}

	err = os.WriteFile(filepath.Join(gpioRoot, "export"), []byte(strconv.Itoa(number)), gpioWriteOnly)
	if err != nil {
		return "", fmt.Errorf("export gpio%d: %w", number, err)
	}

	return dir, nil
}

// openGPIOLine exports a GPIO if it isn't already and opens its value attribute.
// direction is passed to sysfs verbatim: "in", or "low"/"high" to set an output and its
// initial level in one write, so a strobe never glitches high on the way up.
func openGPIOLine(number int, direction string) (*sysfsGPIO, error) {
	dir, err := exportGPIO(number)
	if err != nil {
		return nil, err
	}
	// udev creates the attributes a moment after the directory appears, and on this
	// box that moment is occasionally long enough to lose the first write.
	for range gpioAttrRetries {
		err = os.WriteFile(filepath.Join(dir, "direction"), []byte(direction), gpioWriteOnly)
		if err == nil {
			break
		}

		time.Sleep(gpioAttrRetry)
	}

	if err != nil {
		return nil, fmt.Errorf("gpio%d direction: %w", number, err)
	}

	valueFile, err := os.OpenFile(filepath.Join(dir, "value"), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("gpio%d value: %w", number, err)
	}

	dirFile, err := os.OpenFile(filepath.Join(dir, "direction"), os.O_WRONLY, 0)
	if err != nil {
		_ = valueFile.Close()

		return nil, fmt.Errorf("gpio%d direction: %w", number, err)
	}

	return &sysfsGPIO{n: number, f: valueFile, dir: dirFile, buf: [8]byte{}}, nil
}

// Line is one GPIO line for a driver outside this package (internal/hw/battery).
type Line struct{ l gpioLine }

// BankLine opens line offset of the GPIO bank at the physical address addr ("44e07000" is
// gpio0), exporting it first; direction is as openGPIOLine takes it.
func BankLine(addr string, offset int, direction string) (*Line, error) {
	base, err := gpioBankBase(addr)
	if err != nil {
		return nil, err
	}

	l, err := openGPIOLine(base+offset, direction)
	if err != nil {
		return nil, err
	}

	return &Line{l: l}, nil
}

// Read is the line's level.
func (l *Line) Read() (int, error) { return l.l.read() }

// Write sets an output line's level.
func (l *Line) Write(v int) error { return l.l.write(v) }

// Close releases the line's attribute files.
func (l *Line) Close() error {
	err := l.l.Close()
	if err != nil {
		return fmt.Errorf("close gpio%d: %w", l.l.num(), err)
	}

	return nil
}
