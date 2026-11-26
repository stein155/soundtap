// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

// Package childproc ties helper processes to soundtapd's lifetime.
package childproc

import (
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

// DieWithParent makes the kernel send cmd SIGTERM when soundtapd dies, so a supervisor
// restart does not leave the old helpers running next to the new ones.
func DieWithParent(cmd *exec.Cmd) {
	attr(cmd).Pdeathsig = syscall.SIGTERM
}

// RunAs starts cmd as the system user name with that user's primary group and no
// others (ADR 0066). Go drops the credentials before it sets the parent-death signal,
// which the kernel would otherwise clear, so the two combine in either order. soundtapd
// as its own user switches too: it holds CAP_SETUID and CAP_SETGID for exactly this (ADR
// 0066). A caller with neither root nor those (a development run) could not switch users
// anyway: cmd is left as is. An unknown user is an error, never a quiet fallback.
func RunAs(cmd *exec.Cmd, name string) error {
	if !mayRunAs() {
		return nil
	}

	uid, gid, err := Lookup(name)
	if err != nil {
		return err
	}

	attr(cmd).Credential = &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}

	return nil
}

// Lookup returns the uid and primary gid of the system user name.
func Lookup(name string) (uint32, uint32, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return 0, 0, fmt.Errorf("look up user %s: %w", name, err)
	}

	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("uid of %s: %w", name, err)
	}

	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("gid of %s: %w", name, err)
	}

	return uint32(uid), uint32(gid), nil
}

// LookupGroup returns the gid of the system group name.
func LookupGroup(name string) (uint32, error) {
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("look up group %s: %w", name, err)
	}

	n, err := strconv.ParseUint(group.Gid, 10, 32)

	return uint32(n), err
}

func attr(cmd *exec.Cmd) *syscall.SysProcAttr {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	return cmd.SysProcAttr
}

// KeepCaps gives a command that RunAs moved off root the capabilities caps as ambient
// ones, so they survive the switch. Without a user switch it does nothing: root has them.
func KeepCaps(cmd *exec.Cmd, caps []uintptr) {
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Credential != nil {
		cmd.SysProcAttr.AmbientCaps = caps
	}
}
