// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package abl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Serial is the Bose link on a UART: 19200 baud, 8 data bits, no parity, two stop bits, raw,
// as stock's line discipline sets it.
type Serial struct {
	f   *os.File
	buf [1]byte
}

// OpenSerial configures and opens dev (/dev/ttyS0, the module's UART0).
func OpenSerial(ctx context.Context, dev string) (*Serial, error) {
	if err := exec.CommandContext(ctx, "busybox", "stty", "-F", dev, "19200", "cs8", "cstopb", "-parenb",
		"raw", "-echo", "clocal", "cread", "-crtscts").Run(); err != nil {
		return nil, fmt.Errorf("abl: stty %s: %w", dev, err)
	}
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return &Serial{f: f}, nil
}

// Write sends p.
func (s *Serial) Write(p []byte) (int, error) { return s.f.Write(p) }

// ReadByteWithin reads one byte, or ErrTimeout after d.
func (s *Serial) ReadByteWithin(d time.Duration) (byte, error) {
	if err := s.f.SetReadDeadline(time.Now().Add(d)); err != nil {
		return 0, err
	}
	n, err := s.f.Read(s.buf[:])
	if errors.Is(err, os.ErrDeadlineExceeded) || n == 0 && err == nil {
		return 0, ErrTimeout
	}
	if err != nil {
		return 0, err
	}
	return s.buf[0], nil
}

// Close releases the line.
func (s *Serial) Close() error { return s.f.Close() }
