// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package main

import (
	"log/slog"
	"syscall"
	"unsafe"
)

// fSetPipeSize is F_SETPIPE_SZ. The syscall package does not export it.
const fSetPipeSize = 1031

// setPipeSize caps a kernel pipe buffer. Every byte a pipe holds between the Mixer
// and the DAC is audio that a flush can no longer drop, so both pipes in the chain
// are kept small — see audio.SinkPipeBytes / audio.BusPipeBytes for the budget.
//
// The kernel rounds the request up to a power of two of at least one page, refuses to
// shrink below the currently unread data, and caps unprivileged requests at
// /proc/sys/fs/pipe-max-size. All of that is fine: this is an optimisation, so a
// failure is logged and playback continues with the default size.
func setPipeSize(f any, size int, what string, log *slog.Logger) {
	h, ok := f.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return
	}
	// File.Fd switches a pollable pipe back to blocking mode. That prevents a
	// flush deadline from waking its reader; inspect it through RawConn instead.
	raw, err := h.SyscallConn()
	if err == nil {
		var errno syscall.Errno
		err = raw.Control(func(fd uintptr) {
			_, _, errno = syscall.Syscall(syscall.SYS_FCNTL, fd, fSetPipeSize, uintptr(size))
		})
		if err == nil && errno != 0 {
			err = errno
		}
	}
	if err != nil {
		log.Warn("pipe size", "what", what, "bytes", size, "err", err)
	}
}

// pipeQueued is how many bytes wait in the pipe or FIFO behind fd (FIONREAD), from either
// end and without reading them.
func pipeQueued(fd uintptr) (int, error) {
	var n int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCINQ, uintptr(unsafe.Pointer(&n))); errno != 0 {
		return 0, errno
	}
	return int(n), nil
}
