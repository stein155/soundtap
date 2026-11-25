// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/flash"
)

// PendingPath is the install record in the speaker's home.
const PendingPath = "firmware-pending.json"

// Pending is written only after the final kernel write made the alternate slot bootable.
// It remains after boot so support can distinguish successful boot from rollback.
type Pending struct {
	TargetSlot string         `json:"targetSlot"`
	Package    PendingPackage `json:"package"`
}

// PendingPackage is what the install record says about the package it wrote.
type PendingPackage struct {
	Size   int64    `json:"size"`
	Kernel string   `json:"kernel"`
	Models []string `json:"models"`
}

// readNANDTimeout bounds one read-only NAND command: a whole-partition dump is slow.
const readNANDTimeout = 20 * time.Minute

// errInvalidTargetSlot refuses an install record naming neither slot.
var errInvalidTargetSlot = errors.New("invalid target slot")

// recoveryStatus is a Recovery holding only its status and error.
func recoveryStatus(status string, err error) Recovery {
	return Recovery{
		Pending: Pending{TargetSlot: "", Package: PendingPackage{Size: 0, Kernel: "", Models: nil}},
		Status:  status, Running: "", Err: err,
	}
}

// Recovery is what the last install left for support to read after the reboot.
// Status is "none", "unreadable", "unknown", "booted" or "mismatch".
type Recovery struct {
	Pending Pending
	Status  string
	Running string // the slot this boot runs from
	Err     error
}

// LoadRecovery reads the install record in home against the slot this boot runs from.
func LoadRecovery(home string, readCmdline func() (string, error)) Recovery {
	record, err := os.ReadFile(filepath.Join(home, PendingPath))
	if os.IsNotExist(err) {
		return recoveryStatus("none", nil)
	}

	if err != nil {
		return recoveryStatus("unreadable", err)
	}

	var pending Pending

	err = json.Unmarshal(record, &pending)
	if err != nil || (pending.TargetSlot != "PING" && pending.TargetSlot != "PONG") {
		if err == nil {
			err = errInvalidTargetSlot
		}

		return recoveryStatus("unreadable", err)
	}

	recovery := recoveryStatus("", nil)
	recovery.Pending = pending

	cmdline, err := readCmdline()
	if err != nil {
		recovery.Status, recovery.Err = "unknown", err

		return recovery
	}

	slot, err := flash.SlotFromCmdline(cmdline)
	if err != nil {
		recovery.Status, recovery.Err = "unknown", err

		return recovery
	}

	recovery.Running = slot.Name
	if slot.Name == pending.TargetSlot {
		recovery.Status = "booted"
	} else {
		recovery.Status = "mismatch"
	}

	return recovery
}

// WriteDebug renders r for the diagnostics page.
func (r *Recovery) WriteDebug(out *strings.Builder) {
	fmt.Fprintf(out, "status     : %s\n", r.Status)

	if r.Status == "none" {
		return
	}

	if r.Err != nil {
		fmt.Fprintf(out, "error      : %v\n", r.Err)

		return
	}

	fmt.Fprintf(out, "target slot: %s\n", r.Pending.TargetSlot)
	fmt.Fprintf(out, "running slot: %s\n", r.Running)
	fmt.Fprintf(out, "package    : %s, %d bytes, %s\n",
		r.Pending.Package.Kernel, r.Pending.Package.Size, strings.Join(r.Pending.Package.Models, ", "))
}

// ReadNAND runs one read-only mtd-utils or fw_printenv command and returns its stdout.
func ReadNAND(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), readNANDTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).Output()

	ee := (*exec.ExitError)(nil)
	if errors.As(err, &ee) {
		// fw_printenv says why it refused ("Cannot read environment") on stderr only.
		return nil, fmt.Errorf("%s: %w: %s", name, err, bytes.TrimSpace(ee.Stderr))
	}

	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	return out, nil
}

// LivePlan is what the running speaker can tell about its own NAND and slots.
func LivePlan() (flash.Target, error) {
	cmdline, err := flash.ReadCmdline()
	if err != nil {
		return flash.Target{}, fmt.Errorf("kernel command line: %w", err)
	}

	mtd, err := flash.ReadMTD()
	if err != nil {
		return flash.Target{}, fmt.Errorf("MTD partitions: %w", err)
	}

	target, err := flash.Plan(cmdline, mtd)
	if err == nil {
		return target, nil
	}
	// RAM recovery runs no NAND slot; WLA is the only approved caller.
	if !errors.Is(err, flash.ErrUnknownSlot) || !HasCmdlineField(cmdline, "soundtap.ram=1") {
		return flash.Target{}, fmt.Errorf("install plan: %w", err)
	}

	model, modelErr := LiveModel()
	if modelErr != nil || model != modelWLA {
		return flash.Target{}, fmt.Errorf("install plan: %w", err)
	}
	// The slot the environment boots stands in for the running one, so the install
	// writes the other. Unreadable: PING, as before.
	first, err := flash.RecoveryPlan(mtd, "")
	if err != nil {
		return first, fmt.Errorf("recovery plan: %w", err)
	}
	// The U-Boot on NAND is not the one running, so its format comes from NAND: a copy of
	// the redundant environment that checks out, or else the single 8 KiB copy, which our
	// U-Boot reads as well.
	for _, redundant := range []bool{true, false} {
		first.EnvRedundant = redundant

		env, envErr := (&flash.Installer{Target: first, Run: nil, Open: nil}).Env(ReadNAND)
		if envErr == nil {
			selected, planErr := flash.RecoveryPlan(mtd, flash.SelectedSlot(env))
			selected.EnvRedundant = redundant

			if planErr != nil {
				return selected, fmt.Errorf("recovery plan: %w", planErr)
			}

			return selected, nil
		}
	}

	first.EnvRedundant = false

	return first, nil
}

// HasCmdlineField reports whether the kernel command line holds want as a whole field.
func HasCmdlineField(cmdline, want string) bool {
	return slices.Contains(strings.Fields(cmdline), want)
}
