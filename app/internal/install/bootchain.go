// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
)

// BootChain is the boot chain on NAND by the SHA-256 of each image: the SPL in mtd0 and
// the two U-Boot copies in mtd4. Each image is as long as its own header says (the MLO's
// size word, the uImage header), so the sums equal those of a build's MLO and u-boot.img
// and tell, without a shell, whether an install replaced the chain. A copy that cannot be
// read or does not parse is empty.
type BootChain struct {
	SPL        string `json:"spl"`
	UBoot      string `json:"uboot"`
	UBootSpare string `json:"ubootSpare"`
}

// uImageMagic starts a legacy uImage header (ih_magic).
const uImageMagic = 0x27051956

// ReadBootChain reads the chain through read, nanddump as the installer runs it.
func ReadBootChain(read func(string, ...string) ([]byte, error)) BootChain {
	return BootChain{
		SPL:        bootSlot{0, 0, stagedSPL}.imageSum(read),
		UBoot:      bootSlot{4, 0, stagedUBoot}.imageSum(read),
		UBootSpare: bootSlot{4, SecondUBootOffset, stagedUBoot}.imageSum(read),
	}
}

// dump reads the first n bytes of the slot.
func (s bootSlot) dump(read func(string, ...string) ([]byte, error), n int) ([]byte, error) {
	args := []string{"--omitoob", "--bb=skipbad", "-l", strconv.Itoa(n)}
	if s.offset > 0 {
		args = append(args, "-s", strconv.Itoa(s.offset))
	}
	return read("nanddump", append(args, s.device())...)
}

// imageSum is the SHA-256 of the image the slot holds, or "".
func (s bootSlot) imageSum(read func(string, ...string) ([]byte, error)) string {
	const headLen = 0x210
	head, err := s.dump(read, headLen)
	if err != nil || len(head) < headLen {
		return ""
	}
	var size, limit int
	switch s.name {
	case stagedSPL:
		if binary.LittleEndian.Uint32(head) != 0x40 {
			return ""
		}
		size, limit = 0x200+int(binary.LittleEndian.Uint32(head[0x200:])), 128<<10
	default:
		if binary.BigEndian.Uint32(head) != uImageMagic {
			return ""
		}
		size, limit = 64+int(binary.BigEndian.Uint32(head[12:])), SecondUBootOffset
	}
	if size > limit {
		return ""
	}
	img, err := s.dump(read, size)
	if err != nil || len(img) != size {
		return ""
	}
	sum := sha256.Sum256(img)
	return hex.EncodeToString(sum[:])
}
