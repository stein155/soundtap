// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package stu

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"sort"
)

type scanner struct {
	r    io.Reader
	sink Sink
	pkg  Package

	head         []byte // the current module header
	carried      bool   // head holds the next header, read as the previous payload's tail
	pkgCRC       hash.Hash32
	mainline     bool
	kernelTarget string
	read         int64 // bytes consumed, to hold the package against its own size field
	candidates   map[string]profile
}

func (s *scanner) run() error { //nolint:gocognit,cyclop // moved unchanged
	s.head = make([]byte, headerSize)
	s.pkgCRC = crc32.NewIEEE()
	s.candidates = make(map[string]profile, len(profiles))
	maxModules := 0
	for target, candidate := range profiles {
		s.candidates[target] = candidate
		if len(candidate.modules) > maxModules {
			maxModules = len(candidate.modules)
		}
	}
	for i := 0; ; i++ {
		if i >= maxModules {
			return errors.New("module chain is longer than a package can be")
		}
		done, err := s.module(i)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}
	// A chain shared by several models leaves Target empty; the mainline kernel
	// name, when there is one, is what picks between them.
	if s.pkg.Target == "" && s.kernelTarget != "" {
		for _, target := range s.pkg.Targets {
			if target == s.kernelTarget {
				s.pkg.Target = target
			}
		}
	}
	// Length is checked against the profiles that actually survived the scan, not against
	// the profiles whose *key* happens to equal a target name. Those were the same thing
	// while every target had exactly one profile named after it; the universal chain is a
	// second profile with target "wla" and a different length, and looking up profiles["wla"]
	// then measured the wrong chain and refused a package that matched one of its own
	// candidates module for module.
	expected := 0
	for _, candidate := range s.candidates {
		if got := len(candidate.modules); got > expected {
			expected = got
		}
	}
	if expected == 0 || len(s.pkg.Modules) != expected {
		return fmt.Errorf("module chain has %d modules and no exact supported target", len(s.pkg.Modules))
	}
	if s.read != s.pkg.Size {
		return fmt.Errorf("package is %d bytes, its own header says %d", s.read, s.pkg.Size)
	}
	return nil
}

// counter is a writer that only counts, so the scanner can hold the package
// against the size field in its own header without seeking.
type counter struct{ n *int64 }

func (c counter) Write(p []byte) (int, error) {
	*c.n += int64(len(p))
	return len(p), nil
}

// module reads one header and streams one payload. It reports done once the last
// module has been consumed and the file ends exactly there.
func (s *scanner) module(i int) (done bool, err error) { //nolint:gocognit,cyclop // moved unchanged
	if !s.carried {
		if _, err := io.ReadFull(s.r, s.head); err != nil {
			return false, fmt.Errorf("%w: %w", ErrNotSTU, err)
		}
		_, _ = s.pkgCRC.Write(s.head[:pkgCRCOff])
	} else {
		// The header arrived as the tail of the previous payload; only the bytes
		// after the overlap still have to be read.
		if _, err := io.ReadFull(s.r, s.head[overlap:]); err != nil {
			return false, fmt.Errorf("%w: %w", ErrNotSTU, err)
		}
	}
	s.carried = false

	name, err := moduleName(s.head)
	if err != nil {
		return false, err
	}
	for target, candidate := range s.candidates {
		if (i == 0 && string(s.head[magicOff:magicOff+4]) != candidate.magic) || i >= len(candidate.modules) || name != candidate.modules[i] {
			delete(s.candidates, target)
		}
	}
	if len(s.candidates) == 0 {
		return false, fmt.Errorf("module %d is %q, not part of a supported exact chain", i+1, name)
	}
	if len(s.candidates) == 1 {
		for _, candidate := range s.candidates {
			s.pkg.Target = candidate.target
		}
	}
	// Native gates the bootloader phase and the mainline environment markers, and
	// the label a package wears for stock's benefit does not change whose it is.
	// A chain shared by two of our own profiles is still ours.
	s.pkg.Native = true
	for _, candidate := range s.candidates {
		if !candidate.native {
			s.pkg.Native = false
		}
	}
	// Models sharing one chain never narrow to a single name. Record them all:
	// which of them a package may be installed on is then a question about the
	// hardware in front of us, answered where that is known.
	targets := make([]string, 0, len(s.candidates))
	for _, candidate := range s.candidates {
		if !slicesContains(targets, candidate.target) {
			targets = append(targets, candidate.target)
		}
	}
	sort.Strings(targets)
	s.pkg.Targets = targets
	if got := crc32.ChecksumIEEE(s.head[overlap:headerCRCOff]); got != binary.BigEndian.Uint32(s.head[headerCRCOff:]) {
		return false, fmt.Errorf("%s: header CRC mismatch", name)
	}
	size := int64(binary.BigEndian.Uint32(s.head[sizeOff:]))
	if size <= overlap || size > MaxSize {
		return false, fmt.Errorf("%s: implausible module size %d", name, size)
	}
	m := Module{Name: name, Size: size, Target: s.pkg.Target, Targets: s.pkg.Targets,
		Native: s.pkg.Native}
	if err := s.checkSize(m); err != nil {
		return false, err
	}
	if i == 0 {
		s.pkg.Size = int64(binary.BigEndian.Uint32(s.head[pkgSizeOff:]))
		if s.pkg.Size <= 0 || s.pkg.Size > MaxSize {
			return false, fmt.Errorf("%w: implausible package size %d", ErrNotSTU, s.pkg.Size)
		}
		if got := s.pkgCRC.Sum32(); got != binary.BigEndian.Uint32(s.head[pkgCRCOff:]) {
			return false, errors.New("package header CRC mismatch")
		}
	}

	last := false
	for _, candidate := range s.candidates {
		if i == len(candidate.modules)-1 {
			last = true
		} else {
			last = false
			break
		}
	}
	if err := s.payload(m, last); err != nil {
		return false, err
	}
	s.pkg.Modules = append(s.pkg.Modules, m)
	return last, nil
}

// payload streams size bytes through the module's CRC and, unless this is the last
// module, keeps the trailing overlap as the next header.
func (s *scanner) payload(m Module, last bool) error {
	want := binary.BigEndian.Uint32(s.head[imageCRCOff:])
	sum := crc32.NewIEEE()
	body := m.Size
	if !last {
		body -= overlap
	}
	var out io.Writer
	w := io.Writer(sum)
	if s.sink != nil {
		if out = s.sink(m); out != nil {
			w = io.MultiWriter(w, sinkWriter{out})
		}
	}
	if err := s.copyN(w, body, m); err != nil {
		return err
	}
	if !last { //nolint:nestif // moved unchanged
		if _, err := io.ReadFull(s.r, s.head[:overlap]); err != nil {
			return fmt.Errorf("%w: %w", ErrNotSTU, err)
		}
		_, _ = sum.Write(s.head[:overlap])
		s.carried = true
		// Those 20 bytes are the module's own image, not padding, which is why the
		// module CRC just above covers them and why a header CRC starts at 0x14: the
		// chain overlaps, so a module's tail is read a second time as the head of the
		// next header. Writing a constant over them instead was right only by luck --
		// our own builder leaves that region zero-filled and past the end of every
		// image we ship. Bose does not: in the approved Wireless Link package the MLO
		// and u-boot.img images end exactly at the declared module size, so a
		// synthesised tail truncates the SPL and the bootloader by 12 and 20 bytes of
		// real content.
		//
		// The rootfs used to be excluded here, on the theory that ubiformat would
		// reject a 20-byte tail past the last PEB. It is the opposite: a rootfs module
		// is a whole number of eraseblocks *including* the overlap -- 89,653,248 is
		// exactly 684 of them in the stock package, 50,724,864 exactly 387 in ours --
		// and feeding ubiformat the 20-bytes-short payload makes it stop right after
		// its scan with "error 0 (No error information)". Reproduced on the board with
		// 262,124 bytes against 262,144.
		if out != nil {
			if _, err := (sinkWriter{out}).Write(s.head[:overlap]); err != nil {
				return err
			}
		}
	} else if n, err := io.Copy(io.Discard, s.r); err != nil {
		return fmt.Errorf("reading past the last module: %w", err)
	} else if n != 0 {
		return errors.New("trailing data after the last module")
	}
	if sum.Sum32() != want {
		return fmt.Errorf("%s: image CRC mismatch", m.Name)
	}
	if base := BaseModuleName(m.Name); base == modLinux || base == modKernel {
		// Recorded during the copy; see copyN. The base name, because a universal
		// package names its kernels per model and each of them is still the kernel.
		s.pkg.Mainline = s.mainline
		s.pkg.KernelTarget = s.kernelTarget
	}
	return nil
}

// sinkWriter tags the sink's failures so a flash error cannot be reported as a
// malformed package. Everything else the scanner reads from is its own.
type sinkWriter struct{ w io.Writer }

func (s sinkWriter) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("%w: %w", ErrSink, err)
	}
	return n, nil
}

// copyN copies exactly n bytes, and sniffs the uImage header of the kernel module
// on the way past — a package's kernel flavour is the one thing that decides which
// models it can be installed on, and there is no second pass to go back for it.
func (s *scanner) copyN(w io.Writer, n int64, m Module) error {
	base := BaseModuleName(m.Name)
	if (base != modLinux && base != modKernel) || n < uImageHeader {
		if got, err := io.CopyN(w, s.r, n); err != nil || got != n {
			return readError(err)
		}
		return nil
	}
	head := make([]byte, uImageHeader)
	if _, err := io.ReadFull(s.r, head); err != nil {
		return fmt.Errorf("%w: %w", ErrNotSTU, err)
	}
	s.kernelTarget = MainlineTarget(head)
	s.mainline = s.kernelTarget != ""
	if _, err := w.Write(head); err != nil {
		return err
	}
	if got, err := io.CopyN(w, s.r, n-uImageHeader); err != nil || got != n-uImageHeader {
		return readError(err)
	}
	return nil
}

// readError names the side that failed. io.CopyN reports the sink's error and the
// source's the same way, and calling both "not a package" sends someone to
// re-download a file that was never the problem.
func readError(err error) error {
	if errors.Is(err, ErrSink) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrNotSTU, err)
}

// MainlineTarget returns the exact hardware identity carried in a SoundTap uImage.
// WLA and ST10 share an STU module chain, so the model suffix is a safety boundary.
func MainlineTarget(head []byte) string {
	if len(head) < uImageHeader || binary.BigEndian.Uint32(head) != uImageMagic {
		return ""
	}
	name := head[uImageNameOff:uImageHeader]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	marker := []byte(" SoundTap ")
	i := bytes.Index(name, marker)
	if (!bytes.HasPrefix(name, []byte("Linub-")) && !bytes.HasPrefix(name, []byte("Linux-"))) || i < 0 {
		return ""
	}
	switch string(name[i+len(marker):]) {
	case modelST10:
		return modelST10
	case modelST20:
		return modelST20
	case modelBinky:
		return modelWLA
	case modelST300:
		return modelST300
	default:
		// Never let a future or malformed SoundTap image fall through to the WLA's
		// explicit stock-kernel exception.
		return "unsupported"
	}
}

func (s *scanner) checkSize(m Module) error {
	if m.Name != modUBI && m.Name != modRootfs {
		return nil
	}
	if m.Size > nandRootfsSize {
		return errors.New("ubi.img is larger than the 96 MiB rootfs partition")
	}
	// While more than one model still matches, the loosest of their ceilings is
	// all this can enforce; the exact one belongs to the hardware and is checked
	// with RootfsLimit once the model is known.
	limit := profiles[m.Target].maxRootfs
	for _, target := range m.Targets {
		if got := profiles[target].maxRootfs; got > limit {
			limit = got
		}
	}
	if limit == 0 {
		return errors.New("ubi.img appeared before the package target was established")
	}
	if m.Size > limit {
		return errors.New("ubi.img is larger than any package Bose's installer has streamed")
	}
	return nil
}

// moduleName reads the NUL-terminated ASCII name out of a module header. Anything
// outside printable ASCII means we are not reading a header at all.
func moduleName(head []byte) (string, error) {
	raw := head[nameOff : nameOff+nameLen]
	for i, b := range raw {
		if b == 0 {
			return string(raw[:i]), nil
		}
		if b < 0x20 || b > 0x7e {
			return "", fmt.Errorf("%w: module name is not ASCII", ErrNotSTU)
		}
	}
	// ST20's Bootloader_CE2_Release_8MB_3.x-3_8_0-942 occupies all 40 bytes.
	// The field is fixed-width, so a NUL is optional when every byte is printable.
	return string(raw), nil
}
