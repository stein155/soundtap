// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oled

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type spiOLED struct {
	spi, ce, reset, dc *os.File
	latest             []byte
}

// oledInit is the panel controller's start-up command sequence.
func oledInit() []byte {
	return []byte{
		0xae, 0x15, 0x00, 0x3f, 0x75, 0x00, 0x63, 0x81,
		0x85, 0xa0, 0x53, 0xa2, 0x1c, 0xa1, 0x00, 0xa4,
		0xa8, 0x63, 0xab, 0x01, 0xb1, 0x16, 0xb3, 0x90,
		0xb6, 0x0f, 0xbc, 0x07, 0xbe, 0x00, 0xd5, 0x63,
		0xb8, 0x03, 0x06, 0x08, 0x0b, 0x0e, 0x12, 0x16,
		0x1b, 0x1f, 0x24, 0x29, 0x2e, 0x33, 0x39, 0x3f,
	}
}

// gpioBanks are the AM335x GPIO controllers' register addresses, bank 0 to 3.
func gpioBanks() [gpioBankCount]string {
	return [gpioBankCount]string{"44e07000", "4804c000", "481ac000", "481ae000"}
}

// gpioSysfs is the sysfs GPIO class directory.
const gpioSysfs = "/sys/class/gpio"

// gpioDirection is what a sysfs GPIO's direction attribute is set to: an output that
// starts high or low.
type gpioDirection string

const (
	outHigh gpioDirection = "high"
	outLow  gpioDirection = "low"
)

// gpioLevel is what a sysfs GPIO's value attribute is set to.
type gpioLevel string

const (
	gpioLow  gpioLevel = "0"
	gpioHigh gpioLevel = "1"
)

// The data/command line's level: low for a command, high for frame data.
const (
	dcCommand = gpioLow
	dcData    = gpioHigh
)

// Timing of the panel's lines.
const (
	// resetDelay is how long reset is held low, and then left high before the init.
	resetDelay = 10 * time.Millisecond
	// lineDelay is how long each line change settles around a transfer.
	lineDelay = 10 * time.Millisecond
	// gpioRetries and gpioRetryDelay bound the wait for an exported line's attributes.
	gpioRetries    = 20
	gpioRetryDelay = 5 * time.Millisecond
)

// spiChunk is the most bytes one write hands spidev (its default buffer size).
const spiChunk = 4096

// The SPI settings: mode 3, 8-bit words, 2 MHz, and the spidev ioctls that set them
// (SPI_IOC_WR_MODE, SPI_IOC_WR_BITS_PER_WORD, SPI_IOC_WR_MAX_SPEED_HZ).
const (
	spiMode        = 3
	spiBitsPerWord = 8
	spiSpeedHz     = 2_000_000

	spiIocWrMode        = 0x40016b01
	spiIocWrBitsPerWord = 0x40016b03
	spiIocWrMaxSpeedHz  = 0x40046b04
)

var (
	errShortWrite         = errors.New("short write")
	errControllerNotFound = errors.New("not found")
)

func openSPIOLED(path, panel string) (_ *spiOLED, err error) {
	pins, err := parseSPIPins(panel)
	if err != nil {
		return nil, err
	}

	dev := &spiOLED{spi: nil, ce: nil, reset: nil, dc: nil, latest: nil}

	defer func() {
		if err != nil {
			dev.close()
		}
	}()

	dev.spi, err = os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("oled: open SPI: %w", err)
	}

	for _, pin := range []struct {
		name      string
		line      gpioLine
		direction gpioDirection
		file      **os.File
	}{{"CE", pins.ce, outHigh, &dev.ce}, {"reset", pins.reset, outHigh, &dev.reset}, {"DC", pins.dc, outLow, &dev.dc}} {
		var num int

		num, err = gpioNumber(pin.line)
		if err != nil {
			return nil, err
		}

		*pin.file, err = openGPIO(num, pin.direction)
		if err != nil {
			return nil, fmt.Errorf("oled: open %s: %w", pin.name, err)
		}
	}

	err = spiConfigure(dev.spi)
	if err != nil {
		return nil, fmt.Errorf("oled: configure SPI: %w", err)
	}

	err = setGPIO(dev.reset, gpioLow)
	if err != nil {
		return nil, fmt.Errorf("oled: reset low: %w", err)
	}

	time.Sleep(resetDelay)

	err = setGPIO(dev.reset, gpioHigh)
	if err != nil {
		return nil, fmt.Errorf("oled: reset high: %w", err)
	}

	time.Sleep(resetDelay)

	err = dev.transfer(dcCommand, oledInit())
	if err != nil {
		return nil, fmt.Errorf("oled: init: %w", err)
	}

	return dev, nil
}

// gpioNumber is the sysfs number of a bank's line.
func gpioNumber(gpio gpioLine) (int, error) {
	base, err := gpioBase(gpioBanks()[gpio.bank])
	if err != nil {
		return 0, fmt.Errorf("oled: find gpio%d: %w", gpio.bank, err)
	}

	return base + gpio.line, nil
}

func (o *spiOLED) close() {
	for _, f := range []*os.File{o.spi, o.ce, o.reset, o.dc} {
		if f != nil {
			_ = f.Close()
		}
	}
}

func (o *spiOLED) draw(pix []byte) error {
	transfers := []struct {
		dc  gpioLevel
		buf []byte
	}{
		{dc: dcCommand, buf: []byte{0x15, 0x00, 0x3f}},
		{dc: dcCommand, buf: []byte{0x75, 0x00, 0x63}},
		{dc: dcData, buf: packFrame(pix)},
		{dc: dcCommand, buf: []byte{0xaf}},
	}
	for _, transfer := range transfers {
		err := o.transfer(transfer.dc, transfer.buf)
		if err != nil {
			return fmt.Errorf("oled: SPI transfer: %w", err)
		}
	}

	o.latest = append(o.latest[:0], pix...)

	return nil
}

func (o *spiOLED) transfer(dc gpioLevel, buf []byte) error {
	err := setGPIO(o.dc, dc)
	if err != nil {
		return err
	}

	time.Sleep(lineDelay)

	err = setGPIO(o.ce, gpioLow)
	if err != nil {
		return err
	}

	time.Sleep(lineDelay)

	for len(buf) > 0 {
		chunk := min(len(buf), spiChunk)

		written, writeErr := o.spi.Write(buf[:chunk])
		if writeErr != nil {
			_ = setGPIO(o.ce, gpioHigh)

			return fmt.Errorf("spi: %w", writeErr)
		}

		if written != chunk {
			_ = setGPIO(o.ce, gpioHigh)

			return fmt.Errorf("%w: %d of %d", errShortWrite, written, chunk)
		}

		buf = buf[chunk:]
	}

	time.Sleep(lineDelay)

	err = setGPIO(o.ce, gpioHigh)
	if err != nil {
		return err
	}

	time.Sleep(lineDelay)

	return nil
}

func spiConfigure(dev *os.File) error {
	mode, bits, speed := byte(spiMode), byte(spiBitsPerWord), uint32(spiSpeedHz)

	settings := []struct {
		request uintptr
		value   unsafe.Pointer
	}{
		{spiIocWrMode, unsafe.Pointer(&mode)},
		{spiIocWrBitsPerWord, unsafe.Pointer(&bits)},
		{spiIocWrMaxSpeedHz, unsafe.Pointer(&speed)},
	}
	for _, setting := range settings {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dev.Fd(), setting.request, uintptr(setting.value))
		if errno != 0 {
			return fmt.Errorf("ioctl %#x: %w", setting.request, errno)
		}
	}

	return nil
}

func gpioBase(address string) (int, error) {
	chips, err := filepath.Glob("/sys/class/gpio/gpiochip*")
	if err != nil {
		return 0, fmt.Errorf("list gpio chips: %w", err)
	}

	for _, chip := range chips {
		target, err := os.Readlink(chip)
		if err != nil || !strings.Contains(target, address) {
			continue
		}

		base, err := os.ReadFile(filepath.Join(chip, "base"))
		if err != nil {
			return 0, fmt.Errorf("read gpio base: %w", err)
		}

		number, err := strconv.Atoi(strings.TrimSpace(string(base)))
		if err != nil {
			return 0, fmt.Errorf("gpio base: %w", err)
		}

		return number, nil
	}

	return 0, fmt.Errorf("controller %s %w", address, errControllerNotFound)
}

func openGPIO(number int, direction gpioDirection) (*os.File, error) {
	if ExportGPIO != nil {
		err := ExportGPIO(number)
		if err != nil {
			return nil, err
		}
	} else {
		err := writeSysfs("/sys/class/gpio/export", strconv.Itoa(number))
		if err != nil && !errors.Is(err, syscall.EBUSY) {
			return nil, err
		}
	}

	dir := filepath.Join(gpioSysfs, "gpio"+strconv.Itoa(number))

	var err error
	for range gpioRetries {
		err = writeSysfs(filepath.Join(dir, "direction"), string(direction))
		if err == nil {
			value, openErr := os.OpenFile(filepath.Join(dir, "value"), os.O_WRONLY, 0)
			if openErr != nil {
				return nil, fmt.Errorf("gpio value: %w", openErr)
			}

			return value, nil
		}

		time.Sleep(gpioRetryDelay)
	}

	return nil, err
}

func writeSysfs(path, value string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("sysfs: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(value)
	if err != nil {
		return fmt.Errorf("sysfs: %w", err)
	}

	return nil
}

func setGPIO(line *os.File, level gpioLevel) error {
	_, err := line.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("gpio: %w", err)
	}

	_, err = line.WriteString(string(level))
	if err != nil {
		return fmt.Errorf("gpio: %w", err)
	}

	return nil
}
