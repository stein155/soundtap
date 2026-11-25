// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv"
)

type nandSlot struct{ mtd, offset int }

// nand models the boot chain. A slot being written holds half its bytes, as after a cut,
// and seen keeps the state after every step.
type nand struct {
	slots map[nandSlot][]byte
	seen  []map[nandSlot][]byte
}

func (n *nand) step(s nandSlot, b []byte) {
	n.slots[s] = b
	n.seen = append(n.seen, maps.Clone(n.slots))
}

func deviceSlot(t *testing.T, dev string, offset int) nandSlot {
	t.Helper()

	mtd, err := strconv.Atoi(strings.TrimPrefix(dev, "/dev/mtd"))
	if err != nil {
		t.Fatal(err)
	}

	return nandSlot{mtd, offset}
}

func offsetFlag(args []string) int {
	if i := slices.Index(args, "-s"); i >= 0 {
		off, _ := strconv.Atoi(args[i+1])

		return off
	}

	return 0
}

// bootedUBoot is the ROM and the SPL: the first SPL copy that is whole runs, ours reads
// either U-Boot and Bose's only the first. Partial images fail their CRC. It is the U-Boot
// image that runs, nil when none does. Our SPL
// (uboot-2026.07-spl-second-uboot.patch) boots the first copy unless it is not whole, or is
// foreign (not ours, by its uImage name) while the spare is whole and ours.
func bootedUBoot(state map[nandSlot][]byte, ourSPL, boseSPL, foreign []byte, boots ...[]byte) []byte {
	whole := func(s nandSlot) []byte {
		if i := slices.IndexFunc(boots, func(b []byte) bool { return bytes.Equal(state[s], b) }); i >= 0 {
			return boots[i]
		}

		return nil
	}
	ours := func(b []byte) bool { return b != nil && !bytes.Equal(b, foreign) }

	for _, mtd := range []int{0, 1, 2, 3} {
		switch spl := state[nandSlot{mtd, 0}]; {
		case bytes.Equal(spl, ourSPL):
			first, spare := whole(nandSlot{4, 0}), whole(nandSlot{4, SecondUBootOffset})
			switch {
			case ours(first):
				return first
			case ours(spare):
				return spare
			case first != nil:
				return first
			}

			return spare
		case bytes.Equal(spl, boseSPL):
			return whole(nandSlot{4, 0})
		}
	}

	return nil
}

// writeCut runs WriteBootloaders on n, recording the state after every half write.
func writeCut(t *testing.T, board *nand, mlo, uboot string, size int, ours bool) {
	t.Helper()

	run := func(_ io.Reader, name string, args ...string) error {
		switch name {
		case "flash_erase":
			// Every slot in the erased blocks goes, the environment copy in mtd4's last
			// block too: a count of 0 is to the end of the partition.
			off, _ := strconv.Atoi(args[1])
			blocks, _ := strconv.Atoi(args[2])

			end := off + blocks*nandBlock
			if blocks == 0 {
				end = 1 << 30
			}

			dev := deviceSlot(t, args[0], off)
			board.step(dev, nil)

			for s := range board.slots {
				if s.mtd == dev.mtd && s.offset > off && s.offset < end {
					board.step(s, nil)
				}
			}
		case "nandwrite", "dd": // one dd is the whole of these small images
			src, dev, off := args[len(args)-1], args[len(args)-2], offsetFlag(args)
			if name == "dd" {
				src, dev = strings.TrimPrefix(args[0], "if="), strings.TrimPrefix(args[1], "of=")
				off = ddArg(args, "seek") * nandBlock
			}

			image, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("reading %s: %w", src, err)
			}

			image = image[:min(len(image), size)] // dd's 0xff padding is not part of the image
			s := deviceSlot(t, dev, off)
			board.step(s, image[:len(image)/2])
			board.step(s, image)
		default: // the other commands change nothing the model holds
		}

		return nil
	}

	read := func(_ string, args ...string) ([]byte, error) {
		return board.slots[deviceSlot(t, args[len(args)-1], offsetFlag(args))], nil
	}

	err := WriteBootloaders(run, read, mlo, uboot, ours, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(board.seen) == 0 {
		t.Fatal("nothing was written")
	}
}

// Back to stock: Bose's SPL reads only the first U-Boot copy, so that copy is
// written while our SPL and spare still stand behind it.
//
//nolint:paralleltest // WriteBootloaders reads the mtd hooks other tests replace.
func TestEveryCutDuringAStockRestoreStillBoots(t *testing.T) {
	dir := t.TempDir()
	mlo, uboot := filepath.Join(dir, "MLO"), filepath.Join(dir, "u-boot.img")
	ourSPL, ourBoot := []byte("our-spl-bytes"), []byte("our-uboot-bytes")

	boseSPL, boseBoot := []byte("bose-spl-byte"), []byte("bose-uboot-byt")

	err := os.WriteFile(mlo, boseSPL, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(uboot, boseBoot, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	board := &nand{slots: map[nandSlot][]byte{{4, 0}: ourBoot, {4, SecondUBootOffset}: ourBoot}, seen: nil}
	for _, mtd := range []int{0, 1, 2, 3} {
		board.slots[nandSlot{mtd, 0}] = ourSPL
	}

	writeCut(t, board, mlo, uboot, len(boseBoot), false)

	for i, state := range board.seen {
		// Our SPL boots our spare while the first copy is already Bose's.
		if bootedUBoot(state, ourSPL, boseSPL, boseBoot, ourBoot, boseBoot) == nil {
			t.Errorf("a cut after step %d of %d leaves a board that cannot boot", i+1, len(board.seen))
		}
	}

	if !bytes.Equal(board.slots[nandSlot{0, 0}], boseSPL) || !bytes.Equal(board.slots[nandSlot{4, 0}], boseBoot) {
		t.Errorf("the stock chain is not in place: %q %q", board.slots[nandSlot{0, 0}], board.slots[nandSlot{4, 0}])
	}
}

//nolint:paralleltest // WriteBootloaders reads the mtd hooks other tests replace.
func TestEveryCutDuringABootChainWriteStillBoots(t *testing.T) {
	dir := t.TempDir()
	mlo, uboot := filepath.Join(dir, "MLO"), filepath.Join(dir, "u-boot.img")
	newSPL, newBoot := []byte("new-spl-bytes"), []byte("new-uboot-bytes")
	oldSPL, oldBoot := []byte("old-spl-bytes"), []byte("old-uboot-bytes")

	err := os.WriteFile(mlo, newSPL, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(uboot, newBoot, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		spl, boot []byte // on the board before the install
		spare     []byte
		backups   bool
		bose      bool // the old U-Boot is Bose's: our SPL must never run it
	}{
		{"first install: Bose's lone SPL, no spare", oldSPL, oldBoot, nil, false, true},
		{"update: both change", oldSPL, oldBoot, nil, true, false},
		{"update: only U-Boot changes", newSPL, oldBoot, oldBoot, true, false},
		{"update: only the spare is missing", newSPL, newBoot, nil, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			board := &nand{slots: map[nandSlot][]byte{
				{0, 0}: test.spl, {4, 0}: test.boot, {4, SecondUBootOffset}: test.spare,
			}, seen: nil}
			if test.backups {
				for _, mtd := range []int{1, 2, 3} {
					board.slots[nandSlot{mtd, 0}] = test.spl
				}
			}

			writeCut(t, board, mlo, uboot, len(newBoot), true)

			var foreign []byte
			if test.bose {
				foreign = oldBoot
			}

			for step, state := range board.seen {
				boot := bootedUBoot(state, newSPL, oldSPL, foreign, newBoot, oldBoot)
				if boot == nil {
					t.Errorf("a cut after step %d of %d leaves a board that cannot boot", step+1, len(board.seen))
				}
				// An SPL of ours with Bose's U-Boot is a pairing nobody has proven: by the time
				// our SPL runs, our spare is whole and the SPL boots that.
				ourSPLRuns := slices.ContainsFunc([]int{0, 1, 2, 3}, func(m int) bool {
					return bytes.Equal(state[nandSlot{m, 0}], newSPL) &&
						!slices.ContainsFunc([]int{0, 1, 2, 3}[:m], func(k int) bool {
							return bytes.Equal(state[nandSlot{k, 0}], oldSPL)
						})
				})
				if test.bose && bytes.Equal(boot, oldBoot) && ourSPLRuns {
					t.Errorf("a cut after step %d of %d runs our SPL with Bose's U-Boot", step+1, len(board.seen))
				}
			}

			for _, s := range []nandSlot{{0, 0}, {1, 0}, {2, 0}, {3, 0}} {
				if !bytes.Equal(board.slots[s], newSPL) {
					t.Errorf("mtd%d does not hold the new SPL", s.mtd)
				}
			}

			for _, s := range []nandSlot{{4, 0}, {4, SecondUBootOffset}} {
				if !bytes.Equal(board.slots[s], newBoot) {
					t.Errorf("mtd4+%#x does not hold the new U-Boot", s.offset)
				}
			}
		})
	}
}

// The environment format changes with the U-Boot (ADR 0010): ours from before reads the
// single 8 KiB copy in mtd5, the new one reads the redundant copies (mtd5, and mtd4's last
// block) and, while neither checks out, that single copy too. No U-Boot write may touch the
// environment, and at every cut the U-Boot that runs must find one it reads.
//
//nolint:paralleltest // WriteBootloaders reads the mtd hooks other tests replace.
func TestEveryCutAcrossTheEnvironmentFormatChange(t *testing.T) {
	dir := t.TempDir()
	mlo, uboot := filepath.Join(dir, "MLO"), filepath.Join(dir, "u-boot.img")
	newSPL, newBoot := []byte("new-spl-bytes"), []byte("new-uboot-bytes")
	oldSPL, oldBoot := []byte("old-spl-bytes"), []byte("old-uboot-bytes")

	err := os.WriteFile(mlo, newSPL, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(uboot, newBoot, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	single, redundantCopy := []byte("8 KiB single copy"), []byte("redundant copy")
	primary, redundant := nandSlot{5, 0}, nandSlot{4, ubootenv.RedundantOffset}

	for _, test := range []struct {
		name      string
		env, env2 []byte // mtd5 and mtd4's last block before the install
		oldSingle bool   // the U-Boot on the board reads only the single copy
	}{
		{"from a U-Boot that reads the single copy", single, nil, true},
		{"between two that read the redundant one", redundantCopy, redundantCopy, false},
	} {
		reads := func(boot []byte, state map[nandSlot][]byte) bool {
			if test.oldSingle && bytes.Equal(boot, oldBoot) {
				return bytes.Equal(state[primary], single)
			}

			return bytes.Equal(state[primary], redundantCopy) || bytes.Equal(state[redundant], redundantCopy) ||
				bytes.Equal(state[primary], single)
		}

		t.Run(test.name, func(t *testing.T) {
			board := &nand{slots: map[nandSlot][]byte{
				{4, 0}: oldBoot, {4, SecondUBootOffset}: oldBoot, primary: test.env, redundant: test.env2,
			}, seen: nil}
			for _, mtd := range []int{0, 1, 2, 3} {
				board.slots[nandSlot{mtd, 0}] = oldSPL
			}

			writeCut(t, board, mlo, uboot, len(newBoot), true)

			for step, state := range board.seen {
				boot := bootedUBoot(state, newSPL, oldSPL, nil, newBoot, oldBoot)
				if boot == nil {
					t.Fatalf("a cut after step %d of %d leaves a board that cannot boot", step+1, len(board.seen))
				}

				if !reads(boot, state) {
					t.Errorf("a cut after step %d of %d runs %q with no environment it reads",
						step+1, len(board.seen), boot)
				}
			}

			if !bytes.Equal(board.slots[primary], test.env) || !bytes.Equal(board.slots[redundant], test.env2) {
				t.Error("the boot chain write touched the environment")
			}
		})
	}
}

func ddArg(args []string, key string) int {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, key+"="); ok {
			n, _ := strconv.Atoi(v)

			return n
		}
	}

	return 0
}

// With bad blocks 1 and 3, a U-Boot write must stay in blocks 0-7 or fail before it
// leaves them: the spare from block 8 on is already written.
//
//nolint:paralleltest // it replaces the bad block hooks.
func TestUBootWriteStaysInItsHalf(t *testing.T) {
	oldCount, oldBad := mtdBadBlocks, mtdBlockBad

	t.Cleanup(func() { mtdBadBlocks, mtdBlockBad = oldCount, oldBad })

	bad := map[int]bool{1: true, 3: true}
	mtdBadBlocks = func(int) (int, error) { return len(bad), nil }
	mtdBlockBad = func(_ string, off int) (bool, error) { return bad[off/nandBlock], nil }
	spare := bytes.Repeat([]byte{0xa5}, maxUBootSize)

	for _, test := range []struct {
		name   string
		blocks int
		failAt int // block whose write fails, -1 for none
		ok     bool
	}{
		{"fits in the good blocks", 6, -1, true},
		{"one block too many", 7, -1, false},
		{"last block fails to write", 6, 7, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "u-boot.img")

			image := make([]byte, test.blocks*nandBlock-100)
			for i := range image {
				image[i] = byte(i / nandBlock)
			}

			err := os.WriteFile(path, image, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			// mtd4: erase sets a block to 0xff, dd writes one block and never skips one.
			mem := append(make([]byte, SecondUBootOffset), spare...)

			var (
				cmds  []string
				wrote []int
			)

			run := func(_ io.Reader, name string, args ...string) error {
				cmds = append(cmds, name+" "+strings.Join(args, " "))
				switch name {
				case "flash_erase":
					start, _ := strconv.Atoi(args[1])

					n, _ := strconv.Atoi(args[2])
					for b := start / nandBlock; b < start/nandBlock+n; b++ {
						if !bad[b] {
							copy(mem[b*nandBlock:], bytes.Repeat([]byte{0xff}, nandBlock))
						}
					}
				case "dd":
					block := ddArg(args, "seek")
					if block == test.failAt {
						return io.ErrShortWrite
					}

					wrote = append(wrote, block)

					src, readErr := os.ReadFile(strings.TrimPrefix(args[0], "if="))
					if readErr != nil || len(src)%nandBlock != 0 {
						t.Errorf("dd source: %d bytes, err %v; must be whole blocks", len(src), readErr)

						return io.ErrShortWrite
					}

					skip := ddArg(args, "skip") * nandBlock
					copy(mem[block*nandBlock:], src[skip:skip+nandBlock])
				default:
					t.Errorf("ran %s", name)
				}

				return nil
			}

			err = bootSlot{4, 0, stagedUBoot}.write(run, path)
			if test.ok != (err == nil) {
				t.Fatalf("err = %v", err)
			}

			if !bytes.Equal(mem[SecondUBootOffset:], spare) {
				t.Error("the spare U-Boot changed")
			}

			if slices.ContainsFunc(wrote, func(b int) bool { return b >= SecondUBootOffset/nandBlock || bad[b] }) {
				t.Errorf("wrote blocks %v", wrote)
			}

			if test.ok {
				if !slices.Equal(wrote, []int{0, 2, 4, 5, 6, 7}) {
					t.Errorf("wrote blocks %v, want the six good ones in order", wrote)
				}

				var got []byte
				for _, b := range wrote {
					got = append(got, mem[b*nandBlock:(b+1)*nandBlock]...)
				}

				if !bytes.Equal(got[:len(image)], image) {
					t.Error("the good blocks do not hold the image in order")
				}

				if !bytes.Equal(got[len(image):], bytes.Repeat([]byte{0xff}, len(got)-len(image))) {
					t.Error("the tail of the last block is not 0xff")
				}
			}

			if test.blocks == 7 && len(cmds) != 0 {
				t.Errorf("ran %v before refusing", cmds)
			}
		})
	}
}

// A U-Boot that does not fit in the primary half is refused before the spare, which is
// written first, is touched.
//
//nolint:paralleltest // it replaces the bad block hooks.
func TestUBootThatFitsOnlyOneHalfWritesNothing(t *testing.T) {
	oldCount, oldBad := mtdBadBlocks, mtdBlockBad

	t.Cleanup(func() { mtdBadBlocks, mtdBlockBad = oldCount, oldBad })

	mtdBadBlocks = func(int) (int, error) { return 5, nil }
	mtdBlockBad = func(_ string, off int) (bool, error) {
		return off < SecondUBootOffset && off%(2*nandBlock) != 0, nil
	}
	dir := t.TempDir()

	mlo, uboot := filepath.Join(dir, "MLO"), filepath.Join(dir, "u-boot.img")

	err := os.WriteFile(mlo, []byte("spl"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(uboot, make([]byte, 5*nandBlock), 0o600)
	if err != nil { // the primary half has 4 good blocks
		t.Fatal(err)
	}

	var cmds []string

	run := func(_ io.Reader, name string, args ...string) error {
		cmds = append(cmds, name+" "+strings.Join(args, " "))

		return nil
	}

	read := func(string, ...string) ([]byte, error) { return nil, nil }

	err = WriteBootloaders(run, read, mlo, uboot, true, nil)
	if err == nil {
		t.Fatal("a U-Boot with no room in the primary half was accepted")
	}

	if len(cmds) != 0 {
		t.Errorf("ran %v before refusing", cmds)
	}
}
