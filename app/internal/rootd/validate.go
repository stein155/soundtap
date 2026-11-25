// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// role is what a partition is to the NAND contract (CONTRIBUTING.md, NAND and boot contract).
type role int

const (
	roleNone       role = iota // never written: the running slot, mtd10-12, anything unknown
	roleBootloader             // NAND.SPL, its three backups, NAND.U-Boot
	roleEnv                    // NAND.U-Boot-Env
	roleKernel                 // the kernel partition of a slot that is not running
	roleRootfs                 // the rootfs partition of a slot that is not running
)

// part is one partition of the live table.
type part struct {
	label     string
	size      int64
	eraseSize int64
	role      role
}

// Layout is the live partition table read against the NAND contract: which partitions a
// request may write, and how far.
type Layout struct {
	parts map[int]part
}

// slotParts are the kernel and rootfs labels of each slot.
func slotParts() map[string][2]string {
	return map[string][2]string{
		"PING": {"NAND.Kernel-Ping", "NAND.File-System-Ping"},
		"PONG": {"NAND.Kernel-Pong", "NAND.File-System-Pong"},
	}
}

// bootloaderLabels are the boot chain's partitions, mtd0-mtd4.
func bootloaderLabels() []string {
	return []string{"NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3", "NAND.U-Boot"}
}

// The refusals of a request against the layout. A message with values reads them around
// its sentinel, as it always has: soundtapd shows it.
var (
	errNotInTable       = errors.New("is not in the partition table")
	errMayNotWrite      = errors.New("may not be written by this request")
	errNoUsableGeometry = errors.New("has no usable geometry")
	errEraseNotWhole    = errors.New("is not a whole erase block inside it")
	errRunsPastEnd      = errors.New("runs past its end")
	errWriteNotAtBlock  = errors.New("is not at an erase block inside it")
	errDoNotFit         = errors.New("do not fit")
	errNotEraseBlock    = errors.New("is not mtd")
	errNotOneBlock      = errors.New("is not one erase block inside it")
	errBlockNotInInput  = errors.New("is not in a")
	errImageDoesNotFit  = errors.New("byte image does not fit")
	errEnvCopyCount     = errors.New("environment copies; one or two")
	errNotEnvPlace      = errors.New("is not a place of the U-Boot environment")
	errEnvSize          = errors.New("environment size")
	errEnvEraseBlock    = errors.New("does not match its erase block")
	errEmptyScript      = errors.New("empty environment script")
	errScriptNUL        = errors.New("environment script holds a NUL byte")
	errEntryNotOurs     = errors.New("is not one soundtapd writes")
	errNotDNSLabel      = errors.New("is not a DNS label")
)

// scriptBuffer is the scanner's first buffer for an environment script; it grows to
// MaxMessage.
const scriptBuffer = 4096

// NewLayout reads the table (ParseProcMTD's map) against the kernel command line. On a
// NAND boot (soundtap.nandroot=1) the running slot must be known, or no slot is writable:
// guessing is the one unrecoverable mistake (flash.ErrUnknownSlot). A RAM boot runs
// neither slot, and either may be written, as the recovery plan does.
func NewLayout(cmdline string, mtd map[string]flash.Partition) Layout {
	writable := map[string]bool{}

	running, err := flash.SlotFromCmdline(cmdline)
	switch {
	case err == nil:
		for name, labels := range slotParts() {
			if name != running.Name {
				writable[labels[0]], writable[labels[1]] = true, true
			}
		}
	case !slices.Contains(strings.Fields(cmdline), "soundtap.nandroot=1"):
		for _, labels := range slotParts() {
			writable[labels[0]], writable[labels[1]] = true, true
		}
	default: // a NAND boot whose slot is unknown: no slot is writable
	}

	layout := Layout{parts: map[int]part{}}

	for label, partition := range mtd {
		partRole := roleNone

		switch {
		case slices.Contains(bootloaderLabels(), label):
			partRole = roleBootloader
		case label == "NAND.U-Boot-Env":
			partRole = roleEnv
		case writable[label] && strings.HasPrefix(label, "NAND.Kernel-"):
			partRole = roleKernel
		case writable[label]:
			partRole = roleRootfs
		default: // never written
		}

		layout.parts[partition.Index] = part{
			label: label, size: partition.Size, eraseSize: partition.EraseSize, role: partRole,
		}
	}

	return layout
}

// writable returns partition mtd when its role is one of roles.
func (l Layout) writable(mtd int, roles ...role) (part, error) {
	partition, ok := l.parts[mtd]
	if !ok {
		return part{}, fmt.Errorf("mtd%d %w", mtd, errNotInTable)
	}

	if partition.role == roleNone || !slices.Contains(roles, partition.role) {
		return part{}, fmt.Errorf("mtd%d (%s) %w", mtd, partition.label, errMayNotWrite)
	}

	if partition.size <= 0 || partition.eraseSize <= 0 {
		return part{}, fmt.Errorf("mtd%d (%s) %w", mtd, partition.label, errNoUsableGeometry)
	}

	return partition, nil
}

// checkErase holds flash_erase to whole erase blocks inside the partition.
func (l Layout) checkErase(req *Request) error {
	partition, err := l.writable(req.MTD, roleBootloader, roleEnv, roleKernel)
	if err != nil {
		return err
	}

	if req.Offset < 0 || req.Blocks < 0 || req.Offset%partition.eraseSize != 0 || req.Offset >= partition.size {
		return fmt.Errorf("erase of mtd%d at %#x %w", req.MTD, req.Offset, errEraseNotWhole)
	}

	if req.Blocks > (partition.size-req.Offset)/partition.eraseSize {
		return fmt.Errorf("erase of %d blocks at mtd%d+%#x %w", req.Blocks, req.MTD, req.Offset, errRunsPastEnd)
	}

	return nil
}

// checkWrite holds nandwrite to a block-aligned start and an input that fits.
func (l Layout) checkWrite(req *Request, inputSize int64) error {
	partition, err := l.writable(req.MTD, roleBootloader, roleEnv, roleKernel)
	if err != nil {
		return err
	}

	if req.Offset < 0 || req.Offset%partition.eraseSize != 0 || req.Offset >= partition.size {
		return fmt.Errorf("write to mtd%d at %#x %w", req.MTD, req.Offset, errWriteNotAtBlock)
	}

	if inputSize <= 0 || inputSize > partition.size-req.Offset {
		return fmt.Errorf("%d bytes %w mtd%d+%#x", inputSize, errDoNotFit, req.MTD, req.Offset)
	}

	return nil
}

// checkBlock holds a one-block dd to a bootloader partition and an input that has that block.
func (l Layout) checkBlock(req *Request, inputSize int64) error {
	partition, err := l.writable(req.MTD, roleBootloader)
	if err != nil {
		return err
	}

	if req.Size != partition.eraseSize {
		return fmt.Errorf("block size %#x %w%d's erase block %#x",
			req.Size, errNotEraseBlock, req.MTD, partition.eraseSize)
	}

	if req.Offset < 0 || req.Offset%partition.eraseSize != 0 || req.Offset+partition.eraseSize > partition.size {
		return fmt.Errorf("block write to mtd%d at %#x %w", req.MTD, req.Offset, errNotOneBlock)
	}

	if req.Skip < 0 || (req.Skip+1)*partition.eraseSize > inputSize {
		return fmt.Errorf("block %d %w %d byte input", req.Skip, errBlockNotInInput, inputSize)
	}

	return nil
}

// blockSize is the erase block of mtd, for dd's bs.
func (l Layout) blockSize(mtd int) int64 { return l.parts[mtd].eraseSize }

// checkFormat holds ubiformat to the rootfs of a slot that is not running.
func (l Layout) checkFormat(req *Request) error {
	partition, err := l.writable(req.MTD, roleRootfs)
	if err != nil {
		return err
	}

	if req.Size <= 0 || req.Size > partition.size {
		return fmt.Errorf("a %d %w mtd%d (%s)", req.Size, errImageDoesNotFit, req.MTD, partition.label)
	}

	return nil
}

// checkAttach holds ubiattach to the rootfs of a slot that is not running.
func (l Layout) checkAttach(r *Request) error {
	_, err := l.writable(r.MTD, roleRootfs)

	return err
}

// checkEnvCopies holds fw_env.config to the environment's two places: NAND.U-Boot-Env at
// its start, and the redundant copy at ubootenv.RedundantOffset in NAND.U-Boot, each one
// erase block of the partition's own size and an environment size U-Boot reads.
func (l Layout) checkEnvCopies(copies []EnvCopy) error {
	if len(copies) == 0 || len(copies) > 2 {
		return fmt.Errorf("%d %w", len(copies), errEnvCopyCount)
	}

	for _, envCopy := range copies {
		partition, ok := l.parts[envCopy.MTD]
		if !ok {
			return fmt.Errorf("mtd%d %w", envCopy.MTD, errNotInTable)
		}

		place := (partition.role == roleEnv && envCopy.Offset == 0) ||
			(partition.label == "NAND.U-Boot" && envCopy.Offset == ubootenv.RedundantOffset)
		if !place {
			return fmt.Errorf("mtd%d+%#x %w", envCopy.MTD, envCopy.Offset, errNotEnvPlace)
		}

		if envCopy.Size != ubootenv.Size && envCopy.Size != ubootenv.LegacySize {
			return fmt.Errorf("%w %#x is neither %#x nor %#x",
				errEnvSize, envCopy.Size, ubootenv.Size, ubootenv.LegacySize)
		}

		if envCopy.EraseSize != partition.eraseSize || envCopy.Offset+envCopy.EraseSize > partition.size {
			return fmt.Errorf("environment copy at mtd%d+%#x %w", envCopy.MTD, envCopy.Offset, errEnvEraseBlock)
		}
	}

	return nil
}

// CheckEnvScript refuses a fw_setenv script that touches an entry flash.EnvKeys does not
// name, or that is not text.
func CheckEnvScript(script []byte) error {
	if len(script) == 0 {
		return errEmptyScript
	}

	if bytes.IndexByte(script, 0) >= 0 {
		return errScriptNUL
	}

	allowed := flash.EnvKeys()
	lines := bufio.NewScanner(bytes.NewReader(script))
	lines.Buffer(make([]byte, 0, scriptBuffer), MaxMessage)

	for lines.Scan() {
		line := lines.Text()
		if strings.Trim(line, " \t\r") == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// libubootenv reads "name=value", "name value", and "name=" or "name" to delete.
		key := strings.TrimLeft(line, " \t")
		if i := strings.IndexAny(key, "= \t"); i >= 0 {
			key = key[:i]
		}

		if !slices.Contains(allowed, key) {
			return fmt.Errorf("environment entry %q %w", key, errEntryNotOurs)
		}
	}

	err := lines.Err()
	if err != nil {
		return fmt.Errorf("reading the environment script: %w", err)
	}

	return nil
}

// gpioLine is one line of the SoC's four GPIO banks, by the bank's address.
type gpioLine struct {
	bank string
	line int
}

// The AM335x GPIO controllers by register address, as sysfs names them.
const (
	gpio0 = "44e07000"
	gpio1 = "4804c000"
	gpio2 = "481ac000"
	gpio3 = "481ae000"
)

// gpioBanks are the four SoC GPIO banks.
func gpioBanks() []string { return []string{gpio0, gpio1, gpio2, gpio3} }

// exportableGPIO are the lines soundtapd drives itself and may ask for. Every other line
// is refused: the keypad matrices (internal/hw/keypad.go), the ST20's amplifier fault and
// amplifier lines (internal/hw/amp.go, ampfault.go; model.go), and the panels' chip
// select, reset and data/command lines (oled.PanelLines; gpio_test.go holds the list to them).
func exportableGPIO() []gpioLine {
	return []gpioLine{
		// ST10 keypad: strobes gpio1_28, 1_12, 1_13; switch inputs gpio0_23, 0_26, 0_22, 0_27.
		{gpio1, 28},
		{gpio1, 12},
		{gpio1, 13},
		{gpio0, 23},
		{gpio0, 26},
		{gpio0, 22},
		{gpio0, 27},
		// ST20 keypad: strobes gpio0_28, 0_20, 0_17; switch inputs gpio0_22, 0_23, 0_26, 0_29.
		{gpio0, 28},
		{gpio0, 20},
		{gpio0, 17},
		{gpio0, 29},
		// ST20 amplifier: safe mute gpio1_17, enable gpio1_23, fault gpio1_24.
		{gpio1, 17},
		{gpio1, 23},
		{gpio1, 24},
		// ST20 panel (oled.st20Pins): chip select gpio2_24, reset gpio2_18, data/command gpio1_27.
		{gpio2, 24},
		{gpio2, 18},
		{gpio1, 27},
		// The Series III panel (ST20 and ST30 Series III): chip select gpio0_12, reset gpio2_9,
		// data/command gpio2_12.
		{gpio0, 12},
		{gpio2, 9},
		{gpio2, 12},
		// The SoundTouch Portable's charger (stock taigan_scm gpiodev): charge enable gpio3_8; DC
		// present is gpio0_27, the ST10 keypad input above.
		{gpio3, 8},
	}
}

// CheckHostname holds sethostname to one DNS label, as soundtapd's dhcpHostname makes it.
func CheckHostname(name string) error {
	if name == "" || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return fmt.Errorf("host name %q %w", name, errNotDNSLabel)
	}

	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("host name %q %w", name, errNotDNSLabel)
		}
	}

	return nil
}
