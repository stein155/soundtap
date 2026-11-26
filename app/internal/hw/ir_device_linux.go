// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package hw

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// irDevice is /dev/ir opened for select(2)-driven reads.
//
// This deliberately bypasses os.File. ir_read() returns 0 on an empty fifo, which
// os.File reports as io.EOF, and Go's runtime poller cannot be used on this char
// device anyway — so the fd is waited on directly and read one byte at a time,
// which is all ir_read() ever delivers.
type irDevice struct{ fd int }

// fdSetBits is the fixed 1024-bit width of syscall.FdSet on every platform this
// builds for; only the word type differs (int32 on ARM and darwin, int64 on
// amd64), so the bit position is derived from the array length instead of being
// hard-coded per GOARCH.
const fdSetBits = 1024

// errFDTooHigh is a descriptor select(2) cannot express.
var errFDTooHigh = errors.New("ir: fd too high for select")

// openIRDevice opens ir.ko's /dev/ir, or rc-core's lirc device when path names one.
//
//nolint:ireturn // the two receivers are different types behind the one irSource
func openIRDevice(path string) (irSource, error) {
	if strings.Contains(path, "lirc") {
		return openLircDevice(path)
	}
	// O_NONBLOCK on open: a char device whose driver is loaded but whose IRQ never
	// fires must not be able to wedge startup in open(2).
	devFD, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("ir: open %s: %w", path, err)
	}

	if devFD >= fdSetBits {
		// select(2) cannot express this fd. soundtapd opens nowhere near 1024
		// files, so this is a corruption canary rather than a real limit.
		_ = syscall.Close(devFD)

		return nil, errFDTooHigh
	}

	return &irDevice{fd: devFD}, nil
}

func (d *irDevice) Close() error {
	err := syscall.Close(d.fd)
	if err != nil {
		return fmt.Errorf("ir: close: %w", err)
	}

	return nil
}

func (d *irDevice) next(timeout time.Duration) (byte, bool, error) {
	var set syscall.FdSet

	word := fdSetBits / len(set.Bits)
	set.Bits[d.fd/word] |= 1 << uint(d.fd%word)

	if timeout < 0 {
		timeout = 0
	}
	// NsecToTimeval, not a Timeval literal: the field widths differ per platform
	// (int32 on ARM, int64 on amd64, mixed on darwin).
	tv := syscall.NsecToTimeval(int64(timeout))

	ready, err := syscall.Select(d.fd+1, &set, nil, nil, &tv)
	if err != nil {
		if errors.Is(err, syscall.EINTR) {
			return 0, false, nil // a signal, not a device failure
		}

		return 0, false, fmt.Errorf("ir: select: %w", err)
	}

	if ready == 0 {
		return 0, false, nil
	}

	var buf [1]byte

	got, err := syscall.Read(d.fd, buf[:])
	if err != nil {
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			return 0, false, nil
		}

		return 0, false, fmt.Errorf("ir: read: %w", err)
	}

	if got == 0 {
		// The fifo emptied between select and read. Not an error and not EOF —
		// ir_read() has no end of file.
		return 0, false, nil
	}

	return buf[0], true, nil
}

// lircDevice is rc-core's /dev/lircN in MODE2, fed through lircFramer.
type lircDevice struct {
	fd int
	f  lircFramer
}

// lircSetRecMode is LIRC_SET_REC_MODE, _IOW('i', 0x00000012, __u32).
const (
	lircSetRecMode = 0x40046912
	lircModeMode2  = 0x00000004
	// lircQuiet is how long the line may stay silent before a pending frame is closed.
	lircQuiet = 50 * time.Millisecond
	// lircSampleSize is one MODE2 sample, a uint32.
	lircSampleSize = 4
)

func openLircDevice(path string) (*lircDevice, error) {
	devFD, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("ir: open %s: %w", path, err)
	}

	if devFD >= fdSetBits {
		_ = syscall.Close(devFD)

		return nil, errFDTooHigh
	}

	mode := uint32(lircModeMode2)

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(devFD), lircSetRecMode, uintptr(unsafe.Pointer(&mode)))
	if errno != 0 {
		_ = syscall.Close(devFD)

		return nil, fmt.Errorf("ir: LIRC_SET_REC_MODE on %s: %w", path, errno)
	}

	return &lircDevice{
		fd: devFD,
		f:  lircFramer{durs: nil, queue: nil, down: false, lastFrame: time.Time{}},
	}, nil
}

func (d *lircDevice) Close() error {
	err := syscall.Close(d.fd)
	if err != nil {
		return fmt.Errorf("ir: close: %w", err)
	}

	return nil
}

func (d *lircDevice) next(timeout time.Duration) (byte, bool, error) {
	if c, ok := d.f.pop(); ok {
		return c, true, nil
	}

	if (len(d.f.durs) > 0 || d.f.down) && timeout > lircQuiet {
		timeout = lircQuiet
	}

	ready, err := selectRead(d.fd, timeout)
	if err != nil {
		return 0, false, err
	}

	now := time.Now()
	if !ready {
		d.f.idle(now)
		c, ok := d.f.pop()

		return c, ok, nil
	}

	var buf [256]byte

	count, err := syscall.Read(d.fd, buf[:])
	if err != nil {
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			return 0, false, nil
		}

		return 0, false, fmt.Errorf("ir: read: %w", err)
	}

	for i := 0; i+lircSampleSize <= count; i += lircSampleSize {
		d.f.sample(binary.NativeEndian.Uint32(buf[i:]), now)
	}

	c, ok := d.f.pop()

	return c, ok, nil
}

func selectRead(devFD int, timeout time.Duration) (bool, error) {
	var set syscall.FdSet

	word := fdSetBits / len(set.Bits)
	set.Bits[devFD/word] |= 1 << uint(devFD%word)

	if timeout < 0 {
		timeout = 0
	}

	tv := syscall.NsecToTimeval(int64(timeout))

	ready, err := syscall.Select(devFD+1, &set, nil, nil, &tv)
	if err != nil {
		if errors.Is(err, syscall.EINTR) {
			return false, nil
		}

		return false, fmt.Errorf("ir: select: %w", err)
	}

	return ready > 0, nil
}
