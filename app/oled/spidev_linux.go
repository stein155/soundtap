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

var oledInit = []byte{
	0xae, 0x15, 0x00, 0x3f, 0x75, 0x00, 0x63, 0x81,
	0x85, 0xa0, 0x53, 0xa2, 0x1c, 0xa1, 0x00, 0xa4,
	0xa8, 0x63, 0xab, 0x01, 0xb1, 0x16, 0xb3, 0x90,
	0xb6, 0x0f, 0xbc, 0x07, 0xbe, 0x00, 0xd5, 0x63,
	0xb8, 0x03, 0x06, 0x08, 0x0b, 0x0e, 0x12, 0x16,
	0x1b, 0x1f, 0x24, 0x29, 0x2e, 0x33, 0x39, 0x3f,
}

// gpioBanks are the AM335x GPIO controllers' register addresses, bank 0 to 3.
var gpioBanks = [4]string{"44e07000", "4804c000", "481ac000", "481ae000"}

func openSPIOLED(path, panel string) (_ *spiOLED, err error) {
	pins, err := parseSPIPins(panel)
	if err != nil {
		return nil, err
	}
	number := func(l gpioLine) (int, error) {
		base, err := gpioBase(gpioBanks[l.bank])
		if err != nil {
			return 0, fmt.Errorf("oled: find gpio%d: %w", l.bank, err)
		}
		return base + l.line, nil
	}
	o := &spiOLED{}
	defer func() {
		if err != nil {
			o.close()
		}
	}()
	if o.spi, err = os.OpenFile(path, os.O_WRONLY, 0); err != nil {
		return nil, fmt.Errorf("oled: open SPI: %w", err)
	}
	for _, p := range []struct {
		name string
		line gpioLine
		high bool
		file **os.File
	}{{"CE", pins.ce, true, &o.ce}, {"reset", pins.reset, true, &o.reset}, {"DC", pins.dc, false, &o.dc}} {
		n, err := number(p.line)
		if err != nil {
			return nil, err
		}
		if *p.file, err = openGPIO(n, p.high); err != nil {
			return nil, fmt.Errorf("oled: open %s: %w", p.name, err)
		}
	}
	if err = spiConfigure(o.spi); err != nil {
		return nil, fmt.Errorf("oled: configure SPI: %w", err)
	}
	if err = setGPIO(o.reset, false); err != nil {
		return nil, fmt.Errorf("oled: reset low: %w", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err = setGPIO(o.reset, true); err != nil {
		return nil, fmt.Errorf("oled: reset high: %w", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err = o.transfer(false, oledInit); err != nil {
		return nil, fmt.Errorf("oled: init: %w", err)
	}
	return o, nil
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
		data bool
		buf  []byte
	}{
		{buf: []byte{0x15, 0x00, 0x3f}},
		{buf: []byte{0x75, 0x00, 0x63}},
		{data: true, buf: packFrame(pix)},
		{buf: []byte{0xaf}},
	}
	for _, transfer := range transfers {
		if err := o.transfer(transfer.data, transfer.buf); err != nil {
			return fmt.Errorf("oled: SPI transfer: %w", err)
		}
	}
	o.latest = append(o.latest[:0], pix...)
	return nil
}

func (o *spiOLED) transfer(data bool, buf []byte) error {
	if err := setGPIO(o.dc, data); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	if err := setGPIO(o.ce, false); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	for len(buf) > 0 {
		n := min(len(buf), 4096)
		written, err := o.spi.Write(buf[:n])
		if err != nil {
			_ = setGPIO(o.ce, true)
			return err
		}
		if written != n {
			_ = setGPIO(o.ce, true)
			return fmt.Errorf("short write: %d of %d", written, n)
		}
		buf = buf[n:]
	}
	time.Sleep(10 * time.Millisecond)
	if err := setGPIO(o.ce, true); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	return nil
}

func spiConfigure(f *os.File) error {
	mode, bits, speed := byte(3), byte(8), uint32(2_000_000)
	settings := []struct {
		request uintptr
		value   unsafe.Pointer
	}{
		{0x40016b01, unsafe.Pointer(&mode)},
		{0x40016b03, unsafe.Pointer(&bits)},
		{0x40046b04, unsafe.Pointer(&speed)},
	}
	for _, setting := range settings {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), setting.request, uintptr(setting.value)); errno != 0 {
			return errno
		}
	}
	return nil
}

func gpioBase(address string) (int, error) {
	chips, err := filepath.Glob("/sys/class/gpio/gpiochip*")
	if err != nil {
		return 0, err
	}
	for _, chip := range chips {
		target, err := os.Readlink(chip)
		if err != nil || !strings.Contains(target, address) {
			continue
		}
		base, err := os.ReadFile(filepath.Join(chip, "base"))
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(string(base)))
	}
	return 0, fmt.Errorf("controller %s not found", address)
}

func openGPIO(number int, high bool) (*os.File, error) {
	if ExportGPIO != nil {
		if err := ExportGPIO(number); err != nil {
			return nil, err
		}
	} else if err := writeSysfs("/sys/class/gpio/export", strconv.Itoa(number)); err != nil && !errors.Is(err, syscall.EBUSY) {
		return nil, err
	}
	dir := filepath.Join("/sys/class/gpio", "gpio"+strconv.Itoa(number))
	direction := "low"
	if high {
		direction = "high"
	}
	var err error
	for range 20 {
		err = writeSysfs(filepath.Join(dir, "direction"), direction)
		if err == nil {
			return os.OpenFile(filepath.Join(dir, "value"), os.O_WRONLY, 0)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil, err
}

func writeSysfs(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write([]byte(value))
	return err
}

func setGPIO(f *os.File, high bool) error {
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	value := "0"
	if high {
		value = "1"
	}
	_, err := f.Write([]byte(value))
	return err
}
