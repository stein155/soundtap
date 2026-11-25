// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package rootd

import (
	"fmt"
	"net"
	"syscall"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// peerUID is the uid of the process at the other end of conn (SO_PEERCRED).
func peerUID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return -1, fmt.Errorf("SO_PEERCRED: %w", err)
	}

	var (
		cred    *syscall.Ucred
		credErr error
	)

	err = raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return -1, fmt.Errorf("SO_PEERCRED: %w", err)
	}

	if credErr != nil {
		return -1, fmt.Errorf("SO_PEERCRED: %w", credErr)
	}

	return int(cred.Uid), nil
}

func sethostname(name string) error {
	err := syscall.Sethostname([]byte(name))
	if err != nil {
		return fmt.Errorf("sethostname: %w", err)
	}

	return nil
}

func lookupUser(name string) (int, int, error) {
	uid, gid, err := childproc.Lookup(name)

	return int(uid), int(gid), err
}
