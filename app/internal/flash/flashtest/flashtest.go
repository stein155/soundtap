// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package flashtest builds the files a written slot holds, for tests that fake the flash.
package flashtest

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
)

// UBI geometry of the test images: VID headers at 2048 and data at 4096, as ubinize lays
// them out for 2 KiB pages with subpages (`-O 2048`), and the layout volume's two PEBs first.
const (
	ubiVIDOffset  = 2048
	ubiDataOffset = 4096
	ubiLayoutPEBs = 2
)

// The headers the fakes write, as the real formats lay them out: a UBI EC or VID header
// is 64 bytes; a uImage header is 64 bytes, Linux/ARM/kernel/uncompressed, loaded and
// entered at 0x80008000; a device tree starts with its magic and total size.
const (
	ubiHeaderSize   = 64
	ubiLayoutVolume = 0x7fffefff
	uImageSize      = 64
	uImageMagic     = 0x27051956
	kernelLoad      = 0x80008000
	fdtMagic        = 0xd00dfeed
	dtbSize         = 64
	kernelPayload   = "kernel payload"
)

// UBIImage lays volume out as ubinize does a dynamic volume 0: the two-PEB layout volume,
// then one eraseblock per LEB in order, each with its EC and VID header, the last padded
// with 0xFF. peb is the eraseblock size.
func UBIImage(peb int, volume []byte) []byte {
	leb := peb - ubiDataOffset
	lebs := max(1, (len(volume)+leb-1)/leb)

	image := bytes.Repeat([]byte{0xff}, (ubiLayoutPEBs+lebs)*peb)
	for index := range ubiLayoutPEBs + lebs {
		block := image[index*peb : (index+1)*peb]
		clear(block[:ubiHeaderSize])
		clear(block[ubiVIDOffset : ubiVIDOffset+ubiHeaderSize])
		copy(block, "UBI#\x01")
		binary.BigEndian.PutUint32(block[16:], ubiVIDOffset)
		binary.BigEndian.PutUint32(block[20:], ubiDataOffset)
		vid := block[ubiVIDOffset : ubiVIDOffset+ubiHeaderSize]
		copy(vid, "UBI!\x01")

		vid[5] = 1 // dynamic
		if index < ubiLayoutPEBs {
			binary.BigEndian.PutUint32(vid[8:], ubiLayoutVolume) // the layout volume
			binary.BigEndian.PutUint32(vid[12:], uint32(index))
			copy(block[ubiDataOffset:], "volume table")

			continue
		}

		lnum := index - ubiLayoutPEBs
		binary.BigEndian.PutUint32(vid[12:], uint32(lnum))
		copy(block[ubiDataOffset:], volume[min(lnum*leb, len(volume)):min((lnum+1)*leb, len(volume))])
	}

	return image
}

// UBIVolume is what reading volume 0 of an image made by UBIImage back returns: the data
// area of each of its LEBs, in order.
func UBIVolume(image []byte, peb int) []byte {
	var out []byte
	for off := ubiLayoutPEBs * peb; off+peb <= len(image); off += peb {
		out = append(out, image[off+ubiDataOffset:off+peb]...)
	}

	return out
}

// BootFile returns a valid uImage for a path ending in a uImage name and a valid device
// tree for one ending in .dtb, as flash.VerifyRootfs expects to read them back.
func BootFile(path string) []byte {
	if strings.HasSuffix(path, ".dtb") {
		dtb := make([]byte, dtbSize)
		binary.BigEndian.PutUint32(dtb, fdtMagic)
		binary.BigEndian.PutUint32(dtb[4:], dtbSize)

		return dtb
	}

	payload := []byte(kernelPayload)
	image := make([]byte, uImageSize+len(payload))
	binary.BigEndian.PutUint32(image, uImageMagic)
	binary.BigEndian.PutUint32(image[12:], uint32(len(kernelPayload)))
	binary.BigEndian.PutUint32(image[16:], kernelLoad)
	binary.BigEndian.PutUint32(image[20:], kernelLoad)
	binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(payload))
	image[28], image[29], image[30], image[31] = 5, 2, 2, 0
	copy(image[32:], "Linux-6.12 SoundTap")
	copy(image[uImageSize:], payload)
	binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:uImageSize]))

	return image
}
