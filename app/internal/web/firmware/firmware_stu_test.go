// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

// buildSTU constructs a minimal valid .stu package from the given per-module
// payload sizes. It mirrors the layout of os/installer/build-stu.py and stu_test.go's
// build() helper, so these tests stay independent of the stu package internals
// while still operating on real format bytes.
//
// Layout constants copied from internal/stu/stu.go (same values):
//
//	headerSize   = 0x138
//	overlap      = 0x14
//	sizeOff      = 0x98
//	imageCRCOff  = 0x12c
//	headerCRCOff = 0x134
//	nameOff      = 0x18
//	pkgSizeOff   = 0x08
//	pkgCRCOff    = 0x10

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash/flashtest"
	"github.com/stein155/soundtap/app/internal/stu"
)

const (
	stuHeaderSize   = 0x138
	stuOverlap      = 0x14
	stuSizeOff      = 0x98
	stuImageCRCOff  = 0x12c
	stuHeaderCRCOff = 0x134
	stuNameOff      = 0x18
	stuPkgSizeOff   = 0x08
	stuPkgCRCOff    = 0x10

	// rootfsModuleSize is a soundtap-rootfs module holding a three-eraseblock UBI image
	// (flashtest.UBIImage of one LEB) and the overlapping tail.
	rootfsModuleSize = 3*fakePEB + stuOverlap
)

// buildSTU constructs a valid .stu package. sizes[name] overrides the
// default payload size (0x200) for a named module.
func buildSTU(t *testing.T, sizes map[string]int) []byte {
	return buildSTUChain(t, stu.Expected, sizes)
}

func buildSTUChain(t *testing.T, expected []string, sizes map[string]int) []byte {
	t.Helper()
	type placed struct {
		name         string
		header, size int
	}
	var chain []placed
	off := 0
	for _, name := range expected {
		sz := sizes[name]
		if sz == 0 {
			sz = 0x200
		}
		chain = append(chain, placed{name, off, sz})
		off = off + stuHeaderSize + sz - stuOverlap
	}
	total := chain[len(chain)-1].header + stuHeaderSize + chain[len(chain)-1].size
	buf := make([]byte, total)
	copy(buf[4:8], "BOSE")
	if len(expected) == len(stu.ExpectedWLANative) && expected[0] == stu.ExpectedWLANative[0] {
		copy(buf[4:8], "RTCH")
	}
	// Fill payload bytes; the tail of each payload overlaps the next header.
	for i, m := range chain {
		for j := 0; j < m.size; j++ {
			buf[m.header+stuHeaderSize+j] = byte(i*7 + j)
		}
	}
	// A rootfs sized for a UBI image gets one, as the written slot is read back against it.
	for _, m := range chain {
		if stu.BaseModuleName(m.name) == "soundtap-rootfs" && m.size == rootfsModuleSize {
			copy(buf[m.header+stuHeaderSize:], flashtest.UBIImage(fakePEB, []byte("a root filesystem")))
		}
	}
	// Write names and sizes.
	for _, m := range chain {
		h := buf[m.header : m.header+stuHeaderSize]
		copy(h[stuNameOff:stuNameOff+0x28], make([]byte, 0x28))
		copy(h[stuNameOff:], m.name)
		binary.BigEndian.PutUint32(h[stuSizeOff:], uint32(m.size))
	}
	// Compute CRCs last (they depend on the full content being in place).
	for _, m := range chain {
		h := buf[m.header : m.header+stuHeaderSize]
		img := buf[m.header+stuHeaderSize : m.header+stuHeaderSize+m.size]
		binary.BigEndian.PutUint32(h[stuImageCRCOff:], crc32.ChecksumIEEE(img))
		binary.BigEndian.PutUint32(h[stuHeaderCRCOff:], crc32.ChecksumIEEE(h[stuOverlap:stuHeaderCRCOff]))
	}
	// Package header fields live inside the first module's header.
	binary.BigEndian.PutUint32(buf[stuPkgSizeOff:], uint32(total))
	binary.BigEndian.PutUint32(buf[stuPkgCRCOff:], crc32.ChecksumIEEE(buf[:stuPkgCRCOff]))
	// Re-seal the first module's header CRC because the package fields changed bytes it covers.
	first := buf[:stuHeaderSize]
	binary.BigEndian.PutUint32(first[stuHeaderCRCOff:], crc32.ChecksumIEEE(first[stuOverlap:stuHeaderCRCOff]))
	return buf
}

func TestStockSharedPackageReportsBothModels(t *testing.T) {
	pkg, err := stu.Scan(bytes.NewReader(buildSTU(t, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	models := (&Handler{}).describe(pkg).Models
	if len(models) != 2 || models[0] != "ST10" || models[1] != "WLA" {
		t.Fatalf("stock shared models = %q, want [ST10 WLA]", models)
	}
}

// TestFirmwareOversizedDeclarationRejected verifies that a package whose header
// declares a size larger than stu.MaxSize is refused with a 4xx, not a 500. The
// guard also caps the body reader at stu.MaxSize; this test exercises the scanner's
// own declared-size check, which fires before any payload is read.
func TestFirmwareOversizedDeclarationRejected(t *testing.T) {
	data := buildSTU(t, nil)
	// Claim a package size one byte above the limit.
	binary.BigEndian.PutUint32(data[stuPkgSizeOff:], uint32(stu.MaxSize)+1)
	// Re-seal the package CRC and then the first header CRC.
	binary.BigEndian.PutUint32(data[stuPkgCRCOff:], crc32.ChecksumIEEE(data[:stuPkgCRCOff]))
	first := data[:stuHeaderSize]
	binary.BigEndian.PutUint32(first[stuHeaderCRCOff:], crc32.ChecksumIEEE(first[stuOverlap:stuHeaderCRCOff]))

	rec := post(t, installServer(t, &fakeFlash{}), data)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("oversized declaration = %d %s, want 4xx", rec.Code, rec.Body)
	}
}

// TestFirmwareTruncatedCRCMismatch verifies that a package that has the right
// structure (passes the header CRC check) but contains corrupted payload bytes is
// refused with 400, not 415 (garbage) or 500 (internal error). The 400 vs 415
// distinction matters to the UI: 415 means "not a package", 400 means "recognisable
// package but failed validation". Both must never reach NAND.
func TestFirmwareTruncatedCRCMismatch(t *testing.T) {
	data := buildSTU(t, nil)
	// Flip a byte deep inside the first module's payload. The image CRC was
	// computed before the flip, so the scanner will reject it.
	data[stuHeaderSize+10] ^= 0xFF

	f := &fakeFlash{}
	rec := post(t, installServer(t, f), data)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("CRC-mismatch package = %d %s, want 4xx", rec.Code, rec.Body)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("a CRC-mismatch package still wrote to NAND: %v", f.cmds)
	}
}
