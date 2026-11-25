// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package web

import "errors"

// diskFree is Linux-only; on a dev machine the health report simply omits disk.
// It returns free, then total.
func diskFree(string) (uint64, uint64, error) {
	return 0, 0, errNoStatfs
}

var errNoStatfs = errors.New("statfs unavailable on this platform")
