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

// VerifyBootloaders validates exact streamed bytes before touching mtd0-mtd4.
func VerifyBootloaders(pkg *stu.Package, model, mloPath, ubootPath string) error {
	mlo, uboot, ok := BootloaderModules(pkg, model)
	if !ok {
		return errors.New("the package carries no SPL and U-Boot to restore the boot chain with")
	}
	if mlo.Size <= 0 || mlo.Size > 128<<10 {
		return errors.New("MLO does not fit a 128 KiB SPL partition")
	}
	if uboot.Size <= 0 || uboot.Size > maxUBootSize {
		return errors.New("u-boot.img does not fit twice in its NAND partition")
	}
	for _, item := range []struct {
		module stu.Module
		path   string
	}{{mlo, mloPath}, {uboot, ubootPath}} {
		b, err := os.ReadFile(item.path)
		if err != nil || int64(len(b)) != item.module.Size {
			return fmt.Errorf("%s staged payload identity mismatch", item.module.Name)
		}
		if item.module.Name == mlo.Name {
			if err := ValidateAM335xSPL(b); err != nil {
				return fmt.Errorf("%s %w", mlo.Name, err)
			}
			continue
		}
		if len(b) < 64 || binary.BigEndian.Uint32(b) != 0x27051956 || uint64(binary.BigEndian.Uint32(b[12:]))+64 > uint64(len(b)) || crc32.ChecksumIEEE(b[64:64+binary.BigEndian.Uint32(b[12:])]) != binary.BigEndian.Uint32(b[24:]) {
			return errors.New("u-boot.img has invalid legacy uImage header")
		}
		header := append([]byte(nil), b[:64]...)
		binary.BigEndian.PutUint32(header[4:], 0)
		if crc32.ChecksumIEEE(header) != binary.BigEndian.Uint32(b[4:]) {
			return errors.New("u-boot.img has invalid legacy uImage header CRC")
		}
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
var bootSlots = []bootSlot{
	{4, SecondUBootOffset, stagedUBoot},
	{1, 0, stagedSPL}, {2, 0, stagedSPL}, {3, 0, stagedSPL}, {0, 0, stagedSPL},
	{4, 0, stagedUBoot},
}

// stockBootSlots is the write order for Bose's chain, whose SPL reads only the first
// U-Boot copy. The first copy goes first, while our SPL still runs and falls back
// to our spare; then Bose's SPL, backups before mtd0; the spare last, which Bose's chain
// never reads. Between the first copy and the SPLs our SPL finds Bose's U-Boot in the
// first copy, which it does not boot while our spare is whole
// (uboot-2026.07-spl-second-uboot.patch).
var stockBootSlots = []bootSlot{
	{4, 0, stagedUBoot},
	{1, 0, stagedSPL}, {2, 0, stagedSPL}, {3, 0, stagedSPL}, {0, 0, stagedSPL},
	{4, SecondUBootOffset, stagedUBoot},
}

func (s bootSlot) device() string { return fmt.Sprintf("/dev/mtd%d", s.mtd) }

// String names the copy as the install report and the log do: mtd1, or mtd4+0x100000
// for the spare U-Boot.
func (s bootSlot) String() string {
	if s.offset == 0 {
		return fmt.Sprintf("mtd%d", s.mtd)
	}
	return fmt.Sprintf("mtd%d+%#x", s.mtd, s.offset)
}

// holds reports whether the slot carries want. Not being able to tell counts as no.
func (s bootSlot) holds(read func(string, ...string) ([]byte, error), want []byte) bool {
	ok, _ := s.check(read, want)
	return ok
}

// check reports whether the slot carries want, and how many bitflips the NAND's ECC
// corrected while it was read: the rise of the partition's count across the read.
func (s bootSlot) check(read func(string, ...string) ([]byte, error), want []byte) (holds bool, flips uint64) {
	args := []string{"--omitoob", "--bb=skipbad", "-l", strconv.Itoa(len(want))}
	if s.offset > 0 {
		args = append(args, "-s", strconv.Itoa(s.offset))
	}
	before, okBefore := mtdCorrectedBits(s.mtd)
	got, err := read("nanddump", append(args, s.device())...)
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
	if s.mtd == 4 {
		return s.writeUBoot(run, path)
	}
	if err := run(nil, "flash_erase", s.device(), "0", "0"); err != nil {
		return err
	}
	return run(nil, "nandwrite", "-p", s.device(), path)
}

const nandBlock = 0x20000

// half is the part of mtd4 this U-Boot copy lives in, [start, end): blocks 0-7 for the
// first copy, 8-13 for the spare. Block 14 is the redundant environment's.
func (s bootSlot) half() (start, end int) {
	if s.offset < SecondUBootOffset {
		return 0, SecondUBootOffset
	}
	return SecondUBootOffset, spareEnd
}

// ubootGoodBlocks lists the good blocks of the slot's half of mtd4, and refuses when an
// image of size bytes does not fit in them.
func (s bootSlot) ubootGoodBlocks(size int64) ([]int, error) {
	bad, err := mtdBadBlocks(4)
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
		return nil, fmt.Errorf("mtd4+%#x: U-Boot needs %d good blocks and its half has %d", start, need, len(good))
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
		return err
	}
	good, err := s.ubootGoodBlocks(info.Size())
	if err != nil {
		return err
	}
	start, end := s.half()
	blocks := strconv.Itoa((end - start) / nandBlock) // its own half and nothing past it
	if err := run(nil, "flash_erase", s.device(), strconv.Itoa(start), blocks); err != nil {
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
		if err := run(nil, "dd", "if="+padded, "of="+s.device(), "bs="+strconv.Itoa(nandBlock), "count=1",
			"skip="+strconv.Itoa(i), "seek="+strconv.Itoa(good[i]/nandBlock), "conv=notrunc"); err != nil {
			return fmt.Errorf("mtd4+%#x: %w", good[i], err)
		}
	}
	return nil
}

// padToBlocks copies the file at path next to it, filled with 0xff up to a whole number
// of NAND blocks, and returns the copy's path.
func padToBlocks(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	b = append(b, bytes.Repeat([]byte{0xff}, (nandBlock-len(b)%nandBlock)%nandBlock)...)
	f, err := os.CreateTemp(filepath.Dir(path), "padded-")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(b)
	if err := errors.Join(werr, f.Close()); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// mtdBadBlocks is how many blocks of an MTD partition the kernel has marked bad. A kernel
// without the attribute cannot say, which counts as none.
var mtdBadBlocks = func(mtd int) (int, error) {
	b, err := os.ReadFile(fmt.Sprintf("/sys/class/mtd/mtd%d/bad_blocks", mtd))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(bytes.TrimSpace(b)))
}

// mtdCorrectedBits is how many bitflips the NAND's ECC corrected on an MTD partition since
// boot, the kernel's ecc_stats.corrected (what ECCGETSTATS returns), from sysfs. ok is
// false when the kernel does not say.
var mtdCorrectedBits = func(mtd int) (uint64, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/sys/class/mtd/mtd%d/corrected_bits", mtd))
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(string(bytes.TrimSpace(b)), 10, 64)
	return n, err == nil
}

// mtdBitflipThreshold is the partition's bitflip_threshold: three quarters of the ECC
// strength by default, 6 for the BCH8 these boards use.
var mtdBitflipThreshold = func(mtd int) (int, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/sys/class/mtd/mtd%d/bitflip_threshold", mtd))
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(string(bytes.TrimSpace(b)))
	return n, err == nil
}

// memGetBadBlock is MEMGETBADBLOCK from <mtd/mtd-abi.h>, _IOW('M', 11, __kernel_loff_t).
const memGetBadBlock = 0x40084d0b

// mtdBlockBad asks the kernel whether the block at off of an MTD device is marked bad.
var mtdBlockBad = func(dev string, off int) (bool, error) {
	f, err := os.Open(dev)
	if err != nil {
		return false, err
	}
	defer f.Close()
	o := int64(off)
	bad, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), memGetBadBlock, uintptr(unsafe.Pointer(&o)))
	if errno != 0 {
		return false, errno
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
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		staged[name] = b
	}
	var changing []string
	for _, slot := range bootSlots {
		if holds, flips := slot.check(read, staged[slot.name]); !holds || slot.worn(flips) {
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
func WriteBootloaders(run flash.Command, read func(string, ...string) ([]byte, error), mlo, uboot string, ours bool, log *slog.Logger) error {
	paths := map[string]string{stagedSPL: mlo, stagedUBoot: uboot}
	if err := ubootFitsBothHalves(uboot); err != nil {
		return err
	}
	order := bootSlots
	if !ours {
		order = stockBootSlots
	}
	wrote := false
	for _, slot := range order {
		want, err := os.ReadFile(paths[slot.name])
		if err != nil {
			return err
		}
		holds, flips := slot.check(read, want)
		if holds && !slot.worn(flips) {
			continue
		}
		if holds && log != nil {
			log.Warn("rewriting a boot loader copy that reads right only after ECC corrections",
				"mtd", slot.mtd, "offset", slot.offset, "corrected_bits", flips)
		}
		if err := slot.write(run, paths[slot.name]); err != nil {
			return err
		}
		holds, flips = slot.check(read, want)
		if !holds {
			return fmt.Errorf("mtd%d readback verification failed", slot.mtd)
		}
		if slot.worn(flips) && log != nil {
			log.Warn("boot loader copy still needs ECC corrections after its rewrite; its blocks are wearing out",
				"mtd", slot.mtd, "offset", slot.offset, "corrected_bits", flips)
		}
		if log != nil {
			log.Info("boot loader copy written and read back", "copy", slot.String())
		}
		wrote = true
	}
	if !wrote {
		return nil
	}
	// A block that went bad during a write shifts what follows it. Both U-Boot copies
	// share mtd4, so read both again now that everything is written.
	for _, slot := range bootSlots {
		if slot.mtd != 4 {
			continue
		}
		want, err := os.ReadFile(paths[slot.name])
		if err != nil {
			return err
		}
		if !slot.holds(read, want) {
			return fmt.Errorf("mtd4+%#x no longer holds U-Boot after the boot chain write", slot.offset)
		}
	}
	return nil
}

// ubootFitsBothHalves refuses, before anything is written, a U-Boot that bad blocks leave
// no room for in either half of mtd4.
func ubootFitsBothHalves(uboot string) error {
	info, err := os.Stat(uboot)
	if err != nil {
		return err
	}
	for _, slot := range bootSlots {
		if slot.mtd != 4 {
			continue
		}
		if _, err := slot.ubootGoodBlocks(info.Size()); err != nil {
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
func ValidateAM335xSPL(b []byte) error {
	if len(b) < 0x20c {
		return errors.New("is too short to hold a configuration header")
	}
	if binary.LittleEndian.Uint32(b) != 0x40 {
		return errors.New("has no NAND configuration header")
	}
	if got := binary.LittleEndian.Uint32(b[0x204:]); got != 0x402f0400 {
		return fmt.Errorf("loads to %#08x, not AM335x SRAM at 0x402f0400", got)
	}
	if end := 0x200 + int64(binary.LittleEndian.Uint32(b[0x200:])); end > int64(len(b)) {
		return fmt.Errorf("declares %d bytes but the module holds %d", end, len(b))
	}
	// An unconditional ARM branch, b or bl: the SPL entry point is a jump to reset.
	if op := binary.LittleEndian.Uint32(b[0x208:]) >> 24; op != 0xea && op != 0xeb {
		return fmt.Errorf("starts with %#08x, which is not a branch to the reset handler",
			binary.LittleEndian.Uint32(b[0x208:]))
	}
	return nil
}
