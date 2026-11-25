// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/stu"
	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// SecondUBootOffset is where in mtd4 the spare U-Boot lives: block 8 of 15, so it can be
// erased and written with the first copy untouched. The SPL finds it at this offset plus
// the start of mtd4, CONFIG_SYS_NAND_U_BOOT_OFFS_REDUND.
const SecondUBootOffset = 0x100000

// spareEnd is where the spare's half of mtd4 ends: block 14, the last, holds the redundant
// U-Boot environment (ubootenv.RedundantOffset), which no U-Boot write may erase.
const spareEnd = ubootenv.RedundantOffset

// maxUBootSize is what the smaller half of mtd4 holds: blocks 8-13, 0xc0000 bytes.
const maxUBootSize = spareEnd - SecondUBootOffset

// maxSPLSize is an SPL partition, mtd0-mtd3: 128 KiB.
const maxSPLSize = 128 << 10

// ubootMTD is the partition that holds both U-Boot copies and the redundant environment.
const ubootMTD = 4

// The legacy uImage header u-boot.img starts with: 64 bytes, its own CRC (ih_hcrc) at 4,
// the image size (ih_size) at 12 and the image's CRC (ih_dcrc) at 24.
const (
	uImageHeaderSize = 64
	uImageHCRCOff    = 4
	uImageSizeOff    = 12
	uImageDCRCOff    = 24
)

// The TI image the AM335x mask ROM loads (ValidateAM335xSPL): the configuration header
// TOC, whose first word is the offset of its first item, then at 0x200 the GP header of
// image size and load address, then the SPL's first instruction.
const (
	splTOCFirstItem = 0x40
	splGPHeader     = 0x200
	splLoadAddrOff  = 0x204
	splEntryOff     = 0x208
	splMinSize      = 0x20c
	am335xSRAMLoad  = 0x402f0400
	armOpcodeShift  = 24
	armBranch       = 0xea // b
	armBranchLink   = 0xeb // bl
)

// Why a boot chain is refused or a write did not take. A message with values reads them
// around its sentinel, as it always has.
var (
	errNoBootChain      = errors.New("the package carries no SPL and U-Boot to restore the boot chain with")
	errSPLTooBig        = errors.New("MLO does not fit a 128 KiB SPL partition")
	errUBootTooBig      = errors.New("u-boot.img does not fit twice in its NAND partition")
	errIdentityMismatch = errors.New("staged payload identity mismatch")
	errUImageHeader     = errors.New("u-boot.img has invalid legacy uImage header")
	errUImageHeaderCRC  = errors.New("u-boot.img has invalid legacy uImage header CRC")
	errUBootNeeds       = errors.New("U-Boot needs")
	errReadback         = errors.New("readback verification failed")
	errUBootLost        = errors.New("no longer holds U-Boot after the boot chain write")
	errSPLShort         = errors.New("is too short to hold a configuration header")
	errSPLNoHeader      = errors.New("has no NAND configuration header")
	errSPLNotSRAM       = errors.New("not AM335x SRAM at 0x402f0400")
	errSPLModuleHolds   = errors.New("but the module holds")
	errSPLNoBranch      = errors.New("which is not a branch to the reset handler")
)

// VerifyBootloaders validates exact streamed bytes before touching mtd0-mtd4.
func VerifyBootloaders(pkg *stu.Package, model, mloPath, ubootPath string) error {
	mlo, uboot, ok := BootloaderModules(pkg, model)
	if !ok {
		return errNoBootChain
	}

	if mlo.Size <= 0 || mlo.Size > maxSPLSize {
		return errSPLTooBig
	}

	if uboot.Size <= 0 || uboot.Size > maxUBootSize {
		return errUBootTooBig
	}

	for _, item := range []struct {
		module stu.Module
		path   string
	}{{mlo, mloPath}, {uboot, ubootPath}} {
		image, err := os.ReadFile(item.path)
		if err != nil || int64(len(image)) != item.module.Size {
			return fmt.Errorf("%s %w", item.module.Name, errIdentityMismatch)
		}

		if item.module.Name == mlo.Name {
			err = ValidateAM335xSPL(image)
			if err != nil {
				return fmt.Errorf("%s %w", mlo.Name, err)
			}

			continue
		}

		err = validateLegacyUImage(image)
		if err != nil {
			return err
		}
	}

	return nil
}

// validateLegacyUImage checks u-boot.img's legacy uImage header: the magic, an image size
// within the file, the image's CRC and the header's own.
func validateLegacyUImage(image []byte) error {
	if len(image) < uImageHeaderSize || binary.BigEndian.Uint32(image) != uImageMagic {
		return errUImageHeader
	}

	size := binary.BigEndian.Uint32(image[uImageSizeOff:])
	if uint64(size)+uImageHeaderSize > uint64(len(image)) {
		return errUImageHeader
	}

	body := image[uImageHeaderSize : uImageHeaderSize+size]
	if crc32.ChecksumIEEE(body) != binary.BigEndian.Uint32(image[uImageDCRCOff:]) {
		return errUImageHeader
	}

	header := append([]byte(nil), image[:uImageHeaderSize]...)
	binary.BigEndian.PutUint32(header[uImageHCRCOff:], 0)

	if crc32.ChecksumIEEE(header) != binary.BigEndian.Uint32(image[uImageHCRCOff:]) {
		return errUImageHeaderCRC
	}

	return nil
}

// The staged images a boot slot can be written from.
const (
	stagedSPL   = "spl"
	stagedUBoot = "uboot"
)

// bootSlot is one place in NAND that holds a staged image.
type bootSlot struct {
	mtd, offset int
	name        string // stagedSPL or stagedUBoot
}

// bootSlots is the write order, and it is what survives a power cut (ADR 0009). The spare
// U-Boot goes first and the first copy last, so the first copy is only ever incomplete
// under a new SPL, which falls back to the spare. mtd0 follows its backups: the ROM moves
// on to them when it is half written.
func bootSlots() []bootSlot {
	return []bootSlot{
		{4, SecondUBootOffset, stagedUBoot},
		{1, 0, stagedSPL},
		{2, 0, stagedSPL},
		{3, 0, stagedSPL},
		{0, 0, stagedSPL},
		{4, 0, stagedUBoot},
	}
}

// stockBootSlots is the write order for Bose's chain, whose SPL reads only the first
// U-Boot copy. The first copy goes first, while our SPL still runs and falls back
// to our spare; then Bose's SPL, backups before mtd0; the spare last, which Bose's chain
// never reads. Between the first copy and the SPLs our SPL finds Bose's U-Boot in the
// first copy, which it does not boot while our spare is whole
// (uboot-2026.07-spl-second-uboot.patch).
func stockBootSlots() []bootSlot {
	return []bootSlot{
		{4, 0, stagedUBoot},
		{1, 0, stagedSPL},
		{2, 0, stagedSPL},
		{3, 0, stagedSPL},
		{0, 0, stagedSPL},
		{4, SecondUBootOffset, stagedUBoot},
	}
}

// String names the copy as the install report and the log do: mtd1, or mtd4+0x100000
// for the spare U-Boot.
func (s bootSlot) String() string {
	if s.offset == 0 {
		return fmt.Sprintf("mtd%d", s.mtd)
	}

	return fmt.Sprintf("mtd%d+%#x", s.mtd, s.offset)
}

func (s bootSlot) device() string { return fmt.Sprintf("/dev/mtd%d", s.mtd) }

// holds reports whether the slot carries want. Not being able to tell counts as no.
func (s bootSlot) holds(read func(string, ...string) ([]byte, error), want []byte) bool {
	ok, _ := s.check(read, want)

	return ok
}

// check reports whether the slot carries want, and how many bitflips the NAND's ECC
// corrected while it was read: the rise of the partition's count across the read.
func (s bootSlot) check(read func(string, ...string) ([]byte, error), want []byte) (bool, uint64) {
	args := []string{"--omitoob", "--bb=skipbad", "-l", strconv.Itoa(len(want))}
	if s.offset > 0 {
		args = append(args, "-s", strconv.Itoa(s.offset))
	}

	before, okBefore := mtdCorrectedBits(s.mtd)
	got, err := read("nanddump", append(args, s.device())...)

	var flips uint64

	after, okAfter := mtdCorrectedBits(s.mtd)
	if okBefore && okAfter && after > before {
		flips = after - before
	}

	return err == nil && bytes.Equal(got, want), flips
}

// worn reports whether flips corrected bitflips in one read of this copy are enough to
// write it again although its bytes are right: at least the partition's bitflip_threshold,
// the count in one ECC step at which the kernel itself calls a page due for a rewrite
// (-EUCLEAN, what UBI scrubs on). The count here is the whole copy's, so this rewrites at
// that many flips spread over the copy as well, which for a boot loader the ROM and the SPL
// must read is the safe side. Without the kernel's counts nothing is rewritten for them.
func (s bootSlot) worn(flips uint64) bool {
	threshold, ok := mtdBitflipThreshold(s.mtd)

	return ok && threshold > 0 && flips >= uint64(threshold)
}

// write replaces the slot with the file at path. Only this slot's blocks are erased: the
// first U-Boot must stay whole while the spare behind it is written, and the spare
// must stay whole while the first copy is.
func (s bootSlot) write(run flash.Command, path string) error {
	if s.mtd == ubootMTD {
		return s.writeUBoot(run, path)
	}

	err := run(nil, "flash_erase", s.device(), "0", "0")
	if err != nil {
		return err
	}

	return run(nil, "nandwrite", "-p", s.device(), path)
}

const nandBlock = 0x20000

// half is the part of mtd4 this U-Boot copy lives in, [start, end): blocks 0-7 for the
// first copy, 8-13 for the spare. Block 14 is the redundant environment's.
func (s bootSlot) half() (int, int) {
	if s.offset < SecondUBootOffset {
		return 0, SecondUBootOffset
	}

	return SecondUBootOffset, spareEnd
}

// ubootGoodBlocks lists the good blocks of the slot's half of mtd4, and refuses when an
// image of size bytes does not fit in them.
func (s bootSlot) ubootGoodBlocks(size int64) ([]int, error) {
	bad, err := mtdBadBlocks(ubootMTD)
	if err != nil {
		return nil, fmt.Errorf("mtd4 bad block count: %w", err)
	}

	start, end := s.half()

	var good []int

	for off := start; off < end; off += nandBlock {
		if bad > 0 { // only then is there a block to ask about
			isBad, err := mtdBlockBad(s.device(), off)
			if err != nil {
				return nil, fmt.Errorf("mtd4+%#x: %w", off, err)
			}

			if isBad {
				continue
			}
		}

		good = append(good, off)
	}

	if need := (size + nandBlock - 1) / nandBlock; need > int64(len(good)) {
		return nil, fmt.Errorf("mtd4+%#x: %w %d good blocks and its half has %d", start, errUBootNeeds, need, len(good))
	}

	return good, nil
}

// writeUBoot writes one U-Boot copy a block at a time into the good blocks of its own
// half of mtd4. nandwrite skips bad blocks, and after a failed write carries on in the
// next block, so the first copy could run into the spare behind it, which is already
// written. dd skips nothing: a failed block fails the write, the other half
// untouched.
func (s bootSlot) writeUBoot(run flash.Command, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("staged U-Boot: %w", err)
	}

	good, err := s.ubootGoodBlocks(info.Size())
	if err != nil {
		return err
	}

	start, end := s.half()

	blocks := strconv.Itoa((end - start) / nandBlock) // its own half and nothing past it

	err = run(nil, "flash_erase", s.device(), strconv.Itoa(start), blocks)
	if err != nil {
		return err
	}
	// The kernel refuses a write that ends inside a page ("non page aligned data", the
	// WLA stock restore), so dd reads a copy padded with 0xff to whole blocks.
	padded, err := padToBlocks(path)
	if err != nil {
		return err
	}
	defer os.Remove(padded)

	for i := 0; int64(i)*nandBlock < info.Size(); i++ {
		err = run(nil, "dd", "if="+padded, "of="+s.device(), "bs="+strconv.Itoa(nandBlock), "count=1",
			"skip="+strconv.Itoa(i), "seek="+strconv.Itoa(good[i]/nandBlock), "conv=notrunc")
		if err != nil {
			return fmt.Errorf("mtd4+%#x: %w", good[i], err)
		}
	}

	return nil
}

// padToBlocks copies the file at path next to it, filled with 0xff up to a whole number
// of NAND blocks, and returns the copy's path.
func padToBlocks(path string) (string, error) {
	image, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("padding: %w", err)
	}

	image = append(image, bytes.Repeat([]byte{0xff}, (nandBlock-len(image)%nandBlock)%nandBlock)...)

	padded, err := os.CreateTemp(filepath.Dir(path), "padded-")
	if err != nil {
		return "", fmt.Errorf("padding: %w", err)
	}

	_, werr := padded.Write(image)

	err = errors.Join(werr, padded.Close())
	if err != nil {
		_ = os.Remove(padded.Name())

		return "", err
	}

	return padded.Name(), nil
}

// mtdBadBlocks is how many blocks of an MTD partition the kernel has marked bad. A kernel
// without the attribute cannot say, which counts as none.
//
//nolint:gochecknoglobals // a hook the tests replace to fake bad blocks.
var mtdBadBlocks = func(mtd int) (int, error) {
	count, err := os.ReadFile(fmt.Sprintf("/sys/class/mtd/mtd%d/bad_blocks", mtd))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("sysfs: %w", err)
	}

	return strconv.Atoi(string(bytes.TrimSpace(count)))
}

// mtdCorrectedBits is how many bitflips the NAND's ECC corrected on an MTD partition since
// boot, the kernel's ecc_stats.corrected (what ECCGETSTATS returns), from sysfs. ok is
// false when the kernel does not say.
//
//nolint:gochecknoglobals // a hook the tests replace to fake ECC statistics.
var mtdCorrectedBits = func(mtd int) (uint64, bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/sys/class/mtd/mtd%d/corrected_bits", mtd))
	if err != nil {
		return 0, false
	}

	corrected, err := strconv.ParseUint(string(bytes.TrimSpace(raw)), 10, 64)

	return corrected, err == nil
}

// mtdBitflipThreshold is the partition's bitflip_threshold: three quarters of the ECC
// strength by default, 6 for the BCH8 these boards use.
//
//nolint:gochecknoglobals // a hook the tests replace to fake ECC statistics.
var mtdBitflipThreshold = func(mtd int) (int, bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/sys/class/mtd/mtd%d/bitflip_threshold", mtd))
	if err != nil {
		return 0, false
	}

	threshold, err := strconv.Atoi(string(bytes.TrimSpace(raw)))

	return threshold, err == nil
}

// memGetBadBlock is MEMGETBADBLOCK from <mtd/mtd-abi.h>, _IOW('M', 11, __kernel_loff_t).
const memGetBadBlock = 0x40084d0b

// mtdBlockBad asks the kernel whether the block at off of an MTD device is marked bad.
//
//nolint:gochecknoglobals // a hook the tests replace to fake bad blocks.
var mtdBlockBad = func(dev string, off int) (bool, error) {
	device, err := os.Open(dev)
	if err != nil {
		return false, fmt.Errorf("bad block check: %w", err)
	}
	defer device.Close()

	offset := int64(off)

	bad, _, errno := syscall.Syscall(syscall.SYS_IOCTL, device.Fd(), memGetBadBlock, uintptr(unsafe.Pointer(&offset)))
	if errno != 0 {
		return false, fmt.Errorf("MEMGETBADBLOCK: %w", errno)
	}

	return bad != 0, nil
}

// BootloadersChanging names the copies in mtd0-mtd4 (bootSlot.String) that do not already hold the bytes
// this package would write, or hold them with so many bitflips corrected that they are
// written again (bootSlot.worn). An unreadable partition counts as changing: not being
// able to tell is not the same as knowing it matches.
func BootloadersChanging(read func(string, ...string) ([]byte, error), mlo, uboot string) ([]string, error) {
	staged := map[string][]byte{}

	for name, path := range map[string]string{stagedSPL: mlo, stagedUBoot: uboot} {
		image, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("staged %s: %w", name, err)
		}

		staged[name] = image
	}

	var changing []string

	for _, slot := range bootSlots() {
		holds, flips := slot.check(read, staged[slot.name])
		if !holds || slot.worn(flips) {
			changing = append(changing, slot.String())
		}
	}

	return changing, nil
}

// WriteBootloaders replaces the boot chain in bootSlots order, or stockBootSlots when
// the chain is not ours, leaving alone every slot that already holds its bytes:
// reinstalling the chain the board runs is the common case, and erasing a partition to
// put back what was in it is pure risk for no change. The exception is a copy whose read
// needed many ECC corrections (bootSlot.worn): its charge is fading, and only a rewrite
// restores it before the flips outgrow what the ROM's and the SPL's ECC correct. Each copy
// is written at most once per install, in its place in the order, and logged on log (nil:
// not logged); one that reads back right but still worn is left so and logged.
//
//nolint:revive // flag-parameter: ours names whose chain this is; the firmware handler calls this exported signature.
func WriteBootloaders(
	run flash.Command, read func(string, ...string) ([]byte, error), mlo, uboot string, ours bool, log *slog.Logger,
) error {
	paths := map[string]string{stagedSPL: mlo, stagedUBoot: uboot}

	err := ubootFitsBothHalves(uboot)
	if err != nil {
		return err
	}

	order := bootSlots()
	if !ours {
		order = stockBootSlots()
	}

	wrote := false

	for _, slot := range order {
		written, err := slot.replace(run, read, paths[slot.name], log)
		if err != nil {
			return err
		}

		wrote = wrote || written
	}

	if !wrote {
		return nil
	}
	// A block that went bad during a write shifts what follows it. Both U-Boot copies
	// share mtd4, so read both again now that everything is written.
	for _, slot := range bootSlots() {
		if slot.mtd != ubootMTD {
			continue
		}

		want, err := os.ReadFile(paths[slot.name])
		if err != nil {
			return fmt.Errorf("staged %s: %w", slot.name, err)
		}

		if !slot.holds(read, want) {
			return fmt.Errorf("mtd4+%#x %w", slot.offset, errUBootLost)
		}
	}

	return nil
}

// replace writes the file at path into the slot and reads it back, unless the slot already
// holds it without wear. It reports whether it wrote, and logs on log (nil: not logged).
func (s bootSlot) replace(
	run flash.Command, read func(string, ...string) ([]byte, error), path string, log *slog.Logger,
) (bool, error) {
	want, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("staged %s: %w", s.name, err)
	}

	holds, flips := s.check(read, want)
	if holds && !s.worn(flips) {
		return false, nil
	}

	if holds && log != nil {
		log.Warn("rewriting a boot loader copy that reads right only after ECC corrections",
			"mtd", s.mtd, "offset", s.offset, "corrected_bits", flips)
	}

	err = s.write(run, path)
	if err != nil {
		return false, err
	}

	holds, flips = s.check(read, want)
	if !holds {
		return false, fmt.Errorf("mtd%d %w", s.mtd, errReadback)
	}

	if s.worn(flips) && log != nil {
		log.Warn("boot loader copy still needs ECC corrections after its rewrite; its blocks are wearing out",
			"mtd", s.mtd, "offset", s.offset, "corrected_bits", flips)
	}

	if log != nil {
		log.Info("boot loader copy written and read back", "copy", s.String())
	}

	return true, nil
}

// ubootFitsBothHalves refuses, before anything is written, a U-Boot that bad blocks leave
// no room for in either half of mtd4.
func ubootFitsBothHalves(uboot string) error {
	info, err := os.Stat(uboot)
	if err != nil {
		return fmt.Errorf("staged U-Boot: %w", err)
	}

	for _, slot := range bootSlots() {
		if slot.mtd != ubootMTD {
			continue
		}

		_, err = slot.ubootGoodBlocks(info.Size())
		if err != nil {
			return err
		}
	}

	return nil
}

// ValidateAM335xSPL checks the TI image the mask ROM loads: a 0x200-byte
// configuration header, then an 8-byte GP header of image size and load address,
// then the SPL itself. mkimage counts its own GP header in the size, so our MLO is
// exactly 0x200 + size bytes; Bose's declares less than it stores.
//
// This used to pin the first instruction to 0xea00000f, which is the exact branch
// our own mainline SPL happens to start with. Bose's SPL starts with 0xeb000042 --
// a bl rather than a b, to a different offset -- so the approved stock package,
// the one file that can send this speaker home, was refused for not being a byte
// copy of ours. What actually matters is checked instead: the header the ROM
// parses, the address it loads to, and that execution begins with a branch.
func ValidateAM335xSPL(image []byte) error {
	if len(image) < splMinSize {
		return errSPLShort
	}

	if binary.LittleEndian.Uint32(image) != splTOCFirstItem {
		return errSPLNoHeader
	}

	if got := binary.LittleEndian.Uint32(image[splLoadAddrOff:]); got != am335xSRAMLoad {
		return fmt.Errorf("loads to %#08x, %w", got, errSPLNotSRAM)
	}

	if end := splGPHeader + int64(binary.LittleEndian.Uint32(image[splGPHeader:])); end > int64(len(image)) {
		return fmt.Errorf("declares %d bytes %w %d", end, errSPLModuleHolds, len(image))
	}
	// An unconditional ARM branch, b or bl: the SPL entry point is a jump to reset.
	if op := binary.LittleEndian.Uint32(image[splEntryOff:]) >> armOpcodeShift; op != armBranch && op != armBranchLink {
		return fmt.Errorf("starts with %#08x, %w", binary.LittleEndian.Uint32(image[splEntryOff:]), errSPLNoBranch)
	}

	return nil
}
