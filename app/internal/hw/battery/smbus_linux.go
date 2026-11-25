// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package battery

import (
	"errors"
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

// errBlockTooLong is a block write longer than SMBus allows.
var errBlockTooLong = errors.New("battery: block longer than 32 bytes")

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
	file, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("battery: opening %s: %w", dev, err)
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), ioctlI2CSlave, uintptr(addr))
	if errno != 0 {
		_ = file.Close()

		return nil, fmt.Errorf("battery: I2C_SLAVE 0x%02x on %s: %w", addr, dev, errno)
	}

	return &SMBus{f: file}, nil
}

// Close releases the node.
func (b *SMBus) Close() error {
	err := b.f.Close()
	if err != nil {
		return fmt.Errorf("battery: closing the bus: %w", err)
	}

	return nil
}

// ReadWord is an SMBus read word, little endian.
func (b *SMBus) ReadWord(cmd byte) (uint16, error) {
	var buf smbusData

	err := b.transfer(smbusRead, cmd, smbusWordData, &buf)
	if err != nil {
		return 0, err
	}

	return uint16(buf[0]) | uint16(buf[1])<<8, nil
}

// ReadBlock is an SMBus block read: the length byte, then that many bytes. It goes out as an
// I2C block read of the largest block, since the AM335x's I2C controller (i2c-omap) has no
// I2C_M_RECV_LEN: a real SMBus block read returns the pack's count and zeroes after it there.
func (b *SMBus) ReadBlock(cmd byte) ([]byte, error) {
	var buf smbusData

	buf[0] = smbusBlockMax

	err := b.transfer(smbusRead, cmd, smbusI2CBlockData, &buf)
	if err != nil {
		return nil, err
	}

	return blockPayload(buf[1 : 1+smbusBlockMax]), nil
}

// WriteBlock is an SMBus block write.
func (b *SMBus) WriteBlock(cmd byte, data []byte) error {
	if len(data) > smbusBlockMax {
		return fmt.Errorf("%w: %d bytes", errBlockTooLong, len(data))
	}

	var buf smbusData

	buf[0] = byte(len(data)) //nolint:gosec // at most 32, checked above
	copy(buf[1:], data)

	return b.transfer(smbusWrite, cmd, smbusBlockData, &buf)
}

func (b *SMBus) transfer(readWrite uint8, cmd byte, size uint32, buf *smbusData) error {
	arg := smbusIoctl{readWrite: readWrite, command: cmd, size: size, data: buf}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, b.f.Fd(), ioctlI2CSMBus, uintptr(unsafe.Pointer(&arg)))
	if errno != 0 {
		return fmt.Errorf("battery: SMBus 0x%02x: %w", cmd, errno)
	}

	return nil
}
