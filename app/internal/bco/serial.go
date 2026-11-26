// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// serialSem serialises access to the single BCO UART. It is a channel rather than a
// sync.Mutex because a site survey holds the port for over a minute: a caller that
// arrives meanwhile — or whose request the user already navigated away from — must be
// able to give up instead of blocking uninterruptibly behind it.
//
//nolint:gochecknoglobals // the speaker has one BCO UART, so its lock is one per process
var serialSem = make(chan struct{}, 1)

// The live association's errors; each keeps the text it had before it was a sentinel.
var (
	errNotLinked = errors.New("BCO Wi-Fi is not associated")
	errNoSSID    = errors.New("BCO associated SSID is empty")
)

// lockSerial takes the BCO UART, or returns ctx's error if that happens first.
// Call unlockSerial (deferred) only when it returns nil.
func lockSerial(ctx context.Context) error {
	select {
	case serialSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("BCO UART busy: %w", ctx.Err())
	}
}

func unlockSerial() { <-serialSem }

// CurrentSSID reports the BCO radio's live association, never merely its stored profile.
func CurrentSSID(ctx context.Context, device string) (string, error) {
	status, err := ReadStatus(ctx, device)
	if err != nil {
		return "", err
	}

	return associatedSSID(&status)
}

// associatedSSID is status's network name, or an error while it is not associated.
func associatedSSID(status *Status) (string, error) {
	if !status.Linked {
		return "", fmt.Errorf("%w (state %s)", errNotLinked, status.State)
	}

	ssid := strings.TrimSpace(status.SSID)
	if ssid == "" {
		return "", errNoSSID
	}

	return ssid, nil
}

func openSerial(ctx context.Context, device string) (int, error) {
	if device == "" {
		device = "/dev/ttyS0"
	}

	// No shell: device is one argument to stty, from soundtapd's own environment or flags.
	//nolint:gosec // G702: exec without a shell; the device path cannot inject a command
	stty := exec.CommandContext(ctx, "busybox", "stty", "-F", device, "115200", "raw", "-echo")

	err := stty.Run()
	if err != nil {
		return -1, fmt.Errorf("BCO serial setup: %w", err)
	}

	fd, err := syscall.Open(device, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return -1, fmt.Errorf("BCO serial open: %w", err)
	}

	return fd, nil
}
