// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package abl

import (
	"context"
	"errors"
	"time"
)

// Serial is the Bose link's UART; only Linux has one.
type Serial struct{}

// OpenSerial fails off Linux.
func OpenSerial(context.Context, string) (*Serial, error) {
	return nil, errors.New("abl: no UART here")
}

// Write fails off Linux.
func (*Serial) Write([]byte) (int, error) { return 0, errors.ErrUnsupported }

// ReadByteWithin fails off Linux.
func (*Serial) ReadByteWithin(time.Duration) (byte, error) { return 0, errors.ErrUnsupported }

// Close does nothing.
func (*Serial) Close() error { return nil }
