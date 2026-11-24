// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package stm32amp

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

const ioctlI2CSlave = 0x0703

// Dev is the controller on an i2c-dev node: each Read and Write is one I2C message.
type Dev struct{ f *os.File }

// Open opens dev (/dev/i2c-1 on the SA-5) for Address.
func Open(dev string) (*Dev, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlI2CSlave, Address); e != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("stm32amp: I2C_SLAVE 0x%02x on %s: %w", Address, dev, e)
	}
	return &Dev{f: f}, nil
}

// Write sends one message.
func (d *Dev) Write(p []byte) error {
	n, err := d.f.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return err
}

// Read receives one message of len(p) bytes.
func (d *Dev) Read(p []byte) error {
	n, err := d.f.Read(p)
	if err == nil && n != len(p) {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// Close releases the node.
func (d *Dev) Close() error { return d.f.Close() }
