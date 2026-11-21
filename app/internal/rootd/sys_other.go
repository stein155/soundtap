// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package rootd

import (
	"errors"
	"net"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// The helper runs on the speaker only; elsewhere a test supplies these.
var errNotLinux = errors.New("soundtap-root runs on Linux only")

func peerUID(*net.UnixConn) (int, error) { return -1, errNotLinux }

func sethostname(string) error { return errNotLinux }

func lookupUser(name string) (int, int, error) {
	uid, gid, err := childproc.Lookup(name)
	return int(uid), int(gid), err
}
