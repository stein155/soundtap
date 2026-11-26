// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package hw

import (
	"errors"
	"os"
	"time"
)

// errNotLinux is the watchdog ioctl on a machine that is not the speaker.
var errNotLinux = errors.New("not linux")

func setWatchdogTimeout(*os.File, time.Duration) error { return errNotLinux }
