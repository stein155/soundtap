// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package stu

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"slices"
	"strings"
	"testing"
)

// build lays out a package the way os/installer/build-stu.py does: a chain of 0x138-byte
// headers whose payloads overlap the next header by 0x14 bytes, every CRC filled in
// last. It is deliberately a second implementation of the format — a parser tested
// against its own writer proves only that they agree.
func build(tb testing.TB, sizes map[string]int) []byte {
	tb.Helper()
	return buildChain(tb, Expected, sizes)
}

func buildChain(tb testing.TB, expected []string, sizes map[string]int) []byte {
	tb.Helper()
	return buildChainWith(tb, expected, sizes, nil)
}

// buildChainWith is buildChain plus the contents of chosen modules, for the tests that ask
// what the scanner reads out of a payload rather than how it walks the chain.
func buildChainWith(tb testing.TB, expected []string, sizes map[string]int, payloads map[string][]byte) []byte {
	tb.Helper()
	type placed struct {
		name         string
		header, size int
	}
	var chain []placed
	off := 0
	for _, name := range expected {
		size := sizes[name]
		if size == 0 {
			size = 0x200
		}
		chain = append(chain, placed{name, off, size})
		off = off + headerSize + size - overlap
	}
	total := chain[len(chain)-1].header + headerSize + chain[len(chain)-1].size
	buf := make([]byte, total)
	copy(buf[magicOff:], "BOSE")
	if slices.Equal(expected, ExpectedWLANative) {
		copy(buf[magicOff:], "RTCH")
	}
	// Payload bytes first: the tail of each one is about to become the next header.
	for i, m := range chain {
		for j := 0; j < m.size; j++ {
			buf[m.header+headerSize+j] = byte(i*7 + j)
		}
		// A test that cares what is inside a module puts it there through payloads;
		// everything else keeps the filler above, which is what most of these check.
		if payload, ok := payloads[m.name]; ok {
			copy(buf[m.header+headerSize:m.header+headerSize+m.size], payload)
		}
	}
	for _, m := range chain {
		h := buf[m.header : m.header+headerSize]
		copy(h[nameOff:nameOff+nameLen], make([]byte, nameLen))
		copy(h[nameOff:], m.name)
		binary.BigEndian.PutUint32(h[sizeOff:], uint32(m.size))
	}
	// CRCs only once every byte they cover is in place.
	for _, m := range chain {
		h := buf[m.header : m.header+headerSize]
		image := buf[m.header+headerSize : m.header+headerSize+m.size]
		binary.BigEndian.PutUint32(h[imageCRCOff:], crc32.ChecksumIEEE(image))
		binary.BigEndian.PutUint32(h[headerCRCOff:], crc32.ChecksumIEEE(h[overlap:headerCRCOff]))
	}
	binary.BigEndian.PutUint32(buf[pkgSizeOff:], uint32(total))
	binary.BigEndian.PutUint32(buf[pkgCRCOff:], crc32.ChecksumIEEE(buf[:pkgCRCOff]))
	// The package header is module 1's header, so its CRC has to be redone after
	// the package fields land inside the range it covers.
	first := buf[:headerSize]
	binary.BigEndian.PutUint32(first[headerCRCOff:], crc32.ChecksumIEEE(first[overlap:headerCRCOff]))
	return buf
}

func open(t *testing.T, data []byte) (*Package, error) {
	t.Helper()
	return Scan(bytes.NewReader(data), nil)
}

func TestScanAcceptsACompleteChain(t *testing.T) {
	data := build(t, nil)
	f, err := open(t, data)
	if err != nil {
		t.Fatalf("valid package rejected: %v", err)
	}
	if len(f.Modules) != len(Expected) {
		t.Fatalf("got %d modules, want %d", len(f.Modules), len(Expected))
	}
	if _, ok := f.Module("ubi.img"); !ok {
		t.Error("ubi.img not found in a chain that contains it")
	}
	// The ST10 and the Wireless Link ship the same eight modules, so a package
	// with a stock kernel matches both and names neither; the hardware decides.
	if f.Target != "" {
		t.Fatalf("target = %q, want no single target for a shared chain", f.Target)
	}
	if len(f.Targets) != 2 || f.Targets[0] != "st10" || f.Targets[1] != "wla" {
		t.Fatalf("targets = %v, want [st10 wla]", f.Targets)
	}
	ubi, _ := f.Module("ubi.img")
	if !ubi.Installable("wla") || !ubi.Installable("st10") || ubi.Installable("st20") || ubi.Installable("st300") {
		t.Fatalf("shared chain installable on the wrong models: %v", ubi.Targets)
	}
}

func TestScanAcceptsExactST20Chain(t *testing.T) {
	f, err := open(t, buildChain(t, ExpectedST20, nil))
	if err != nil {
		t.Fatalf("valid ST20 package rejected: %v", err)
	}
	if f.Target != "st20" || len(f.Modules) != len(ExpectedST20) {
		t.Fatalf("package = %+v, want exact ST20 chain", f)
	}
}

func TestScanIdentifiesExactST300Chain(t *testing.T) {
	want := []string{
		"SoftwareUpdateInstaller", "ARMUpdate", "UbootUpdate", "STP_Update",
		"linuxPatched", "ubi.img", "MLO", "u-boot.img", "ginger_1.16.7.5043495.bos",
	}
	if !slices.Equal(ExpectedST300, want) {
		t.Fatalf("ST300 chain = %v, want stock chain %v", ExpectedST300, want)
	}
	f, err := open(t, buildChain(t, ExpectedST300, nil))
	if err != nil {
		t.Fatalf("valid ST300 package rejected: %v", err)
	}
	if f.Target != "st300" || len(f.Modules) != len(ExpectedST300) {
		t.Fatalf("package = %+v, want exact ST300 identity", f)
	}
}

func TestST300ProfileMatchesStockArtifact(t *testing.T) {
	const stockArtifactUBIModuleSize = 89_653_248
	if got := RootfsLimit("st300"); got != stockArtifactUBIModuleSize {
		t.Fatalf("st300 rootfs limit = %d, want stock artifact size %d", got, stockArtifactUBIModuleSize)
	}
}

func TestProfileRegistryRequiresEarlyUniqueChains(t *testing.T) {
	bad := map[string]profile{
		"one": {modules: []string{"a", "ubi.img", "end"}, maxRootfs: 100},
		"two": {modules: []string{"a", "ubi.img", "other"}, maxRootfs: 100},
	}
	if err := validateProfileRegistry(bad); err == nil {
		t.Error("profiles that diverge after ubi.img were accepted")
	}
}

func TestOfficialStockUBISizeIsAccepted(t *testing.T) {
	s := scanner{}
	if err := s.checkSize(Module{Name: "ubi.img", Size: stockST10UBIModuleSize, Target: "st10"}); err != nil {
		t.Fatalf("official stock UBI size rejected: %v", err)
	}
	if err := s.checkSize(Module{Name: "ubi.img", Size: stockST10UBIModuleSize + 1, Target: "st10"}); err == nil {
		t.Fatal("UBI larger than official stock image accepted")
	}
}

// Every byte matters: a package is installed onto flash that has no second chance,
// so a single flipped bit anywhere must be refused rather than repaired.
func TestSingleBitCorruptionIsAlwaysRejected(t *testing.T) {
	data := build(t, nil)
	for _, at := range []int{0, 4, 0x99, headerSize, headerSize + 3, len(data) / 2, len(data) - 1} {
		bad := append([]byte(nil), data...)
		bad[at] ^= 0x01
		if _, err := open(t, bad); err == nil {
			t.Errorf("a flipped bit at %#x was accepted", at)
		}
	}
}

func TestTrailingDataIsRejected(t *testing.T) {
	data := append(build(t, nil), 0x00)
	if _, err := open(t, data); err == nil {
		t.Error("a package with a byte appended was accepted")
	}
}

func TestGarbageIsNotAPackage(t *testing.T) {
	for _, name := range []string{"empty", "html", "truncated"} {
		var data []byte
		switch name {
		case "html":
			data = []byte(strings.Repeat("<html>not your firmware</html>", 100))
		case "truncated":
			data = build(t, nil)[:1000]
		}
		if _, err := open(t, data); err == nil {
			t.Errorf("%s was accepted as a package", name)
		}
	}
}

func TestDeclaredPackageOverLimitIsRejected(t *testing.T) {
	data := build(t, nil)
	binary.BigEndian.PutUint32(data[pkgSizeOff:], MaxSize+1)
	binary.BigEndian.PutUint32(data[pkgCRCOff:], crc32.ChecksumIEEE(data[:pkgCRCOff]))
	first := data[:headerSize]
	binary.BigEndian.PutUint32(first[headerCRCOff:], crc32.ChecksumIEEE(first[overlap:headerCRCOff]))
	if _, err := open(t, data); err == nil {
		t.Fatal("package declaring more than MaxSize was accepted")
	}
}

func TestOversizedRootfsIsRejected(t *testing.T) {
	// Not built at full size — the check is on the declared module size, and a
	// 96 MiB test fixture is 96 MiB of nothing.
	data := build(t, nil)
	header := ubiImageOffset(t, data) - headerSize
	binary.BigEndian.PutUint32(data[header+sizeOff:], nandRootfsSize+1)
	if _, err := open(t, data); err == nil {
		t.Error("a rootfs larger than its partition was accepted")
	}
}

// A rootfs module is a whole number of eraseblocks only when the overlapping tail
// is counted in: 89,653,248 bytes is exactly 684 of them in the approved stock
// package, 50,724,864 exactly 387 in ours. This used to stop 20 bytes short, and
// ubiformat answers that with "error 0 (No error information)" straight after its
// scan -- reproduced on the board with 262,124 bytes against 262,144.
func TestTheRootfsSinkGetsTheWholeModule(t *testing.T) {
	data := build(t, map[string]int{"ubi.img": 0x400})
	f, err := open(t, data)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.Module("ubi.img")
	var out bytes.Buffer
	if _, err := Scan(bytes.NewReader(data), func(got Module) io.Writer {
		if got.Name == "ubi.img" {
			return &out
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if int64(out.Len()) != m.Size {
		t.Fatalf("wrote %d bytes, want the whole module (%d)", out.Len(), m.Size)
	}
	start := ubiImageOffset(t, data)
	if !bytes.Equal(out.Bytes(), data[start:start+int(m.Size)]) {
		t.Error("the staged rootfs is not the module as the package holds it")
	}
}

// A module's last 20 bytes are its own image, and the chain overlaps them with the
// next header rather than storing them twice. Writing anything else there -- this
// used to write 0xff -- truncates the module: in the approved Bose package the MLO
// and u-boot.img images end exactly at the declared module size.
func TestNonUBISinkWritesTheOverlapBytesFromThePackage(t *testing.T) {
	data := build(t, map[string]int{"linuxPatched": 0x400})
	var out bytes.Buffer
	pkg, err := Scan(bytes.NewReader(data), func(got Module) io.Writer {
		if got.Name == "linuxPatched" {
			return &out
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out.Len(), 0x400; got != want {
		t.Fatalf("wrote %d bytes, want %d", got, want)
	}
	m, ok := pkg.Module("linuxPatched")
	if !ok {
		t.Fatal("no kernel module")
	}
	start := moduleImageOffset(t, data, "linuxPatched")
	if !bytes.Equal(out.Bytes(), data[start:start+int(m.Size)]) {
		t.Fatal("the staged kernel is not the module as the package holds it")
	}
}

// ubiImageOffset walks the chain the way build() laid it out.
func ubiImageOffset(t *testing.T, data []byte) int {
	t.Helper()
	return moduleImageOffset(t, data, "ubi.img")
}

func moduleImageOffset(t *testing.T, data []byte, want string) int {
	t.Helper()
	off := 0
	for _, name := range Expected {
		size := int(binary.BigEndian.Uint32(data[off+sizeOff:]))
		if name == want {
			return off + headerSize
		}
		off += headerSize + size - overlap
	}
	t.Fatalf("no %s module", want)
	return 0
}

func TestMainlineDetectsTheKernelFlavour(t *testing.T) {
	data := build(t, nil)
	if f, err := open(t, data); err != nil || f.Mainline {
		t.Fatalf("a random kernel payload reported as mainline (err=%v)", err)
	}
	header := kernelHeader(t, data)
	image := header + headerSize
	size := int(binary.BigEndian.Uint32(data[header+sizeOff:]))
	binary.BigEndian.PutUint32(data[image:], 0x27051956)
	// The stock kernel is stamped "Linub" too — the marker that means mainline is
	// the SoundTap image name, not the signature bytes.
	copy(data[image+32:], "Linub-3.14.43+\x00")
	if f, err := open(t, restamp(data, header, image, size)); err != nil || f.Mainline {
		t.Fatalf("a stock Bose kernel reported as mainline (err=%v)", err)
	}
	copy(data[image+32:], "Linub-6.12 SoundTap st10\x00")
	f2, err := open(t, restamp(data, header, image, size))
	if err != nil {
		t.Fatal(err)
	}
	if !f2.Mainline {
		t.Error("a mainline uImage was not recognised")
	}
	if f2.KernelTarget != "st10" {
		t.Errorf("mainline target = %q, want st10", f2.KernelTarget)
	}
	copy(data[image+32:], "Linub-6.12 SoundTap binky\x00")
	f3, err := open(t, restamp(data, header, image, size))
	if err != nil || f3.KernelTarget != "wla" {
		t.Fatalf("binky kernel target = %q, err=%v", f3.KernelTarget, err)
	}
	copy(data[image+32:], "Linub-6.12 SoundTap st300\x00")
	if got := MainlineTarget(data[image : image+size]); got != "st300" {
		t.Fatalf("unproven st300 kernel target = %q, want st300 identity", got)
	}
	copy(data[image+32:], "Linub-6.12 SoundTap future\x00")
	if got := MainlineTarget(data[image : image+size]); got != "unsupported" {
		t.Fatalf("unknown SoundTap kernel target = %q, want unsupported", got)
	}
}

// restamp re-computes the CRCs an edit to a module payload invalidated.
func restamp(data []byte, header, image, size int) []byte {
	binary.BigEndian.PutUint32(data[header+imageCRCOff:], crc32.ChecksumIEEE(data[image:image+size]))
	h := data[header : header+headerSize]
	binary.BigEndian.PutUint32(h[headerCRCOff:], crc32.ChecksumIEEE(h[overlap:headerCRCOff]))
	return data
}

// kernelHeader is ubiImageOffset's sibling for the module before it.
func kernelHeader(t *testing.T, data []byte) int {
	t.Helper()
	off := 0
	for _, name := range Expected {
		if name == "linuxPatched" {
			return off
		}
		off += headerSize + int(binary.BigEndian.Uint32(data[off+sizeOff:])) - overlap
	}
	t.Fatal("no linuxPatched module")
	return 0
}

// A sink that fails is a flash that failed, not a package that is wrong. The two
// used to come back as the same error, which sends someone off to re-download a
// file that was never the problem.
func TestSinkFailureIsNotAPackageError(t *testing.T) {
	data := build(t, nil)
	_, err := Scan(bytes.NewReader(data), func(m Module) io.Writer {
		if m.Name == "ubi.img" {
			return failingWriter{}
		}
		return nil
	})
	if !errors.Is(err, ErrSink) {
		t.Fatalf("err = %v, want an ErrSink", err)
	}
	if errors.Is(err, ErrNotSTU) {
		t.Error("a write failure was reported as a malformed package")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// errAfter hands out its bytes and then fails, as an upload cut off after the payload.
type errAfter struct{ r *bytes.Reader }

func (e errAfter) Read(p []byte) (int, error) {
	if e.r.Len() == 0 {
		return 0, errors.New("upload transport failed before EOF")
	}
	return e.r.Read(p)
}

func TestScanRejectsAReadErrorAfterTheLastModule(t *testing.T) {
	if _, err := Scan(errAfter{bytes.NewReader(build(t, nil))}, nil); err == nil {
		t.Fatal("a package whose stream failed after the last module was accepted")
	}
}
