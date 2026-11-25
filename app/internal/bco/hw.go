// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// The BCO's supply switch is gpio0_14 (stock: dm870pwr, high = off). Mainline's ST20
// device tree hogs the line output-low so the BCO is on from boot, and a hogged line
// cannot be requested from userspace, so it is driven through the GPIO0 bank's set/clear
// registers. They touch only bit 14. Works the same under stock 3.14.
// /dev/mem, because of the hog; a gpio-line-names entry plus libgpiod-style
// request replaces it once the DT stops hogging the pin.
const (
	gpio0Base    = 0x44E07000
	gpio0Size    = 0x1000 // the GPIO0 bank's register window
	gpioOE       = 0x134
	gpioClearOut = 0x190
	gpioSetOut   = 0x194
	bcoPowerBit  = 1 << 14
)

// errPowerNotOutput means gpio0_14 is configured as an input, so driving it would do nothing.
var errPowerNotOutput = errors.New("gpio0_14 (BCO power) is not an output")

// Power is how the flash and the LAN watch reach the supply switch: MemPower as root,
// soundtap-root's bco.power when soundtapd runs as its own user (ADR 0066), which has no
// /dev/mem.
//
//nolint:gochecknoglobals // soundtapd replaces it at startup with soundtap-root's route (ADR 0066)
var Power = MemPower

// MemPower returns a Power func for Flasher, plus a close func.
func MemPower() (func(on bool) error, func(), error) {
	mem, err := os.OpenFile("/dev/mem", os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/mem: %w", err)
	}

	regs, err := syscall.Mmap(int(mem.Fd()), gpio0Base, gpio0Size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	// The mapping stays valid without the descriptor, and nothing was written through it.
	_ = mem.Close()

	if err != nil {
		return nil, nil, fmt.Errorf("map GPIO0: %w", err)
	}

	unmap := func() { _ = syscall.Munmap(regs) } // nothing to do about a failed unmap

	reg := func(off int) *uint32 { return (*uint32)(unsafe.Pointer(&regs[off])) }
	if *reg(gpioOE)&bcoPowerBit != 0 {
		unmap()

		return nil, nil, errPowerNotOutput
	}

	power := func(on bool) error {
		if on {
			*reg(gpioClearOut) = bcoPowerBit
		} else {
			*reg(gpioSetOut) = bcoPowerBit
		}

		return nil
	}

	return power, unmap, nil
}

// SerialPort opens the BCO UART as a non-blocking io.ReadWriter for Flasher.
func SerialPort(ctx context.Context, device string) (*FDPort, error) {
	fd, err := openSerial(ctx, device)
	if err != nil {
		return nil, err
	}

	return &FDPort{fd: fd}, nil
}

// FDPort is a non-blocking file descriptor: Read returns 0, nil when nothing is waiting.
type FDPort struct{ fd int }

func (p *FDPort) Read(b []byte) (int, error) {
	count, err := syscall.Read(p.fd, b)
	if errors.Is(err, syscall.EAGAIN) {
		return 0, nil
	}

	if count < 0 {
		count = 0
	}

	if err != nil {
		return count, fmt.Errorf("BCO UART read: %w", err)
	}

	return count, nil
}

func (p *FDPort) Write(b []byte) (int, error) {
	count, err := syscall.Write(p.fd, b)
	if err != nil {
		return count, fmt.Errorf("BCO UART write: %w", err)
	}

	return count, nil
}

// Close closes the UART's file descriptor.
func (p *FDPort) Close() error {
	err := syscall.Close(p.fd)
	if err != nil {
		return fmt.Errorf("BCO UART close: %w", err)
	}

	return nil
}

// UploadTFTP is the Upload func for Flasher on real hardware.
func UploadTFTP(ctx context.Context, image []byte) error {
	return tftpPut(ctx, BootloaderTFTP, "smsc_app.bcd", image)
}
