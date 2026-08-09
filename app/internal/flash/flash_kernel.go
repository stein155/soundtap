// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// KernelSignature returns the five bytes at offset 32 of a uImage — the field
// the whole A/B scheme turns on.
func KernelSignature(head []byte) string {
	if len(head) < 37 {
		return ""
	}
	return string(head[32:37])
}

// Bootable reports whether U-Boot would accept this kernel image. "Linux" is a
// plain image; "Linub" is what Bose's packaging leaves behind for fixsig to
// repair on the way up. "Linem" is how a slot is marked empty.
func Bootable(sig string) bool { return sig == "Linux" || sig == "Linub" }

// ValidateKernel verifies the uImage exactly as U-Boot will see it after fixsig
// changes Linub back to Linux. Note what that check proves and what it does not:
// a header CRC that matches once the name is restored says the image is intact
// and merely marked, not that it is cleared to run.
func ValidateKernel(image []byte) error {
	const headerSize = 64
	if len(image) < headerSize || binary.BigEndian.Uint32(image) != 0x27051956 {
		return errors.New("invalid uImage magic or truncated header")
	}
	sig := KernelSignature(image)
	if !Bootable(sig) {
		return fmt.Errorf("kernel signature is %q, not bootable", sig)
	}
	size := binary.BigEndian.Uint32(image[12:])
	if size == 0 {
		return errors.New("uImage payload is empty")
	}
	if image[28] != 5 || image[29] != 2 || image[30] != 2 || image[31] != 0 {
		return fmt.Errorf("uImage type is os=%d arch=%d type=%d comp=%d, want Linux/ARM/kernel/none",
			image[28], image[29], image[30], image[31])
	}
	if load, entry := binary.BigEndian.Uint32(image[16:]), binary.BigEndian.Uint32(image[20:]); load != 0x80008000 || entry != 0x80008000 {
		return fmt.Errorf("uImage load/entry is %08x/%08x, want 80008000/80008000", load, entry)
	}
	end := uint64(headerSize) + uint64(size)
	if end > uint64(len(image)) {
		return errors.New("uImage payload exceeds the kernel module")
	}
	if got, want := crc32.ChecksumIEEE(image[headerSize:end]), binary.BigEndian.Uint32(image[24:]); got != want {
		return fmt.Errorf("uImage data CRC is %08x, want %08x", got, want)
	}
	header := append([]byte(nil), image[:headerSize]...)
	if sig == "Linub" {
		copy(header[32:37], "Linux")
	}
	want := binary.BigEndian.Uint32(header[4:])
	binary.BigEndian.PutUint32(header[4:], 0)
	if got := crc32.ChecksumIEEE(header); got != want {
		return fmt.Errorf("uImage header CRC after fixsig is %08x, want %08x", got, want)
	}
	return nil
}
