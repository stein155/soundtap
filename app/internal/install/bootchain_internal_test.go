// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"
)

// The fake's refusals.
var (
	errUnexpected = errors.New("unexpected")
	errReadPast   = errors.New("read past")
)

// fakeNAND answers nanddump -l N [-s OFF] /dev/mtdM from per-partition bytes.
func fakeNAND(parts map[string][]byte) func(string, ...string) ([]byte, error) {
	return func(name string, args ...string) ([]byte, error) {
		if name != "nanddump" {
			return nil, fmt.Errorf("%w %s", errUnexpected, name)
		}

		dev := args[len(args)-1]

		size, off := 0, 0
		if i := slices.Index(args, "-l"); i >= 0 {
			size, _ = strconv.Atoi(args[i+1])
		}

		if i := slices.Index(args, "-s"); i >= 0 {
			off, _ = strconv.Atoi(args[i+1])
		}

		b, ok := parts[dev]
		if !ok || off+size > len(b) {
			return nil, fmt.Errorf("%w %s", errReadPast, dev)
		}

		return b[off : off+size], nil
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)

	return hex.EncodeToString(s[:])
}

func TestReadBootChainHashesEachImageByItsHeader(t *testing.T) {
	t.Parallel()

	mlo := make([]byte, 0x200+0x400)
	binary.LittleEndian.PutUint32(mlo, 0x40)
	binary.LittleEndian.PutUint32(mlo[0x200:], 0x400)

	mtd0 := append(slices.Clone(mlo), make([]byte, 0x1000)...) // erased tail is not the image
	for i := len(mlo); i < len(mtd0); i++ {
		mtd0[i] = 0xff
	}

	uboot := make([]byte, 64+0x800)
	binary.BigEndian.PutUint32(uboot, uImageMagic)
	binary.BigEndian.PutUint32(uboot[12:], 0x800)
	spare := slices.Clone(uboot)
	spare[100] = 1 // a different spare
	mtd4 := make([]byte, 2*SecondUBootOffset)
	copy(mtd4, uboot)
	copy(mtd4[SecondUBootOffset:], spare)

	got := ReadBootChain(fakeNAND(map[string][]byte{"/dev/mtd0": mtd0, "/dev/mtd4": mtd4}))

	want := BootChain{SPL: sum(mlo), UBoot: sum(uboot), UBootSpare: sum(spare)}
	if got != want {
		t.Errorf("ReadBootChain = %+v, want %+v", got, want)
	}
}

func TestReadBootChainLeavesWhatDoesNotParseEmpty(t *testing.T) {
	t.Parallel()

	erased := make([]byte, 2*SecondUBootOffset)
	for i := range erased {
		erased[i] = 0xff
	}

	empty := BootChain{SPL: "", UBoot: "", UBootSpare: ""}

	got := ReadBootChain(fakeNAND(map[string][]byte{"/dev/mtd0": erased[:128<<10], "/dev/mtd4": erased}))
	if got != empty {
		t.Errorf("an erased chain hashed as %+v", got)
	}

	if got := ReadBootChain(fakeNAND(nil)); got != empty {
		t.Errorf("an unreadable chain hashed as %+v", got)
	}
}
