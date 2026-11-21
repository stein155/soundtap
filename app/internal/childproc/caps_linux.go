// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package childproc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// Capability numbers (linux/capability.h); syscall names none of them.
const (
	CapKill           = 5
	CapSetgid         = 6
	CapSetuid         = 7
	CapNetBindService = 10
	CapNetAdmin       = 12
	CapNetRaw         = 13
	CapSetpcap        = 8
)

// SoundtapdCaps are the capabilities soundtapd keeps as its own user (ADR 0066): :80, the
// network tools it runs (Wi-Fi, DHCP, the setup AP, nft), and starting and stopping the
// media daemons as their own users.
var SoundtapdCaps = []uintptr{CapKill, CapSetgid, CapSetuid, CapNetBindService, CapNetAdmin, CapNetRaw}

// netCaps are what a network tool soundtapd starts gets: ip, iw, wpa_supplicant, hostapd,
// udhcpc and udhcpd, dnsd, nft and the scripts that call them.
var netCaps = []uintptr{CapNetAdmin, CapNetRaw, CapNetBindService}

const (
	capVersion3          = 0x20080522
	prCapAmbient         = 47
	prCapAmbientRaise    = 2
	prCapAmbientClearAll = 4
	prSetKeepcaps        = 8
	prCapbsetDrop        = 24
)

type capHeader struct {
	version uint32
	pid     int32
}

type capData struct {
	effective, permitted, inheritable uint32
}

func capget() ([2]capData, error) {
	h := capHeader{version: capVersion3}
	var d [2]capData
	if _, _, e := syscall.RawSyscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&d[0])), 0); e != 0 {
		return d, e
	}
	return d, nil
}

// HasCap reports whether this process holds capability c in its effective set.
func HasCap(c uintptr) bool {
	d, err := capget()
	return err == nil && d[c/32].effective&(1<<(c%32)) != 0
}

func hasAll(caps []uintptr) bool {
	for _, c := range caps {
		if !HasCap(c) {
			return false
		}
	}
	return true
}

// mayRunAs reports whether this process can start a command as another user: root, or a
// process holding CAP_SETUID and CAP_SETGID (soundtapd as its own user).
func mayRunAs() bool { return os.Geteuid() == 0 || hasAll([]uintptr{CapSetuid, CapSetgid}) }

// NetTool gives cmd the network capabilities as ambient ones, so a tool soundtapd starts
// as its own user can configure interfaces, open raw sockets and bind DHCP's and DNS's
// ports. Root needs none of this, and a process without the capabilities (a development
// run) leaves cmd as it is rather than fail to start it.
func NetTool(cmd *exec.Cmd) {
	if os.Geteuid() == 0 || !hasAll(netCaps) {
		return
	}
	attr(cmd).AmbientCaps = netCaps
}

// ClearAmbient empties this process's ambient capabilities on every thread. soundtapd
// arrives with them (that is how its capabilities survived the exec from soundtap-root),
// but an ambient capability is inherited by every child, and the media daemons must get
// none: a child that needs one asks for it (NetTool, KeepCaps).
func ClearAmbient() error {
	if _, _, e := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, prCapAmbient, prCapAmbientClearAll, 0, 0, 0, 0); e != 0 {
		return e
	}
	return nil
}

// ExecAs replaces this process, which must be root, with path running as user, in its
// primary group and groups, holding caps and no other capability (ADR 0066). The
// bounding set loses everything else first, while this is still root, so no later exec
// regains it; the capabilities are then raised as ambient ones, the only kind a program
// without file capabilities keeps across exec as a user other than root.
func ExecAs(user string, groups []string, caps []uintptr, path string, argv, env []string) error {
	if os.Geteuid() != 0 {
		return errors.New("exec-as must start as root")
	}
	uid, gid, err := Lookup(user)
	if err != nil {
		return err
	}
	gids := []int{int(gid)}
	for _, g := range groups {
		n, err := LookupGroup(g)
		if err != nil {
			return err
		}
		gids = append(gids, int(n))
	}
	if err := allThreads(prSetKeepcaps, 1); err != nil {
		return fmt.Errorf("keep capabilities: %w", err)
	}
	if err := limitBounding(caps); err != nil {
		return err
	}
	if err := syscall.Setgroups(gids); err != nil {
		return err
	}
	if err := syscall.Setgid(int(gid)); err != nil {
		return err
	}
	if err := syscall.Setuid(int(uid)); err != nil {
		return err
	}
	if err := holdOnly(caps); err != nil {
		return err
	}
	return syscall.Exec(path, argv, env)
}

// limitBounding drops every capability but caps from the bounding set of every thread.
func limitBounding(caps []uintptr) error {
	last, err := lastCap()
	if err != nil {
		return err
	}
	for c := uintptr(0); c <= last; c++ {
		if slices.Contains(caps, c) {
			continue
		}
		if err := allThreads(prCapbsetDrop, c); err != nil {
			return fmt.Errorf("drop capability %d from the bounding set: %w", c, err)
		}
	}
	return nil
}

// holdOnly makes caps the effective, permitted, inheritable and ambient sets of every thread.
func holdOnly(caps []uintptr) error {
	var d [2]capData
	for _, c := range caps {
		bit := uint32(1) << (c % 32)
		d[c/32].effective |= bit
		d[c/32].permitted |= bit
		d[c/32].inheritable |= bit
	}
	h := capHeader{version: capVersion3}
	if _, _, e := syscall.AllThreadsSyscall(syscall.SYS_CAPSET, uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&d[0])), 0); e != 0 {
		return fmt.Errorf("set capabilities: %w", e)
	}
	for _, c := range caps {
		if _, _, e := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, prCapAmbient, prCapAmbientRaise, c, 0, 0, 0); e != 0 {
			return fmt.Errorf("raise ambient capability %d: %w", c, e)
		}
	}
	return nil
}

func allThreads(option, arg uintptr) error {
	if _, _, e := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, option, arg, 0, 0, 0, 0); e != 0 {
		return e
	}
	return nil
}

// lastCap is the kernel's highest capability number.
func lastCap() (uintptr, error) {
	b, err := os.ReadFile("/proc/sys/kernel/cap_last_cap")
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 8)
	return uintptr(n), err
}
