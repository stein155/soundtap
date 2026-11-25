// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package stu reads and validates Bose .stu firmware packages on the speaker
// itself: the same structural + CRC32 gate the stock updater applies, ported from
// os/support/stu_check.py so an upload can be judged before anything is written.
//
// It is a single-pass streaming reader, and that is not a style choice. A package
// is ~96 MB; the speaker has 128 MB of RAM, a 38 MB persistent partition and a
// read-only rootfs, so there is nowhere to put a copy of one. Every module header
// precedes its payload and carries that payload's CRC32, so a package can be
// checked — and later written — as it arrives, which is what the stock installer
// does too.
//
// Nothing here writes to flash.
package stu

import (
	"errors"
	"io"
	"slices"

	"github.com/stein155/soundtap/app/internal/modeltable"
)

// Layout of a package. A module header is 0x138 bytes; the module that follows
// starts 0x14 bytes before the end of the previous payload, so the chain overlaps
// and the tail of every payload is really the head of the next header.
// modelST300 is the SoundTouch 300's model name.
const modelST300 = "st300"

const (
	headerSize   = 0x138
	overlap      = 0x14
	sizeOff      = 0x98
	imageCRCOff  = 0x12c
	headerCRCOff = 0x134
	nameOff      = 0x18
	nameLen      = 0x28
	pkgSizeOff   = 0x08
	pkgCRCOff    = 0x10
	magicOff     = 0x04

	// The rootfs partition is 96 MiB, and Bose's own timed streamer has only ever
	// been proven with a module this large (the model table's stock_rootfs_limit). Both are
	// rejection limits, not warnings.
	nandRootfsSize          = 0x6000000
	stockST10UBIModuleSize  = modeltable.StockRootfsLimitST10
	stockST20UBIModuleSize  = modeltable.StockRootfsLimitST20
	stockWLAUBIModuleSize   = modeltable.StockRootfsLimitWLA
	stockST300UBIModuleSize = modeltable.StockRootfsLimitST300

	// MaxSize is the largest package worth reading at all: supported module chains
	// plus their headers cannot legitimately exceed this.
	MaxSize = 120 << 20

	// The uImage header the kernel module starts with: 64 bytes, with a 32-byte
	// image name at offset 32.
	uImageHeader  = 64
	uImageNameOff = 32
	uImageMagic   = 0x27051956
)

// Module is one payload inside a package. Size includes the 0x14 overlap that
// belongs to the next module's header — Payload is what would be written.
type Module struct {
	Name    string
	Size    int64
	Target  string   // the one model the chain selects, empty if it selects several
	Targets []string // every model whose chain this package matches, sorted
	Native  bool     // repo-native RTCH chain, not stock updater chain
}

// Installable reports whether a package matching this module chain may be
// installed on the given hardware model. A chain shared by two models — the ST10
// and the Wireless Link ship the same eight — leaves the choice to the hardware.
func (m Module) Installable(model string) bool {
	if slices.Contains(m.Targets, model) {
		return true
	}

	return m.Targets == nil && m.Target == model
}

func slicesContains(values []string, want string) bool {
	return slices.Contains(values, want)
}

// Payload is the part of the module that is the module: the overlap belongs to
// the next header, and writing it would write a header into a firmware image.
func (m Module) Payload() int64 { return m.Size - overlap }

// Package is the result of a scan: what the file turned out to contain.
type Package struct {
	Size         int64
	Modules      []Module
	Target       string   // the one model the chain selects, empty if it selects several
	Targets      []string // every model whose chain this package matches, sorted
	Mainline     bool     // the kernel is a SoundTap mainline uImage, not a stock Bose one
	KernelTarget string   // model suffix from a SoundTap mainline uImage name
	Native       bool     // repo-native RTCH chain
}

// Module returns the named module.
func (p *Package) Module(name string) (Module, bool) {
	for _, m := range p.Modules {
		if m.Name == name {
			return m, true
		}
	}

	return Module{Name: "", Size: 0, Target: "", Targets: nil, Native: false}, false
}

// ErrSink means the package was fine but the writer it was being streamed into
// failed — a full disk, an ubiformat that died, a pipe nobody is reading. Telling
// this apart from a bad package is the difference between "your download is
// broken, fetch it again" and "the flash write failed, do not reboot".
var ErrSink = errors.New("writing the package payload failed")

// ErrNotSTU means the file is not a Bose update package at all — a JPEG, an HTML
// error page a download turned into, a transfer that stopped halfway.
var ErrNotSTU = errors.New("not a Bose .stu package")

// Sink is asked, for each module, where that module's payload should go. Return
// nil to discard it, which is what validating an upload does. The payload is
// only handed over as it streams past, so a sink that writes to flash writes a
// module it has not finished checking yet — see Scan.
type Sink func(m Module) io.Writer

// Overlap is how many bytes of a module's tail are stored once and read twice:
// as the end of that module's image and as the head of the next module's header.
// A caller that has to decide how much of a module is really its image needs it.
const Overlap = overlap

// Scan reads a package from src, validating every structural field and every CRC32
// as it goes, and offering each payload to sink.
//
// The order matters for anything that writes: a module's CRC can only be confirmed
// after its last byte has gone past, so a sink that flashes must treat its target
// as unverified until Scan returns without error. That is exactly why an installer
// writes the inactive slot and confirms it before making it bootable.
func Scan(src io.Reader, sink Sink) (*Package, error) {
	err := validateProfileRegistry(profiles())
	if err != nil {
		return nil, err
	}

	state := &scanner{
		r: nil, sink: sink, head: nil, carried: false, pkgCRC: nil,
		mainline: false, kernelTarget: "", read: 0, candidates: nil,
		pkg: Package{
			Size: 0, Modules: nil, Target: "", Targets: nil, Mainline: false, KernelTarget: "", Native: false,
		},
	}

	state.r = io.TeeReader(io.LimitReader(src, MaxSize+1), counter{&state.read})

	err = state.run()
	if err != nil {
		return nil, err
	}

	return &state.pkg, nil
}
