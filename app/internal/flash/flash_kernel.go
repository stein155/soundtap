// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// The uImage signature: five bytes at offset 32, the start of the image name.
const (
	kernelSigOff = 32
	kernelSigEnd = kernelSigOff + 5
)

// The refusals of a kernel image. A message with values reads them around its sentinel.
var (
	errKernelHeader   = errors.New("invalid uImage magic or truncated header")
	errKernelSig      = errors.New("kernel signature is")
	errKernelEmpty    = errors.New("uImage payload is empty")
	errKernelType     = errors.New("uImage type is")
	errKernelLoad     = errors.New("uImage load/entry is")
	errKernelTooLong  = errors.New("uImage payload exceeds the kernel module")
	errKernelDataCRC  = errors.New("uImage data CRC is")
	errKernelFixedCRC = errors.New("uImage header CRC after fixsig is")
)

// KernelSignature returns the five bytes at offset 32 of a uImage — the field
// the whole A/B scheme turns on.
func KernelSignature(head []byte) string {
	if len(head) < kernelSigEnd {
		return ""
	}

	return string(head[kernelSigOff:kernelSigEnd])
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
		return errKernelHeader
	}

	sig := KernelSignature(image)
	if !Bootable(sig) {
		return fmt.Errorf("%w %q, not bootable", errKernelSig, sig)
	}

	size := binary.BigEndian.Uint32(image[12:])
	if size == 0 {
		return errKernelEmpty
	}

	if image[28] != 5 || image[29] != 2 || image[30] != 2 || image[31] != 0 {
		return fmt.Errorf("%w os=%d arch=%d type=%d comp=%d, want Linux/ARM/kernel/none",
			errKernelType, image[28], image[29], image[30], image[31])
	}

	load, entry := binary.BigEndian.Uint32(image[16:]), binary.BigEndian.Uint32(image[20:])
	if load != 0x80008000 || entry != 0x80008000 {
		return fmt.Errorf("%w %08x/%08x, want 80008000/80008000", errKernelLoad, load, entry)
	}

	end := uint64(headerSize) + uint64(size)
	if end > uint64(len(image)) {
		return errKernelTooLong
	}

	if got, want := crc32.ChecksumIEEE(image[headerSize:end]), binary.BigEndian.Uint32(image[24:]); got != want {
		return fmt.Errorf("%w %08x, want %08x", errKernelDataCRC, got, want)
	}

	header := append([]byte(nil), image[:headerSize]...)
	if sig == "Linub" {
		copy(header[kernelSigOff:kernelSigEnd], "Linux")
	}

	want := binary.BigEndian.Uint32(header[4:])
	binary.BigEndian.PutUint32(header[4:], 0)

	if got := crc32.ChecksumIEEE(header); got != want {
		return fmt.Errorf("%w %08x, want %08x", errKernelFixedCRC, got, want)
	}

	return nil
}
