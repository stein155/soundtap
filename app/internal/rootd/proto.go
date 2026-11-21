// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package rootd is soundtap-root, the small root helper soundtapd asks for the few things
// its own user may not do (ADR 0066): write NAND and the U-Boot environment, reboot, reach
// U-Boot's boot count and the BCO's power switch, name the host, hand it a GPIO line, and
// touch the state the media daemons and BlueZ own.
//
// soundtapd runs as the user soundtapd. soundtap-root runs as root, started by
// init.d/soundtap, and listens on a unix socket only that user (and root) can reach. Each
// connection carries one typed request: an operation from a fixed list with its numbers,
// never a path or a command line. File contents travel as descriptors passed with the
// request (SCM_RIGHTS), so the helper opens nothing soundtapd names. Every request is
// checked against the live partition table and the NAND contract before anything runs,
// and logged.
package rootd

import "fmt"

// Default places on the speaker. init.d/soundtap creates Dir as root:soundtapd 0750 and
// tells soundtapd where the socket is through EnvVar.
const (
	Dir           = "/run/soundtap-root"
	DefaultSocket = Dir + "/sock"
	// MountDir is where slot.mount mounts the written slot: a directory only root writes,
	// so soundtapd cannot swap it for a link to somewhere else (flash.VerifyDir).
	MountDir = Dir + "/verify-rootfs"
	// EnvVar names the socket in soundtapd's environment. Unset (a development run, a
	// test, an older OS that starts soundtapd as root) soundtapd does the work itself.
	EnvVar = "SOUNDTAP_ROOT_HELPER"
)

// The operations. This list is the whole interface; ADR 0066 documents each one.
const (
	OpNANDErase      = "nand.erase"      // flash_erase: MTD, Offset, Blocks (0: to the end)
	OpNANDWrite      = "nand.write"      // nandwrite [-p] [-s Offset] MTD <file fd>
	OpNANDBlock      = "nand.block"      // dd one erase block, Skip in the file, to Offset
	OpUBIFormat      = "ubi.format"      // ubiformat MTD -f - -S Size from the stream fd
	OpUBIAttach      = "ubi.attach"      // ubiattach -m MTD -d 7 -O 2048, volumes readable
	OpUBIDetach      = "ubi.detach"      // ubidetach -d 7
	OpSlotMount      = "slot.mount"      // mount ubi7:rootfs read-only on MountDir
	OpSlotUmount     = "slot.umount"     // umount MountDir
	OpEnvSet         = "env.set"         // fw_setenv over Copies with the script fd
	OpReboot         = "reboot"          // reboot
	OpBootCountRead  = "bootcount.read"  // RTC SCRATCH2, in Value
	OpBootCountClear = "bootcount.clear" // clear SCRATCH2, reboot mode cold
	OpBCOPower       = "bco.power"       // the ST20 BCO's supply switch: On
	OpHostname       = "hostname.set"    // sethostname(Name)
	OpGPIOExport     = "gpio.export"     // export GPIO and hand its attributes to soundtapd
	OpClockSync      = "clock.sync"      // init.d/soundtap sync-clock, in the background
	OpBTCtl          = "bt.ctl"          // /opt/soundtap/bt-ctl.sh Name
	OpNVReset        = "nv.reset"        // the factory reset of state other users own
)

// Request is one operation and its arguments. Fields an operation does not use must be
// zero; unknown fields are refused.
type Request struct {
	Op     string `json:"op"`
	MTD    int    `json:"mtd,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Blocks int64  `json:"blocks,omitempty"`
	Skip   int64  `json:"skip,omitempty"`
	Pad    bool   `json:"pad,omitempty"`
	Size   int64  `json:"size,omitempty"`
	// Copies are the environment's places in NAND, as fw_env.config lists them.
	Copies []EnvCopy `json:"copies,omitempty"`
	On     bool      `json:"on,omitempty"`
	Name   string    `json:"name,omitempty"`
	GPIO   int       `json:"gpio,omitempty"`
	// Input says the first descriptor passed with the request is the command's input; Lock
	// that the next one is the operations lock, which the command holds while it runs.
	Input bool `json:"input,omitempty"`
	Lock  bool `json:"lock,omitempty"`
}

// EnvCopy is one line of fw_env.config: device, offset, size and erase-block size.
type EnvCopy struct {
	MTD       int   `json:"mtd"`
	Offset    int64 `json:"offset"`
	Size      int64 `json:"size"`
	EraseSize int64 `json:"eraseSize"`
}

// Response is the answer: Error is empty on success. Out is the command's output, Value
// SCRATCH2 for bootcount.read, Exit a script's exit status for bt.ctl.
type Response struct {
	Out   string `json:"out,omitempty"`
	Value uint32 `json:"value,omitempty"`
	Exit  int    `json:"exit,omitempty"`
	Error string `json:"error,omitempty"`
}

// MaxMessage bounds a request and a response on the wire.
const MaxMessage = 64 << 10

// ExitError is a script that ran and exited non-zero (bt.ctl).
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode is the exit status err carries, or -1.
func (e *ExitError) ExitCode() int { return e.Code }

// BTVerbs are the bt-ctl.sh commands bt.ctl runs.
var BTVerbs = []string{"next", "prev", "play", "pause", "reconnect", "disconnect", "trim"}
