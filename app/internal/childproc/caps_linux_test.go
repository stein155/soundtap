// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package childproc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// A network tool still starts where its capabilities cannot be raised: as root (which has
// them) and as a user without them (a development run), NetTool leaves the command alone.
func TestNetToolNeverStopsAStart(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "true")
	NetTool(cmd)
	if os.Geteuid() == 0 && cmd.SysProcAttr != nil && len(cmd.SysProcAttr.AmbientCaps) > 0 {
		t.Error("root got ambient capabilities it already has")
	}
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}

// The capability check reads this process's own set: root holds CAP_SETUID, and RunAs
// switches users then.
func TestHasCapMatchesRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if !HasCap(CapSetuid) || !mayRunAs() {
		t.Error("root without CAP_SETUID")
	}
}

// Clearing ambient capabilities works on every thread of a pure-Go process; soundtapd
// does it first thing.
func TestClearAmbient(t *testing.T) {
	if err := ClearAmbient(); errors.Is(err, syscall.ENOTSUP) {
		t.Skip("a cgo build: AllThreadsSyscall is pure Go only, as soundtapd is built")
	} else if err != nil {
		t.Fatal(err)
	}
}
