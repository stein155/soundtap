// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package flash installs a firmware package into NAND, the way Bose's own
// ARMUpdate does it.
//
// What the stock installer does, read out of the ARMUpdate binary in
// the stock ST10 STU (its module payload is a static ARM ELF):
//
//   - It asks /proc/cmdline which slot is running: `ubi.mtd=8` is PING, `9` is
//     PONG ("SCM_get_current_boot", "Booted from PING/PONG").
//   - It writes the *alternate* slot, never the running one — "primary: %s dev:
//     %s /dev/mtd6" / "alternate: %s dev: %s /dev/mtd7", then "Erasing Kernel
//     Pong partition", "Writing to File System Ping", and so on for either side.
//   - It finds those partitions by name in /proc/mtd, not by hardcoded number.
//   - It juggles the five signature bytes at offset 32 of the uImage — Linux,
//     Linub, Linem, Linpv, Linxt — and reports "After operation, Primary sig is
//     %s, Alternate %s". U-Boot's `fixsig` turns Linub back into Linux at boot,
//     and U-Boot's own updater invalidates a slot by writing Linem.
//   - It never touches the U-Boot environment: no `bootcmd`, `setenv` or
//     `fw_setenv` string appears in ARMUpdate, SoftwareUpdateInstaller,
//     UbootUpdate or SCMReset_Update. Which slot boots is decided by those
//     signature bytes, which is why Bose's installer does not need fw_setenv.
//     Our U-Boot does read the environment, and bootselect.go writes it with
//     libubootenv's fw_setenv (ADR 0010).
//
// So the order here is chosen so that every failure lands on the running slot:
// erase the alternate kernel first (stock's U-Boot boots no slot without one), then
// write the rootfs, and only write the kernel and select the slot -- the last steps --
// once the whole package has streamed through and every CRC in it has checked out.
// Under our U-Boot the kernel comes out of the rootfs, so what keeps a half-written
// slot from booting there is the selection in the environment (bootselect.go). An
// update interrupted at any point leaves the speaker booting what it booted before.
package flash

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stein155/soundtap/app/internal/safego"
)

// Partition names as the stock layout spells them in /proc/mtd. ARMUpdate reads
// the same table and matches the same strings.
const (
	kernelPing = "NAND.Kernel-Ping"
	kernelPong = "NAND.Kernel-Pong"
	rootfsPing = "NAND.File-System-Ping"
	rootfsPong = "NAND.File-System-Pong"
	envPart    = "NAND.U-Boot-Env"
)

// Slot is one half of the A/B pair.
type Slot struct {
	Name       string // "PING" or "PONG"
	KernelMTD  int
	RootfsMTD  int
	RootfsName string

	// Capacities read from the live table, so a package can be turned away for
	// not fitting before anything is erased rather than by a write that fails
	// halfway.
	KernelSize int64
	RootfsSize int64
	// RootfsEraseSize is the eraseblock size of the rootfs partition. ubiformat
	// refuses an image that is not a whole number of them, and a package's module
	// size is the only place to find out which of its two candidate lengths is one.
	RootfsEraseSize int64
}

// Partition is one line of /proc/mtd: which device, and how big.
type Partition struct {
	Index     int
	Size      int64
	EraseSize int64
}

// Target is where an install will go: the slot that is not running.
type Target struct {
	Running              Slot
	Alternate            Slot
	BootloaderSizes      map[int]int64 // verified NAND.SPL{,.backup1-3} and NAND.U-Boot geometry
	BootloaderEraseSizes map[int]int64

	// EnvMTD is NAND.U-Boot-Env, the partition that actually decides which slot
	// this board boots -- see internal/ubootenv. -1 when the table has no such
	// partition, which means the slot cannot be selected and an install that
	// needs to select one must refuse rather than write and hope.
	EnvMTD       int
	EnvSize      int64
	EnvEraseSize int64

	// PersistentName is how this image spells the persistent-storage partition.
	// Stock misspells it "Persistant" on some builds, and a marker that says
	// something the table does not is a partition UBI will never find.
	PersistentName string

	// BootCount is set on a NAND boot: our U-Boot keeps a boot count in RTC SCRATCH2
	// and runs altbootcmd once it passes bootlimit (ADR 0010).
	BootCount bool

	// EnvRedundant is set when the U-Boot that booted this system reads the redundant
	// environment (ADR 0010): two 32 KiB copies, the primary in NAND.U-Boot-Env and the
	// other at ubootenv.RedundantOffset in NAND.U-Boot. It says so on the kernel command
	// line (EnvRedundantMarker). Unset, the environment is the single 8 KiB copy Bose's
	// U-Boot reads, which is also what an older U-Boot of ours reads.
	EnvRedundant bool
}

// EnvRedundantMarker is what our U-Boot adds to every kernel command line it hands over
// when it reads the redundant environment (board_fdt_chosen_bootargs in
// os/bootloader/uboot/uboot-2026.07-usb-recovery.patch). An environment is only written in
// the format the U-Boot on the board reads: a U-Boot that reads one copy of 8 KiB falls
// back to its built-in default on two copies of 32 KiB.
const EnvRedundantMarker = "soundtap.env=redundant"

// The documented NAND layout's slot partitions: mtd6/mtd7 the kernels, mtd8/mtd9 the
// root filesystems. plan holds the live table to these numbers.
const (
	kernelPingMTD = 6
	kernelPongMTD = 7
	rootfsPingMTD = 8
	rootfsPongMTD = 9
)

//nolint:gochecknoglobals // the two fixed slots of the NAND layout, used as values throughout.
var (
	ping = Slot{
		Name: "PING", KernelMTD: kernelPingMTD, RootfsMTD: rootfsPingMTD, RootfsName: rootfsPing,
		KernelSize: 0, RootfsSize: 0, RootfsEraseSize: 0,
	}
	pong = Slot{
		Name: "PONG", KernelMTD: kernelPongMTD, RootfsMTD: rootfsPongMTD, RootfsName: rootfsPong,
		KernelSize: 0, RootfsSize: 0, RootfsEraseSize: 0,
	}
)

// The ways a partition table is not the layout this package writes. A message with values
// reads them around its sentinel, as it always has.
var (
	errMissingMTD        = errors.New("is missing from /proc/mtd")
	errExpectedMTD       = errors.New("expected")
	errNoUsableSize      = errors.New("has no usable size")
	errNoEraseSize       = errors.New("has no erase size that divides it")
	errRedundantEnvPlace = errors.New("the redundant environment copy is not at NAND.U-Boot's last block")
	errBootloaderNotMTD  = errors.New("bootloader partition is not")
	errNoGeometry        = errors.New("has no usable geometry")
	errNoPartitions      = errors.New("no partitions in /proc/mtd")
	errKernelReadback    = errors.New("kernel readback verification failed on")
	errNoBootFiles       = errors.New("no boot files known for variant")
	errNothingWritten    = errors.New("cannot verify the written root filesystem: no rootfs was written")
	errNotDeviceTree     = errors.New("is not a whole device tree")
)

// bootloaderPartitions are the boot chain's partitions, in mtd order: mtd0-mtd4.
func bootloaderPartitions() []string {
	return []string{"NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3", "NAND.U-Boot"}
}

// procMTDNumbers is size and erasesize, the two hex numbers before a /proc/mtd name.
const procMTDNumbers = 2

// The flattened device tree header: its magic and, at 4, its total size.
const (
	fdtMagic      = 0xd00dfeed
	fdtHeaderSize = 8
)

// ubiVIDOffset is where ubiformat -s 2048 put the VID headers.
const ubiVIDOffset = "2048"

// ErrUnknownSlot means the running slot could not be determined. Guessing is not
// an option: writing the slot we are running from is the one unrecoverable
// mistake this package can make.
var ErrUnknownSlot = errors.New("cannot tell which NAND slot is running")

// SlotFromCmdline reads the running slot out of a /proc/cmdline string.
//
// There is more than one ubi.mtd= on this board's command line and that is not a
// mistake: the first attaches the root slot as ubi0, the second attaches
// NAND.Persistent-Storage as ubi1 because the image has no mtd-utils to do it
// afterwards. This used to refuse the moment it met the second one -- "ubi.mtd=
// NAND.Persistent-Storage is neither slot" -- which meant the speaker could not
// say which slot it was running and every install was turned away. Found on the
// board, in /api/debug, not in a test.
//
// So: the root slot is the first ubi.mtd=, because ubi0 is whatever attaches
// first. Later attachments are other volumes and are not the root. What stays
// strict is everything that could hide a wrong answer -- a first entry that names
// neither slot, a later entry that names a boot slot too, no entry at all.
func SlotFromCmdline(cmdline string) (Slot, error) {
	var running Slot

	first := true

	for field := range strings.FieldsSeq(cmdline) {
		value, ok := strings.CutPrefix(field, "ubi.mtd=")
		if !ok {
			continue
		}

		mtd, offset, hasOffset := strings.Cut(value, ",")
		if hasOffset {
			_, err := strconv.ParseUint(offset, 10, 32)
			if err != nil {
				return Slot{}, fmt.Errorf("%w: invalid ubi.mtd offset %q", ErrUnknownSlot, offset)
			}
		}

		var slot Slot

		switch mtd {
		case "8", rootfsPing:
			slot = ping
		case "9", rootfsPong:
			slot = pong
		default:
			if first {
				return Slot{}, fmt.Errorf("%w: the root ubi.mtd=%s is neither slot", ErrUnknownSlot, mtd)
			}

			first = false

			continue
		}

		if !first {
			return Slot{}, fmt.Errorf("%w: %s is attached after the root volume", ErrUnknownSlot, mtd)
		}

		first = false
		running = slot
	}

	if running.Name == "" {
		return Slot{}, fmt.Errorf("%w: no ubi.mtd= on the kernel command line", ErrUnknownSlot)
	}

	return running, nil
}

// Plan reports which slot is running and which one an install would write.
func Plan(cmdline string, mtd map[string]Partition) (Target, error) {
	running, err := SlotFromCmdline(cmdline)
	if err != nil {
		return Target{}, err
	}

	target, err := planFor(running, mtd)
	if err == nil {
		target.EnvRedundant = slices.Contains(strings.Fields(cmdline), EnvRedundantMarker)
	}

	return target, err
}

// RecoveryPlan is for a RAM recovery system, which is not running either NAND
// slot. The caller must restrict this to hardware with an independent recovery
// path; normal NAND boots must continue through Plan.
//
// selected is the slot the environment boots for sure (ProvenSlot), which stands in for the
// running one: the install writes the other, so the slot that last booted is never the one
// erased, nor, while a selection is unconfirmed, the fallback U-Boot would return to.
// Unknown ("") counts as PING.
func RecoveryPlan(mtd map[string]Partition, selected string) (Target, error) {
	if selected == pong.Name {
		return planFor(pong, mtd)
	}

	return planFor(ping, mtd)
}

// planFor is the plan for a board running from running.
func planFor(running Slot, mtd map[string]Partition) (Target, error) {
	alt := pong
	if running.Name == pong.Name {
		alt = ping
	}

	err := checkSlotPartitions(mtd)
	if err != nil {
		return Target{}, err
	}

	sized := func(slot Slot) Slot {
		if slot.Name == ping.Name {
			slot.KernelSize, slot.RootfsSize = mtd[kernelPing].Size, mtd[rootfsPing].Size
			slot.RootfsEraseSize = mtd[rootfsPing].EraseSize
		} else {
			slot.KernelSize, slot.RootfsSize = mtd[kernelPong].Size, mtd[rootfsPong].Size
			slot.RootfsEraseSize = mtd[rootfsPong].EraseSize
		}

		return slot
	}

	bootloaderSizes, bootloaderEraseSizes, err := bootloaderGeometry(mtd)
	if err != nil {
		return Target{}, err
	}

	target := Target{
		Running: sized(running), Alternate: sized(alt),
		BootloaderSizes: bootloaderSizes, BootloaderEraseSizes: bootloaderEraseSizes,
		EnvMTD: -1, EnvSize: 0, EnvEraseSize: 0, PersistentName: "",
		// Every plan ends in our U-Boot, which counts boots, including a RAM recovery's.
		BootCount:    true,
		EnvRedundant: false,
	}

	for _, name := range []string{"NAND.Persistent-Storage", "NAND.Persistant-Storage"} {
		if _, ok := mtd[name]; ok {
			target.PersistentName = name

			break
		}
	}

	if env, ok := mtd[envPart]; ok {
		if env.Size <= 0 || env.EraseSize <= 0 {
			return Target{}, fmt.Errorf("%s %w", envPart, errNoGeometry)
		}

		target.EnvMTD, target.EnvSize, target.EnvEraseSize = env.Index, env.Size, env.EraseSize
	}

	return target, nil
}

// checkSlotPartitions holds the live table to the slots' documented layout. The numbers in
// ping and pong are the documented layout; the live table is what we actually write to. A
// mismatch means this is not the layout we think it is.
func checkSlotPartitions(mtd map[string]Partition) error {
	for name, want := range map[string]int{
		kernelPing: ping.KernelMTD, kernelPong: pong.KernelMTD,
		rootfsPing: ping.RootfsMTD, rootfsPong: pong.RootfsMTD,
	} {
		got, ok := mtd[name]
		if !ok {
			return fmt.Errorf("%s %w", name, errMissingMTD)
		}

		if got.Index != want {
			return fmt.Errorf("%s is mtd%d, %w mtd%d", name, got.Index, errExpectedMTD, want)
		}

		if got.Size <= 0 {
			return fmt.Errorf("%s %w", name, errNoUsableSize)
		}
		// A rootfs's erase size is known before anything is erased: its read-back needs it,
		// and an install that can only fail must not wipe the other slot first.
		if (name == rootfsPing || name == rootfsPong) && (got.EraseSize <= 0 || got.Size%got.EraseSize != 0) {
			return fmt.Errorf("%s %w", name, errNoEraseSize)
		}
	}

	return nil
}

// bootloaderGeometry is the size and erase size of each boot chain partition the table
// has, by mtd number; one in another place, or of no size, is refused.
func bootloaderGeometry(mtd map[string]Partition) (map[int]int64, map[int]int64, error) {
	sizes := map[int]int64{}
	eraseSizes := map[int]int64{}

	for want, name := range bootloaderPartitions() {
		got, ok := mtd[name]
		if !ok {
			continue
		}

		if got.Index != want || got.Size <= 0 || got.EraseSize <= 0 {
			return nil, nil, fmt.Errorf("%s %w mtd%d", name, errBootloaderNotMTD, want)
		}

		sizes[want] = got.Size
		eraseSizes[want] = got.EraseSize
	}

	return sizes, eraseSizes, nil
}

// WLABootloaderGeometry rejects an unfamiliar boot chain before WLA writes it.
func (t *Target) WLABootloaderGeometry() bool {
	return t.BootloaderSizes[0] == 0x20000 && t.BootloaderSizes[1] == 0x20000 &&
		t.BootloaderSizes[2] == 0x20000 && t.BootloaderSizes[3] == 0x20000 &&
		t.BootloaderSizes[4] == 0x1e0000 &&
		t.BootloaderEraseSizes[0] == 0x20000 && t.BootloaderEraseSizes[1] == 0x20000 &&
		t.BootloaderEraseSizes[2] == 0x20000 && t.BootloaderEraseSizes[3] == 0x20000 &&
		t.BootloaderEraseSizes[4] == 0x20000
}

// Fits reports whether a payload of n bytes can be written to a partition of the
// given size. An unknown capacity must refuse: otherwise a write can fail halfway
// through after its alternate kernel was erased.
func Fits(n, size int64) bool { return size > 0 && n <= size }

// ParseProcMTD maps partition names to their device number and size.
func ParseProcMTD(r io.Reader) (map[string]Partition, error) {
	out := map[string]Partition{}

	lines := bufio.NewScanner(r)
	for lines.Scan() {
		line := lines.Text()

		dev, rest, ok := strings.Cut(line, ":")
		if !ok || !strings.HasPrefix(dev, "mtd") {
			continue
		}

		index, err := strconv.Atoi(strings.TrimPrefix(dev, "mtd"))
		if err != nil {
			continue
		}

		start := strings.Index(rest, `"`)

		end := strings.LastIndex(rest, `"`)
		if start < 0 || end <= start {
			continue
		}
		// "mtd6: 00500000 00020000 \"NAND.Kernel-Ping\"" — size and erasesize are hex.
		// A size that does not parse stays 0, which is unknown capacity: Fits refuses
		// every write to it, so a garbled line can stop an install but never overrun.
		var size, eraseSize int64
		if fields := strings.Fields(rest[:start]); len(fields) >= procMTDNumbers {
			size, _ = strconv.ParseInt(fields[0], 16, 64)
			eraseSize, _ = strconv.ParseInt(fields[1], 16, 64)
		}

		out[rest[start+1:end]] = Partition{Index: index, Size: size, EraseSize: eraseSize}
	}

	err := lines.Err()
	if err != nil {
		return nil, fmt.Errorf("reading /proc/mtd: %w", err)
	}

	if len(out) == 0 {
		return nil, errNoPartitions
	}

	return out, nil
}

// Command starts one flash command with an optional stdin stream. Real installs
// shell out to the mtd-utils binaries already in the image. Tests supply their own and
// assert on what would have happened.
type Command func(stdin io.Reader, name string, args ...string) error

// Installer writes a package into the alternate slot.
//
// Nothing is staged whole. There is nowhere to put 86 MB on this box, which is
// also why the stock installer streams its payload through a FIFO
// (/mnt/update/scm_swup_data, straight into the NAND writer) rather than saving
// it. The rootfs is piped into ubiformat as it arrives off the network; only the
// 3 MB kernel is held, because it must not be written until the whole package
// has proven itself.
type Installer struct {
	Target Target
	Run    Command
	// Open reads a UBI volume of the written slot back, streaming; nil is os.Open.
	Open func(name string) (io.ReadCloser, error)

	// written is the per-LEB CRCs of the rootfs image StartRootfs handed to ubiformat, which
	// VerifyRootfs holds the slot to.
	written *rootfsDigest
}

// EraseAlternateKernel invalidates the slot for stock's U-Boot before anything else is
// written: it boots no slot whose kernel partition is erased. Our U-Boot boots the slot
// the environment selects, which an install changes last.
func (in *Installer) EraseAlternateKernel() error {
	return in.Run(nil, "flash_erase", in.dev(in.Target.Alternate.KernelMTD), "0", "0")
}

// EraseAlternateRootfs erases the alternate slot's root filesystem whole. Under our U-Boot
// the kernel comes out of the root filesystem, so a written slot boots once something
// selects it, or once a lost environment leaves U-Boot its built-in bootcmd: a package
// that turns out not to be the one its signature names must not stay there.
func (in *Installer) EraseAlternateRootfs() error {
	return in.Run(nil, "flash_erase", in.dev(in.Target.Alternate.RootfsMTD), "0", "0")
}

// StartRootfs begins writing the UBI image and returns the writer to feed it and
// a function that waits for the write to finish. ubiformat is what the stock
// geometry wants: subpage size 2048, and it lays out the volume table and erase
// counters itself rather than leaving a raw dump.
//
// size is required, not a nicety: reading an image from stdin, ubiformat refuses
// to start without -S because it sizes its work from it up front.
//
// What ubiformat accepts is also checksummed, LEB by LEB, as it passes; VerifyRootfs
// reads the written volumes back against that. The writer has CloseWithError, as a pipe
// does, so a failed scan fails ubiformat's read instead of ending the image early.
func (in *Installer) StartRootfs(size int64) (io.WriteCloser, func() error) {
	alt := in.Target.Alternate
	in.written = newRootfsDigest(alt.RootfsEraseSize, alt.RootfsSize)
	ubiformatIn, stream := io.Pipe()
	done := make(chan error, 1)

	safego.Go("flash.ubiformat", func() {
		done <- in.Run(ubiformatIn, "ubiformat", in.dev(in.Target.Alternate.RootfsMTD),
			"-f", "-", "-S", strconv.FormatInt(size, 10), "-s", "2048", "-y")
		// Whatever happens to ubiformat, the producer must not block forever on a
		// pipe nobody is reading any more.
		_ = ubiformatIn.CloseWithError(io.ErrClosedPipe)
	})

	return &rootfsStream{pw: stream, digest: in.written}, func() error { return <-done }
}

// WriteKernel installs the uImage last, which is what makes the new slot
// bootable. The image already carries Bose's "Linub" signature, and fixsig turns
// it back into "Linux" while booting. That used to mean Bose's fixsig, in Bose's
// bootloader. It is ours now -- cmd/fixsig.c, added because replacing their
// bootloader would otherwise have made every slot their updater writes
// unbootable. Leaving the marker in place is deliberate: it is what tells the
// bootloader this image came from their packaging.
//
// The write is read back. nandwrite can return 0 while a block failed to take,
// and this is the step that makes the slot bootable: an unverified kernel is a
// slot the board will jump into and hang in. A mismatch is reported before the
// environment is touched, so the speaker keeps booting what it boots now.
func (in *Installer) WriteKernel(read Reader, path string) error {
	image, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("staged kernel: %w", err)
	}

	dev := in.dev(in.Target.Alternate.KernelMTD)

	err = in.Run(nil, "flash_erase", dev, "0", "0")
	if err != nil {
		return err
	}

	err = in.Run(nil, "nandwrite", "-p", dev, path)
	if err != nil {
		return err
	}

	back, err := read("nanddump", "--omitoob", "--bb=skipbad", "-l", strconv.Itoa(len(image)), dev)
	if err != nil {
		return fmt.Errorf("reading the kernel back: %w", err)
	}

	if len(back) < len(image) || !bytes.Equal(back[:len(image)], image) {
		return fmt.Errorf("%w %s", errKernelReadback, dev)
	}

	return nil
}

// ReadCmdline is the live /proc/cmdline.
func ReadCmdline() (string, error) {
	cmdline, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return "", fmt.Errorf("reading the kernel command line: %w", err)
	}

	return string(cmdline), nil
}

// ReadMTD is the live /proc/mtd.
func ReadMTD() (map[string]Partition, error) {
	table, err := os.Open("/proc/mtd")
	if err != nil {
		return nil, fmt.Errorf("reading /proc/mtd: %w", err)
	}
	defer table.Close()

	return ParseProcMTD(table)
}

// VerifyDir is where VerifyRootfs mounts the written slot. soundtapd points it at
// soundtap-root's own directory when that helper mounts it (ADR 0066): a mount point in a
// directory soundtapd may write is one it could swap for a link.
//
//nolint:gochecknoglobals // soundtapd sets it once at start (cmd/soundtapd/roothelper.go); rootd reads it.
var VerifyDir = "/run/soundtap/verify-rootfs"

// verifyUBI is the UBI device number the written slot is attached as for VerifyRootfs;
// ubi0 is the running root and ubi1 the persistent volume.
const verifyUBI = "7"

// VerifyRootfs reads the freshly written root filesystem back before the slot can be
// selected. ubiformat's exit status says the write was issued, not that what landed reads.
//
// It attaches the alternate slot and reads every volume StartRootfs wrote back in full,
// against the CRC of each LEB taken as the image streamed in: a block that did not take, an
// uncorrectable bitflip or a short volume fails here, as it does in the stock installer
// and our U-Boot's USB recovery, which also read back every block. Then it mounts the slot
// read-only the way U-Boot will use it and checks the kernel (header and data CRC) and the
// device tree that slot_boot loads from /boot, and that /init is there.
func (in *Installer) VerifyRootfs(read Reader, variant string) error {
	files, ok := recipeModels()[variant]
	if !ok {
		return fmt.Errorf("%w %q", errNoBootFiles, variant)
	}

	if in.written == nil {
		return errNothingWritten
	}

	dir := VerifyDir
	// -O 2048: ubiformat -s 2048 put the VID headers there; without it the attach fails.
	err := in.Run(nil, "ubiattach", "-m", strconv.Itoa(in.Target.Alternate.RootfsMTD), "-d", verifyUBI,
		"-O", ubiVIDOffset)
	if err != nil {
		return fmt.Errorf("attaching the written root filesystem: %w", err)
	}
	// The slot was read back above; a detach that fails changes nothing about it.
	defer func() { _ = in.Run(nil, "ubidetach", "-d", verifyUBI) }()

	open := in.Open
	if open == nil {
		open = func(name string) (io.ReadCloser, error) { return os.Open(name) }
	}

	err = in.written.verify(open)
	if err != nil {
		return err
	}

	err = in.Run(nil, "mkdir", "-p", dir)
	if err != nil {
		return err
	}

	err = in.Run(nil, "mount", "-t", "ubifs", "-o", "ro", "ubi"+verifyUBI+":rootfs", dir)
	if err != nil {
		return fmt.Errorf("mounting the written root filesystem: %w", err)
	}
	// What was to be read is read; an unmount that fails changes nothing about it.
	defer func() { _ = in.Run(nil, "umount", dir) }()

	kernel, err := read("cat", dir+"/boot/"+files[1])
	if err != nil {
		return fmt.Errorf("reading /boot/%s back: %w", files[1], err)
	}

	err = ValidateKernel(kernel)
	if err != nil {
		return fmt.Errorf("/boot/%s: %w", files[1], err)
	}

	dtb, err := read("cat", dir+"/boot/"+files[0])
	if err != nil {
		return fmt.Errorf("reading /boot/%s back: %w", files[0], err)
	}

	if len(dtb) < fdtHeaderSize || binary.BigEndian.Uint32(dtb) != fdtMagic ||
		int(binary.BigEndian.Uint32(dtb[4:])) != len(dtb) {
		return fmt.Errorf("/boot/%s %w", files[0], errNotDeviceTree)
	}

	err = in.Run(nil, "test", "-x", dir+"/init")
	if err != nil {
		return fmt.Errorf("the written root filesystem has no executable /init: %w", err)
	}

	return nil
}

func (*Installer) dev(mtd int) string { return "/dev/mtd" + strconv.Itoa(mtd) }
