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
//
//nolint:gochecknoglobals // exported for soundtap-root, which hands exactly these to soundtapd
var SoundtapdCaps = []uintptr{CapKill, CapSetgid, CapSetuid, CapNetBindService, CapNetAdmin, CapNetRaw}

// netCaps are what a network tool soundtapd starts gets: ip, iw, wpa_supplicant, hostapd,
// udhcpc and udhcpd, dnsd, nft and the scripts that call them.
func netCaps() []uintptr { return []uintptr{CapNetAdmin, CapNetRaw, CapNetBindService} }

// errNotRoot: ExecAs drops root, so it has to start there.
var errNotRoot = errors.New("exec-as must start as root")

const (
	capVersion3          = 0x20080522
	prCapAmbient         = 47
	prCapAmbientRaise    = 2
	prCapAmbientClearAll = 4
	prSetKeepcaps        = 8
	prCapbsetDrop        = 24
	// capWordBits is how many capabilities one 32-bit word of the capget/capset data holds.
	capWordBits = 32
)

type capHeader struct {
	version uint32
	pid     int32
}

type capData struct {
	effective, permitted, inheritable uint32
}

func capget() ([2]capData, error) {
	header := capHeader{version: capVersion3, pid: 0}

	var data [2]capData

	_, _, errno := syscall.RawSyscall(
		syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0)
	if errno != 0 {
		return data, fmt.Errorf("get capabilities: %w", errno)
	}

	return data, nil
}

// HasCap reports whether this process holds capability c in its effective set.
func HasCap(c uintptr) bool {
	data, err := capget()

	return err == nil && data[c/capWordBits].effective&(1<<(c%capWordBits)) != 0
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
	if os.Geteuid() == 0 || !hasAll(netCaps()) {
		return
	}

	attr(cmd).AmbientCaps = netCaps()
}

// ClearAmbient empties this process's ambient capabilities on every thread. soundtapd
// arrives with them (that is how its capabilities survived the exec from soundtap-root),
// but an ambient capability is inherited by every child, and the media daemons must get
// none: a child that needs one asks for it (NetTool, KeepCaps).
func ClearAmbient() error {
	_, _, errno := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, prCapAmbient, prCapAmbientClearAll, 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("clear ambient capabilities: %w", errno)
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
		return errNotRoot
	}

	uid, gid, err := Lookup(user)
	if err != nil {
		return err
	}

	gids := []int{int(gid)}

	for _, g := range groups {
		groupGID, groupErr := LookupGroup(g)
		if groupErr != nil {
			return groupErr
		}

		gids = append(gids, int(groupGID))
	}

	err = allThreads(prSetKeepcaps, 1)
	if err != nil {
		return fmt.Errorf("keep capabilities: %w", err)
	}

	err = limitBounding(caps)
	if err != nil {
		return err
	}

	err = syscall.Setgroups(gids)
	if err != nil {
		return fmt.Errorf("set groups: %w", err)
	}

	err = syscall.Setgid(int(gid))
	if err != nil {
		return fmt.Errorf("set gid: %w", err)
	}

	err = syscall.Setuid(int(uid))
	if err != nil {
		return fmt.Errorf("set uid: %w", err)
	}

	err = holdOnly(caps)
	if err != nil {
		return err
	}

	err = syscall.Exec(path, argv, env)

	return fmt.Errorf("exec %s: %w", path, err)
}

// limitBounding drops every capability but caps from the bounding set of every thread.
func limitBounding(caps []uintptr) error {
	last, err := lastCap()
	if err != nil {
		return err
	}

	for capability := uintptr(0); capability <= last; capability++ {
		if slices.Contains(caps, capability) {
			continue
		}

		err := allThreads(prCapbsetDrop, capability)
		if err != nil {
			return fmt.Errorf("drop capability %d from the bounding set: %w", capability, err)
		}
	}

	return nil
}

// holdOnly makes caps the effective, permitted, inheritable and ambient sets of every thread.
func holdOnly(caps []uintptr) error {
	var data [2]capData

	for _, c := range caps {
		bit := uint32(1) << (c % capWordBits)
		data[c/capWordBits].effective |= bit
		data[c/capWordBits].permitted |= bit
		data[c/capWordBits].inheritable |= bit
	}

	header := capHeader{version: capVersion3, pid: 0}

	_, _, errno := syscall.AllThreadsSyscall(
		syscall.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0)
	if errno != 0 {
		return fmt.Errorf("set capabilities: %w", errno)
	}

	for _, c := range caps {
		_, _, errno := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, prCapAmbient, prCapAmbientRaise, c, 0, 0, 0)
		if errno != 0 {
			return fmt.Errorf("raise ambient capability %d: %w", c, errno)
		}
	}

	return nil
}

func allThreads(option, arg uintptr) error {
	_, _, errno := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, option, arg, 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("prctl %d: %w", option, errno)
	}

	return nil
}

// lastCap is the kernel's highest capability number.
func lastCap() (uintptr, error) {
	b, err := os.ReadFile("/proc/sys/kernel/cap_last_cap")
	if err != nil {
		return 0, fmt.Errorf("read the last capability: %w", err)
	}

	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 8)

	return uintptr(n), err
}
