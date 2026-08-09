// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gpioRoot is the sysfs GPIO tree; a variable so tests can point at a fake one.
var gpioRoot = "/sys/class/gpio"

// gpioLine is one exported GPIO. An interface so the scan logic can be tested against
// a simulated key matrix instead of real hardware.
type gpioLine interface {
	num() int
	read() (int, error)
	write(v int) error
	direction(string) error
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

func (g *sysfsGPIO) num() int { return g.n }

func (g *sysfsGPIO) read() (int, error) {
	// ReadAt reports io.EOF when it reads fewer bytes than the buffer holds, which is
	// the normal case for a two-byte sysfs attribute. Only a read of nothing is a
	// failure.
	n, err := g.f.ReadAt(g.buf[:], 0)
	if n == 0 {
		if err == nil {
			err = fmt.Errorf("empty read")
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
	if _, err := g.f.WriteAt(b, 0); err != nil {
		return fmt.Errorf("gpio%d value: %w", g.n, err)
	}
	return nil
}

func (g *sysfsGPIO) direction(v string) error {
	if _, err := g.dir.WriteAt([]byte(v), 0); err != nil {
		return fmt.Errorf("gpio%d direction: %w", g.n, err)
	}
	return nil
}

func (g *sysfsGPIO) Close() error {
	_ = g.dir.Close()
	return g.f.Close()
}

// gpioBankBase resolves the sysfs numbering base of the GPIO bank at the given physical
// address.
func gpioBankBase(addr string) (int, error) {
	chips, _ := filepath.Glob(filepath.Join(gpioRoot, "gpiochip*"))
	for _, c := range chips {
		target, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		if !strings.Contains(target, addr) {
			continue
		}
		base, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(c), "gpiochip"))
		if err != nil {
			continue
		}
		return base, nil
	}
	return 0, fmt.Errorf("no gpiochip at %s", addr)
}

// ExportGPIO, when set, exports sysfs GPIO n and makes its attributes this process's:
// soundtapd sets it to soundtap-root's gpio.export. nil exports here, as root does.
var ExportGPIO func(n int) error

// exportGPIO exports sysfs GPIO n unless it already is, and returns its directory. The
// attributes inside may appear a moment later (udev); callers wait or retry.
func exportGPIO(n int) (string, error) {
	dir := filepath.Join(gpioRoot, fmt.Sprintf("gpio%d", n))
	if ExportGPIO != nil {
		// soundtapd's user may not export a line, nor write the attributes of one it did:
		// soundtap-root does both, for the lines soundtapd drives (ADR 0066). Also for a
		// line init exported, whose attributes are root's.
		if err := ExportGPIO(n); err != nil {
			return "", fmt.Errorf("export gpio%d: %w", n, err)
		}
		return dir, nil
	}
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	if err := os.WriteFile(filepath.Join(gpioRoot, "export"), []byte(strconv.Itoa(n)), 0o200); err != nil {
		return "", fmt.Errorf("export gpio%d: %w", n, err)
	}
	return dir, nil
}

// openGPIOLine exports a GPIO if it isn't already and opens its value attribute.
// direction is passed to sysfs verbatim: "in", or "low"/"high" to set an output and its
// initial level in one write, so a strobe never glitches high on the way up.
func openGPIOLine(n int, direction string) (gpioLine, error) {
	dir, err := exportGPIO(n)
	if err != nil {
		return nil, err
	}
	// udev creates the attributes a moment after the directory appears, and on this
	// box that moment is occasionally long enough to lose the first write.
	for range 20 {
		if err = os.WriteFile(filepath.Join(dir, "direction"), []byte(direction), 0o200); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		return nil, fmt.Errorf("gpio%d direction: %w", n, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "value"), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("gpio%d value: %w", n, err)
	}
	d, err := os.OpenFile(filepath.Join(dir, "direction"), os.O_WRONLY, 0)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("gpio%d direction: %w", n, err)
	}
	return &sysfsGPIO{n: n, f: f, dir: d}, nil
}
