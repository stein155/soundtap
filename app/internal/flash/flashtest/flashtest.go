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

// UBIImage lays volume out as ubinize does a dynamic volume 0: the two-PEB layout volume,
// then one eraseblock per LEB in order, each with its EC and VID header, the last padded
// with 0xFF. peb is the eraseblock size.
func UBIImage(peb int, volume []byte) []byte {
	leb := peb - ubiDataOffset
	lebs := max(1, (len(volume)+leb-1)/leb)
	image := bytes.Repeat([]byte{0xff}, (ubiLayoutPEBs+lebs)*peb)
	for i := range ubiLayoutPEBs + lebs {
		b := image[i*peb : (i+1)*peb]
		clear(b[:64])
		clear(b[ubiVIDOffset : ubiVIDOffset+64])
		copy(b, "UBI#\x01")
		binary.BigEndian.PutUint32(b[16:], ubiVIDOffset)
		binary.BigEndian.PutUint32(b[20:], ubiDataOffset)
		vid := b[ubiVIDOffset : ubiVIDOffset+64]
		copy(vid, "UBI!\x01")
		vid[5] = 1 // dynamic
		if i < ubiLayoutPEBs {
			binary.BigEndian.PutUint32(vid[8:], 0x7fffefff) // the layout volume
			binary.BigEndian.PutUint32(vid[12:], uint32(i))
			copy(b[ubiDataOffset:], "volume table")
			continue
		}
		lnum := i - ubiLayoutPEBs
		binary.BigEndian.PutUint32(vid[12:], uint32(lnum))
		copy(b[ubiDataOffset:], volume[min(lnum*leb, len(volume)):min((lnum+1)*leb, len(volume))])
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
		dtb := make([]byte, 64)
		binary.BigEndian.PutUint32(dtb, 0xd00dfeed)
		binary.BigEndian.PutUint32(dtb[4:], uint32(len(dtb)))
		return dtb
	}
	payload := []byte("kernel payload")
	image := make([]byte, 64+len(payload))
	binary.BigEndian.PutUint32(image, 0x27051956)
	binary.BigEndian.PutUint32(image[12:], uint32(len(payload)))
	binary.BigEndian.PutUint32(image[16:], 0x80008000)
	binary.BigEndian.PutUint32(image[20:], 0x80008000)
	binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(payload))
	image[28], image[29], image[30], image[31] = 5, 2, 2, 0
	copy(image[32:], "Linux-6.12 SoundTap")
	copy(image[64:], payload)
	binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
	return image
}
