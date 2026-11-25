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
	err := exec.CommandContext(ctx, "busybox", "stty", "-F", dev, "19200", "cs8", "cstopb", "-parenb",
		"raw", "-echo", "clocal", "cread", "-crtscts").Run()
	if err != nil {
		return nil, fmt.Errorf("abl: stty %s: %w", dev, err)
	}

	file, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("abl: open %s: %w", dev, err)
	}

	return &Serial{f: file, buf: [1]byte{}}, nil
}

// Write sends p.
func (s *Serial) Write(p []byte) (int, error) {
	n, err := s.f.Write(p)
	if err != nil {
		return n, fmt.Errorf("abl: write: %w", err)
	}

	return n, nil
}

// ReadByteWithin reads one byte, or ErrTimeout after d.
func (s *Serial) ReadByteWithin(d time.Duration) (byte, error) {
	err := s.f.SetReadDeadline(time.Now().Add(d))
	if err != nil {
		return 0, fmt.Errorf("abl: read deadline: %w", err)
	}

	n, err := s.f.Read(s.buf[:])
	if errors.Is(err, os.ErrDeadlineExceeded) || n == 0 && err == nil {
		return 0, ErrTimeout
	}

	if err != nil {
		return 0, fmt.Errorf("abl: read: %w", err)
	}

	return s.buf[0], nil
}

// Close releases the line.
func (s *Serial) Close() error {
	err := s.f.Close()
	if err != nil {
		return fmt.Errorf("abl: close: %w", err)
	}

	return nil
}
