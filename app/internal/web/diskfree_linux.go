// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package web

import (
	"errors"
	"fmt"
	"syscall"
)

// errBlockSize is a filesystem that reports a block size that is not positive.
var errBlockSize = errors.New("block size")

// diskFree reports the bytes available to a non-root writer and the filesystem size.
// Bavail, not Bfree: the reserved blocks are not ours to stage an update in.
// It returns free, then total.
func diskFree(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t

	err := syscall.Statfs(path, &stat)
	if err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}

	if stat.Bsize <= 0 {
		return 0, 0, fmt.Errorf("statfs %s: %w %d", path, errBlockSize, stat.Bsize)
	}

	blockSize := uint64(stat.Bsize)

	return stat.Bavail * blockSize, stat.Blocks * blockSize, nil
}
