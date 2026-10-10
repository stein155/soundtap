// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package web

import "errors"

// diskFree is Linux-only; on a dev machine the health report simply omits disk.
func diskFree(string) (free, total uint64, err error) {
	return 0, 0, errors.New("statfs unavailable on this platform")
}
