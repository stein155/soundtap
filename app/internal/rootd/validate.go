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
var slotParts = map[string][2]string{
	"PING": {"NAND.Kernel-Ping", "NAND.File-System-Ping"},
	"PONG": {"NAND.Kernel-Pong", "NAND.File-System-Pong"},
}

var bootloaderLabels = []string{"NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3", "NAND.U-Boot"}

// NewLayout reads the table (ParseProcMTD's map) against the kernel command line. On a
// NAND boot (soundtap.nandroot=1) the running slot must be known, or no slot is writable:
// guessing is the one unrecoverable mistake (flash.ErrUnknownSlot). A RAM boot runs
// neither slot, and either may be written, as the recovery plan does.
func NewLayout(cmdline string, mtd map[string]flash.Partition) Layout {
	writable := map[string]bool{}
	running, err := flash.SlotFromCmdline(cmdline)
	switch {
	case err == nil:
		for name, labels := range slotParts {
			if name != running.Name {
				writable[labels[0]], writable[labels[1]] = true, true
			}
		}
	case !slices.Contains(strings.Fields(cmdline), "soundtap.nandroot=1"):
		for _, labels := range slotParts {
			writable[labels[0]], writable[labels[1]] = true, true
		}
	}
	l := Layout{parts: map[int]part{}}
	for label, p := range mtd {
		r := roleNone
		switch {
		case slices.Contains(bootloaderLabels, label):
			r = roleBootloader
		case label == "NAND.U-Boot-Env":
			r = roleEnv
		case writable[label] && strings.HasPrefix(label, "NAND.Kernel-"):
			r = roleKernel
		case writable[label]:
			r = roleRootfs
		}
		l.parts[p.Index] = part{label: label, size: p.Size, eraseSize: p.EraseSize, role: r}
	}
	return l
}

// writable returns partition mtd when its role is one of roles.
func (l Layout) writable(mtd int, roles ...role) (part, error) {
	p, ok := l.parts[mtd]
	if !ok {
		return part{}, fmt.Errorf("mtd%d is not in the partition table", mtd)
	}
	if p.role == roleNone || !slices.Contains(roles, p.role) {
		return part{}, fmt.Errorf("mtd%d (%s) may not be written by this request", mtd, p.label)
	}
	if p.size <= 0 || p.eraseSize <= 0 {
		return part{}, fmt.Errorf("mtd%d (%s) has no usable geometry", mtd, p.label)
	}
	return p, nil
}

// checkErase holds flash_erase to whole erase blocks inside the partition.
func (l Layout) checkErase(r Request) error {
	p, err := l.writable(r.MTD, roleBootloader, roleEnv, roleKernel)
	if err != nil {
		return err
	}
	if r.Offset < 0 || r.Blocks < 0 || r.Offset%p.eraseSize != 0 || r.Offset >= p.size {
		return fmt.Errorf("erase of mtd%d at %#x is not a whole erase block inside it", r.MTD, r.Offset)
	}
	if r.Blocks > (p.size-r.Offset)/p.eraseSize {
		return fmt.Errorf("erase of %d blocks at mtd%d+%#x runs past its end", r.Blocks, r.MTD, r.Offset)
	}
	return nil
}

// checkWrite holds nandwrite to a block-aligned start and an input that fits.
func (l Layout) checkWrite(r Request, inputSize int64) error {
	p, err := l.writable(r.MTD, roleBootloader, roleEnv, roleKernel)
	if err != nil {
		return err
	}
	if r.Offset < 0 || r.Offset%p.eraseSize != 0 || r.Offset >= p.size {
		return fmt.Errorf("write to mtd%d at %#x is not at an erase block inside it", r.MTD, r.Offset)
	}
	if inputSize <= 0 || inputSize > p.size-r.Offset {
		return fmt.Errorf("%d bytes do not fit mtd%d+%#x", inputSize, r.MTD, r.Offset)
	}
	return nil
}

// checkBlock holds a one-block dd to a bootloader partition and an input that has that block.
func (l Layout) checkBlock(r Request, inputSize int64) error {
	p, err := l.writable(r.MTD, roleBootloader)
	if err != nil {
		return err
	}
	if r.Size != p.eraseSize {
		return fmt.Errorf("block size %#x is not mtd%d's erase block %#x", r.Size, r.MTD, p.eraseSize)
	}
	if r.Offset < 0 || r.Offset%p.eraseSize != 0 || r.Offset+p.eraseSize > p.size {
		return fmt.Errorf("block write to mtd%d at %#x is not one erase block inside it", r.MTD, r.Offset)
	}
	if r.Skip < 0 || (r.Skip+1)*p.eraseSize > inputSize {
		return fmt.Errorf("block %d is not in a %d byte input", r.Skip, inputSize)
	}
	return nil
}

// blockSize is the erase block of mtd, for dd's bs.
func (l Layout) blockSize(mtd int) int64 { return l.parts[mtd].eraseSize }

// checkFormat holds ubiformat to the rootfs of a slot that is not running.
func (l Layout) checkFormat(r Request) error {
	p, err := l.writable(r.MTD, roleRootfs)
	if err != nil {
		return err
	}
	if r.Size <= 0 || r.Size > p.size {
		return fmt.Errorf("a %d byte image does not fit mtd%d (%s)", r.Size, r.MTD, p.label)
	}
	return nil
}

// checkAttach holds ubiattach to the rootfs of a slot that is not running.
func (l Layout) checkAttach(r Request) error {
	_, err := l.writable(r.MTD, roleRootfs)
	return err
}

// checkEnvCopies holds fw_env.config to the environment's two places: NAND.U-Boot-Env at
// its start, and the redundant copy at ubootenv.RedundantOffset in NAND.U-Boot, each one
// erase block of the partition's own size and an environment size U-Boot reads.
func (l Layout) checkEnvCopies(copies []EnvCopy) error {
	if len(copies) == 0 || len(copies) > 2 {
		return fmt.Errorf("%d environment copies; one or two", len(copies))
	}
	for _, c := range copies {
		p, ok := l.parts[c.MTD]
		if !ok {
			return fmt.Errorf("mtd%d is not in the partition table", c.MTD)
		}
		place := (p.role == roleEnv && c.Offset == 0) ||
			(p.label == "NAND.U-Boot" && c.Offset == ubootenv.RedundantOffset)
		if !place {
			return fmt.Errorf("mtd%d+%#x is not a place of the U-Boot environment", c.MTD, c.Offset)
		}
		if c.Size != ubootenv.Size && c.Size != ubootenv.LegacySize {
			return fmt.Errorf("environment size %#x is neither %#x nor %#x", c.Size, ubootenv.Size, ubootenv.LegacySize)
		}
		if c.EraseSize != p.eraseSize || c.Offset+c.EraseSize > p.size {
			return fmt.Errorf("environment copy at mtd%d+%#x does not match its erase block", c.MTD, c.Offset)
		}
	}
	return nil
}

// CheckEnvScript refuses a fw_setenv script that touches an entry flash.EnvKeys does not
// name, or that is not text.
func CheckEnvScript(script []byte) error {
	if len(script) == 0 {
		return errors.New("empty environment script")
	}
	if bytes.IndexByte(script, 0) >= 0 {
		return errors.New("environment script holds a NUL byte")
	}
	allowed := flash.EnvKeys()
	s := bufio.NewScanner(bytes.NewReader(script))
	s.Buffer(make([]byte, 0, 4096), MaxMessage)
	for s.Scan() {
		line := s.Text()
		if strings.Trim(line, " \t\r") == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// libubootenv reads "name=value", "name value", and "name=" or "name" to delete.
		key := strings.TrimLeft(line, " \t")
		if i := strings.IndexAny(key, "= \t"); i >= 0 {
			key = key[:i]
		}
		if !slices.Contains(allowed, key) {
			return fmt.Errorf("environment entry %q is not one soundtapd writes", key)
		}
	}
	return s.Err()
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

var gpioBanks = []string{gpio0, gpio1, gpio2, gpio3}

// exportableGPIO are the lines soundtapd drives itself and may ask for. Every other line
// is refused: the keypad matrices (internal/hw/keypad.go), the ST20's amplifier fault and
// amplifier lines (internal/hw/amp.go, ampfault.go; model.go), and the panels' chip
// select, reset and data/command lines (oled.PanelLines; gpio_test.go holds the list to them).
var exportableGPIO = []gpioLine{
	// ST10 keypad: strobes gpio1_28, 1_12, 1_13; switch inputs gpio0_23, 0_26, 0_22, 0_27.
	{gpio1, 28}, {gpio1, 12}, {gpio1, 13},
	{gpio0, 23}, {gpio0, 26}, {gpio0, 22}, {gpio0, 27},
	// ST20 keypad: strobes gpio0_28, 0_20, 0_17; switch inputs gpio0_22, 0_23, 0_26, 0_29.
	{gpio0, 28}, {gpio0, 20}, {gpio0, 17}, {gpio0, 29},
	// ST20 amplifier: safe mute gpio1_17, enable gpio1_23, fault gpio1_24.
	{gpio1, 17}, {gpio1, 23}, {gpio1, 24},
	// ST20 panel (oled.st20Pins): chip select gpio2_24, reset gpio2_18, data/command gpio1_27.
	{gpio2, 24}, {gpio2, 18}, {gpio1, 27},
	// The Series III panel (ST20 and ST30 Series III): chip select gpio0_12, reset gpio2_9,
	// data/command gpio2_12.
	{gpio0, 12}, {gpio2, 9}, {gpio2, 12},
	// The SoundTouch Portable's charger (stock taigan_scm gpiodev): charge enable gpio3_8; DC
	// present is gpio0_27, the ST10 keypad input above.
	{gpio3, 8},
}

// CheckHostname holds sethostname to one DNS label, as soundtapd's dhcpHostname makes it.
func CheckHostname(name string) error {
	if name == "" || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return fmt.Errorf("host name %q is not a DNS label", name)
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("host name %q is not a DNS label", name)
		}
	}
	return nil
}
