// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package battery

import "errors"

// SMBus is the pack's bus; only Linux has i2c-dev.
type SMBus struct{}

// OpenSMBus fails off Linux.
func OpenSMBus(string, uint16) (*SMBus, error) { return nil, errors.New("battery: no i2c-dev here") }

// Close does nothing.
func (*SMBus) Close() error { return nil }

// ReadWord fails off Linux.
func (*SMBus) ReadWord(byte) (uint16, error) { return 0, errors.ErrUnsupported }

// ReadBlock fails off Linux.
func (*SMBus) ReadBlock(byte) ([]byte, error) { return nil, errors.ErrUnsupported }

// WriteBlock fails off Linux.
func (*SMBus) WriteBlock(byte, []byte) error { return errors.ErrUnsupported }
