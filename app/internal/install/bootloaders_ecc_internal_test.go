// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// eccNAND is a boot chain that holds the package's bytes everywhere, with a fake of the
// kernel's ECC statistics: each read of a copy adds that copy's corrected bitflips to its
// partition's count, and a rewrite sets the copy's flips to rewritten.
type eccNAND struct {
	t         *testing.T
	slots     map[nandSlot][]byte
	flips     map[nandSlot]uint64 // per read of the copy
	rewritten uint64              // a copy's flips once written again
	counts    map[int]uint64      // the partitions' corrected_bits
	stats     bool                // the kernel reports them
	erased    []nandSlot
}

func newECCNAND(t *testing.T, mlo, uboot []byte) *eccNAND {
	t.Helper()

	fake := &eccNAND{
		t: t, slots: map[nandSlot][]byte{}, flips: map[nandSlot]uint64{}, rewritten: 0,
		counts: map[int]uint64{}, stats: true, erased: nil,
	}

	for _, slot := range bootSlots() {
		image := mlo
		if slot.name == stagedUBoot {
			image = uboot
		}

		fake.slots[nandSlot{slot.mtd, slot.offset}] = image
	}

	oldBits, oldThreshold := mtdCorrectedBits, mtdBitflipThreshold

	t.Cleanup(func() { mtdCorrectedBits, mtdBitflipThreshold = oldBits, oldThreshold })

	mtdCorrectedBits = func(mtd int) (uint64, bool) { return fake.counts[mtd], fake.stats }
	mtdBitflipThreshold = func(int) (int, bool) { return 6, fake.stats } // BCH8: 8 * 3/4

	return fake
}

func (n *eccNAND) read(_ string, args ...string) ([]byte, error) {
	s := deviceSlot(n.t, args[len(args)-1], offsetFlag(args))
	n.counts[s.mtd] += n.flips[s]

	return n.slots[s], nil
}

func (n *eccNAND) run(_ io.Reader, name string, args ...string) error {
	switch name {
	case "flash_erase":
		s := deviceSlot(n.t, args[0], 0)
		if s.mtd == 4 {
			s.offset = ddArgOffset(args[1])
		}

		n.erased = append(n.erased, s)
	case "nandwrite", "dd":
		src, dev, off := args[len(args)-1], args[len(args)-2], 0
		if name == "dd" {
			src, dev = strings.TrimPrefix(args[0], "if="), strings.TrimPrefix(args[1], "of=")
			off = ddArg(args, "seek") * nandBlock
		}

		image, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("reading %s: %w", src, err)
		}

		s := deviceSlot(n.t, dev, off)
		n.slots[s] = bytes.TrimRight(image, "\xff") // dd's padding is not part of the image
		n.flips[s] = n.rewritten
	default: // the other commands change nothing the fake holds
	}

	return nil
}

func ddArgOffset(s string) int {
	if s == "0" {
		return 0
	}

	return SecondUBootOffset
}

// eccFiles is a staged boot chain: the two files and their bytes.
type eccFiles struct {
	mlo, uboot           string
	mloBytes, ubootBytes []byte
}

func eccChain(t *testing.T) eccFiles {
	t.Helper()

	dir := t.TempDir()
	files := eccFiles{
		mlo: filepath.Join(dir, "MLO"), uboot: filepath.Join(dir, "u-boot.img"),
		mloBytes: []byte("our-spl-bytes"), ubootBytes: []byte("our-uboot-bytes"),
	}

	err := os.WriteFile(files.mlo, files.mloBytes, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(files.uboot, files.ubootBytes, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return files
}

// A copy that reads back right only after many ECC corrections is written again, alone and
// once, and logged; the rest of the chain, which reads clean, is left alone.
//
//nolint:paralleltest // newECCNAND replaces the package's ECC statistics hooks.
func TestWornBootloaderCopyIsRewritten(t *testing.T) {
	files := eccChain(t)
	mlo, uboot := files.mlo, files.uboot
	fake := newECCNAND(t, files.mloBytes, files.ubootBytes)
	fake.flips[nandSlot{2, 0}] = 7                 // SPL backup 2: past the threshold
	fake.flips[nandSlot{4, SecondUBootOffset}] = 9 // the spare U-Boot too
	fake.flips[nandSlot{3, 0}] = 5                 // below it: left

	changing, err := BootloadersChanging(fake.read, mlo, uboot)
	if err != nil || !slices.Equal(changing, []string{"mtd4+0x100000", "mtd2"}) {
		t.Fatalf("BootloadersChanging = %v, %v; want the spare (mtd4) and mtd2", changing, err)
	}

	var logged bytes.Buffer

	err = WriteBootloaders(fake.run, fake.read, mlo, uboot, true, slog.New(slog.NewTextHandler(&logged, nil)))
	if err != nil {
		t.Fatal(err)
	}

	if want := []nandSlot{{4, SecondUBootOffset}, {2, 0}}; !slices.Equal(fake.erased, want) {
		t.Fatalf("erased %v, want %v", fake.erased, want)
	}

	if got := strings.Count(logged.String(), "rewriting a boot loader copy"); got != 2 {
		t.Fatalf("%d rewrites logged:\n%s", got, logged.String())
	}

	if !strings.Contains(logged.String(), "mtd=2") || !strings.Contains(logged.String(), "corrected_bits=7") {
		t.Fatalf("log does not name the copy and its count:\n%s", logged.String())
	}
	// Written again they read clean: the next install leaves the chain alone.
	fake.erased = nil

	err = WriteBootloaders(fake.run, fake.read, mlo, uboot, true, nil)
	if err != nil || len(fake.erased) != 0 {
		t.Fatalf("second install erased %v (%v)", fake.erased, err)
	}
}

// A copy whose blocks keep needing corrections after the rewrite is written once per
// install, not in a loop, and the install goes on: its bytes are right.
//
//nolint:paralleltest // newECCNAND replaces the package's ECC statistics hooks.
func TestWornBootloaderCopyIsRewrittenOncePerInstall(t *testing.T) {
	files := eccChain(t)
	mlo, uboot := files.mlo, files.uboot
	fake := newECCNAND(t, files.mloBytes, files.ubootBytes)
	fake.flips[nandSlot{0, 0}] = 12
	fake.rewritten = 12

	var logged bytes.Buffer

	err := WriteBootloaders(fake.run, fake.read, mlo, uboot, true, slog.New(slog.NewTextHandler(&logged, nil)))
	if err != nil {
		t.Fatal(err)
	}

	if want := []nandSlot{{0, 0}}; !slices.Equal(fake.erased, want) {
		t.Fatalf("erased %v, want mtd0 once", fake.erased)
	}

	if !strings.Contains(logged.String(), "still needs ECC corrections") {
		t.Fatalf("a copy still worn after its rewrite was not logged:\n%s", logged.String())
	}
}

// Without the kernel's ECC statistics a copy whose bytes match is never rewritten.
//
//nolint:paralleltest // newECCNAND replaces the package's ECC statistics hooks.
func TestNoECCStatisticsNoRewrite(t *testing.T) {
	files := eccChain(t)
	mlo, uboot := files.mlo, files.uboot
	fake := newECCNAND(t, files.mloBytes, files.ubootBytes)
	fake.stats = false

	fake.flips[nandSlot{1, 0}] = 50

	changing, err := BootloadersChanging(fake.read, mlo, uboot)
	if err != nil || len(changing) != 0 {
		t.Fatalf("BootloadersChanging = %v, %v", changing, err)
	}

	err = WriteBootloaders(fake.run, fake.read, mlo, uboot, true, nil)
	if err != nil || len(fake.erased) != 0 {
		t.Fatalf("erased %v (%v)", fake.erased, err)
	}
}
