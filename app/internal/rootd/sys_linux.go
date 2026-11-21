// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package rootd

import (
	"net"
	"syscall"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// peerUID is the uid of the process at the other end of c (SO_PEERCRED).
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if credErr != nil {
		return -1, credErr
	}
	return int(cred.Uid), nil
}

func sethostname(name string) error { return syscall.Sethostname([]byte(name)) }

func lookupUser(name string) (int, int, error) {
	uid, gid, err := childproc.Lookup(name)
	return int(uid), int(gid), err
}
