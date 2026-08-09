// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

// wdiocSetTimeout is WDIOC_SETTIMEOUT, _IOWR('W', 6, int).
const wdiocSetTimeout = 0xc0045706

func setWatchdogTimeout(f *os.File, d time.Duration) error {
	secs := int32(d / time.Second) //nolint:gosec // G115: watchdogTimeout, 120 s
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), wdiocSetTimeout, uintptr(unsafe.Pointer(&secs))); e != 0 {
		return e
	}
	return nil
}
