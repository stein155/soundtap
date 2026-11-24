// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package battery

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// The i2c-dev ioctls and SMBus transaction sizes (linux/i2c-dev.h, linux/i2c.h).
const (
	ioctlI2CSlave = 0x0703
	ioctlI2CSMBus = 0x0720

	smbusRead  = 1
	smbusWrite = 0

	smbusWordData     = 3
	smbusBlockData    = 5
	smbusI2CBlockData = 8

	smbusBlockMax = 32
)

// smbusData is union i2c_smbus_data: a byte, a word or a length-prefixed block.
type smbusData [smbusBlockMax + 2]byte

type smbusIoctl struct {
	readWrite uint8
	command   uint8
	size      uint32
	data      *smbusData
}

// SMBus is the pack on an i2c-dev node, without PEC (stock never turns it on).
type SMBus struct{ f *os.File }

// OpenSMBus opens dev (/dev/i2c-0 on the Portable) for the device at addr (0x0b).
func OpenSMBus(dev string, addr uint16) (*SMBus, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlI2CSlave, uintptr(addr)); e != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("battery: I2C_SLAVE 0x%02x on %s: %w", addr, dev, e)
	}
	return &SMBus{f: f}, nil
}

// Close releases the node.
func (b *SMBus) Close() error { return b.f.Close() }

func (b *SMBus) transfer(rw uint8, cmd byte, size uint32, d *smbusData) error {
	arg := smbusIoctl{readWrite: rw, command: cmd, size: size, data: d}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, b.f.Fd(), ioctlI2CSMBus, uintptr(unsafe.Pointer(&arg))); e != 0 {
		return fmt.Errorf("battery: SMBus 0x%02x: %w", cmd, e)
	}
	return nil
}

// ReadWord is an SMBus read word, little endian.
func (b *SMBus) ReadWord(cmd byte) (uint16, error) {
	var d smbusData
	if err := b.transfer(smbusRead, cmd, smbusWordData, &d); err != nil {
		return 0, err
	}
	return uint16(d[0]) | uint16(d[1])<<8, nil
}

// ReadBlock is an SMBus block read: the length byte, then that many bytes. It goes out as an
// I2C block read of the largest block, since the AM335x's I2C controller (i2c-omap) has no
// I2C_M_RECV_LEN: a real SMBus block read returns the pack's count and zeroes after it there.
func (b *SMBus) ReadBlock(cmd byte) ([]byte, error) {
	var d smbusData
	d[0] = smbusBlockMax
	if err := b.transfer(smbusRead, cmd, smbusI2CBlockData, &d); err != nil {
		return nil, err
	}
	return blockPayload(d[1 : 1+smbusBlockMax]), nil
}

// WriteBlock is an SMBus block write.
func (b *SMBus) WriteBlock(cmd byte, data []byte) error {
	if len(data) > smbusBlockMax {
		return fmt.Errorf("battery: block of %d bytes", len(data))
	}
	var d smbusData
	d[0] = byte(len(data)) //nolint:gosec // at most 32, checked above
	copy(d[1:], data)
	return b.transfer(smbusWrite, cmd, smbusBlockData, &d)
}
