// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import (
	"bytes"
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
	n := &eccNAND{t: t, slots: map[nandSlot][]byte{}, flips: map[nandSlot]uint64{}, counts: map[int]uint64{}, stats: true}
	for _, s := range bootSlots {
		b := mlo
		if s.name == stagedUBoot {
			b = uboot
		}
		n.slots[nandSlot{s.mtd, s.offset}] = b
	}
	oldBits, oldThreshold := mtdCorrectedBits, mtdBitflipThreshold
	t.Cleanup(func() { mtdCorrectedBits, mtdBitflipThreshold = oldBits, oldThreshold })
	mtdCorrectedBits = func(mtd int) (uint64, bool) { return n.counts[mtd], n.stats }
	mtdBitflipThreshold = func(int) (int, bool) { return 6, n.stats } // BCH8: 8 * 3/4
	return n
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
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		s := deviceSlot(n.t, dev, off)
		n.slots[s] = bytes.TrimRight(b, "\xff") // dd's padding is not part of the image
		n.flips[s] = n.rewritten
	}
	return nil
}

func ddArgOffset(s string) int {
	if s == "0" {
		return 0
	}
	return SecondUBootOffset
}

func eccChain(t *testing.T) (mlo, uboot string, mloBytes, ubootBytes []byte) {
	t.Helper()
	dir := t.TempDir()
	mlo, uboot = filepath.Join(dir, "MLO"), filepath.Join(dir, "u-boot.img")
	mloBytes, ubootBytes = []byte("our-spl-bytes"), []byte("our-uboot-bytes")
	if err := os.WriteFile(mlo, mloBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(uboot, ubootBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return mlo, uboot, mloBytes, ubootBytes
}

// A copy that reads back right only after many ECC corrections is written again, alone and
// once, and logged; the rest of the chain, which reads clean, is left alone.
func TestWornBootloaderCopyIsRewritten(t *testing.T) {
	mlo, uboot, mloBytes, ubootBytes := eccChain(t)
	n := newECCNAND(t, mloBytes, ubootBytes)
	n.flips[nandSlot{2, 0}] = 7                 // SPL backup 2: past the threshold
	n.flips[nandSlot{4, SecondUBootOffset}] = 9 // the spare U-Boot too
	n.flips[nandSlot{3, 0}] = 5                 // below it: left
	changing, err := BootloadersChanging(n.read, mlo, uboot)
	if err != nil || !slices.Equal(changing, []string{"mtd4+0x100000", "mtd2"}) {
		t.Fatalf("BootloadersChanging = %v, %v; want the spare (mtd4) and mtd2", changing, err)
	}
	var logged bytes.Buffer
	if err := WriteBootloaders(n.run, n.read, mlo, uboot, true, slog.New(slog.NewTextHandler(&logged, nil))); err != nil {
		t.Fatal(err)
	}
	if want := []nandSlot{{4, SecondUBootOffset}, {2, 0}}; !slices.Equal(n.erased, want) {
		t.Fatalf("erased %v, want %v", n.erased, want)
	}
	if got := strings.Count(logged.String(), "rewriting a boot loader copy"); got != 2 {
		t.Fatalf("%d rewrites logged:\n%s", got, logged.String())
	}
	if !strings.Contains(logged.String(), "mtd=2") || !strings.Contains(logged.String(), "corrected_bits=7") {
		t.Fatalf("log does not name the copy and its count:\n%s", logged.String())
	}
	// Written again they read clean: the next install leaves the chain alone.
	n.erased = nil
	if err := WriteBootloaders(n.run, n.read, mlo, uboot, true, nil); err != nil || len(n.erased) != 0 {
		t.Fatalf("second install erased %v (%v)", n.erased, err)
	}
}

// A copy whose blocks keep needing corrections after the rewrite is written once per
// install, not in a loop, and the install goes on: its bytes are right.
func TestWornBootloaderCopyIsRewrittenOncePerInstall(t *testing.T) {
	mlo, uboot, mloBytes, ubootBytes := eccChain(t)
	n := newECCNAND(t, mloBytes, ubootBytes)
	n.flips[nandSlot{0, 0}] = 12
	n.rewritten = 12
	var logged bytes.Buffer
	if err := WriteBootloaders(n.run, n.read, mlo, uboot, true, slog.New(slog.NewTextHandler(&logged, nil))); err != nil {
		t.Fatal(err)
	}
	if want := []nandSlot{{0, 0}}; !slices.Equal(n.erased, want) {
		t.Fatalf("erased %v, want mtd0 once", n.erased)
	}
	if !strings.Contains(logged.String(), "still needs ECC corrections") {
		t.Fatalf("a copy still worn after its rewrite was not logged:\n%s", logged.String())
	}
}

// Without the kernel's ECC statistics a copy whose bytes match is never rewritten.
func TestNoECCStatisticsNoRewrite(t *testing.T) {
	mlo, uboot, mloBytes, ubootBytes := eccChain(t)
	n := newECCNAND(t, mloBytes, ubootBytes)
	n.stats = false
	n.flips[nandSlot{1, 0}] = 50
	if changing, err := BootloadersChanging(n.read, mlo, uboot); err != nil || len(changing) != 0 {
		t.Fatalf("BootloadersChanging = %v, %v", changing, err)
	}
	if err := WriteBootloaders(n.run, n.read, mlo, uboot, true, nil); err != nil || len(n.erased) != 0 {
		t.Fatalf("erased %v (%v)", n.erased, err)
	}
}
