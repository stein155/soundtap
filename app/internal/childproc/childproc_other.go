// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

// Package childproc ties helper processes to soundtapd's lifetime.
package childproc

import (
	"errors"
	"os/exec"
)

// Off Linux there are no system users, groups or capabilities to switch to.
var (
	errNoUsers  = errors.New("childproc: no system users off linux")
	errNoGroups = errors.New("childproc: no system groups off linux")
	errNoExecAs = errors.New("childproc: exec-as is Linux only")
)

// DieWithParent is a no-op off Linux: there is no parent-death signal.
func DieWithParent(*exec.Cmd) {}

// RunAs only works on Linux; elsewhere the helper keeps running as the caller.
func RunAs(*exec.Cmd, string) error { return nil }

// Lookup has no system users to look up off Linux.
func Lookup(string) (uint32, uint32, error) {
	return 0, 0, errNoUsers
}

// LookupGroup has no system groups to look up off Linux.
func LookupGroup(string) (uint32, error) {
	return 0, errNoGroups
}

// KeepCaps is a no-op off Linux: there are no ambient capabilities.
func KeepCaps(*exec.Cmd, []uintptr) {}

// Capability numbers, so callers build everywhere; off Linux nothing holds them.
const (
	CapKill           = 5
	CapSetgid         = 6
	CapSetuid         = 7
	CapNetBindService = 10
	CapNetAdmin       = 12
	CapNetRaw         = 13
)

// SoundtapdCaps are soundtapd's capabilities on the speaker (caps_linux.go).
//
//nolint:gochecknoglobals // exported for soundtap-root, which hands exactly these to soundtapd
var SoundtapdCaps = []uintptr{CapKill, CapSetgid, CapSetuid, CapNetBindService, CapNetAdmin, CapNetRaw}

// HasCap is false off Linux.
func HasCap(uintptr) bool { return false }

// NetTool is a no-op off Linux.
func NetTool(*exec.Cmd) {}

// ClearAmbient is a no-op off Linux.
func ClearAmbient() error { return nil }

// ExecAs only works on Linux.
func ExecAs(string, []string, []uintptr, string, []string, []string) error {
	return errNoExecAs
}
