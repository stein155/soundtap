// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package stm32amp

import "errors"

// Dev is the controller's node; only Linux has i2c-dev.
type Dev struct{}

// Open fails off Linux.
func Open(string) (*Dev, error) { return nil, errors.New("stm32amp: no i2c-dev here") }

// Write fails off Linux.
func (*Dev) Write([]byte) error { return errors.ErrUnsupported }

// Read fails off Linux.
func (*Dev) Read([]byte) error { return errors.ErrUnsupported }

// Close does nothing.
func (*Dev) Close() error { return nil }
