// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"strconv"
)

// UBI on-flash header layout, as include/uapi/mtd/ubi-media.h defines it.
const (
	ubiECMagic       = "UBI#"
	ubiVIDMagic      = "UBI!"
	ubiVIDHeaderSize = 64
	ubiDynamic       = 1
	ubiStatic        = 2
	// Volume IDs from here up are UBI's own: the layout volume holds the volume table.
	ubiInternalVolStart = 0x7fffefff
)

// rootfsDigest follows the UBI image as it streams into ubiformat and records, for each
// volume in it, what reading that volume back must return: the CRC-32 and length of every
// LEB's data in order, with a LEB the image leaves out reading as erased (0xFF).
//
// The written slot is held to that. It is taken from the bytes ubiformat was given, the
// same bytes the package's CRCs and signature cover, so it needs nothing from the build.
// Reading the volume back is the full check the stock installer and our U-Boot's USB
// recovery do: a block that did not take, or a bitflip ECC could not mend, shows as an
// I/O error or a LEB that differs, instead of a slot that boots and then fails in a library.
// A CRC per LEB, not one cryptographic digest: what it guards against is the flash, not
// the package, and it costs next to nothing on a Cortex-A8 that already hashes the whole
// package for its signature. It also says which LEB went wrong.
//
// Memory is bounded: one eraseblock of buffer, and eight bytes per LEB the partition holds.
type rootfsDigest struct {
	peb  int
	lebs uint32 // no LEB number reaches this: the partition has no more eraseblocks
	buf  []byte
	vols map[uint32]*volumeDigest
	err  error
}

// volumeDigest is what one volume must read back as, LEB by LEB.
type volumeDigest struct {
	leb    int
	static bool
	sums   []lebSum
}

type lebSum struct {
	crc uint32
	n   int
}

func newRootfsDigest(peb, partition int64) *rootfsDigest {
	d := &rootfsDigest{vols: map[uint32]*volumeDigest{}}
	if peb <= 0 || partition < peb {
		d.err = errors.New("the rootfs partition geometry is unknown")
		return d
	}
	d.peb, d.lebs = int(peb), uint32(partition/peb)
	return d
}

// Write never fails: the write to NAND must not depend on this follower. What it could not
// follow is reported by verify, before the slot can be selected.
func (d *rootfsDigest) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && d.err == nil {
		if d.buf == nil {
			d.buf = make([]byte, 0, d.peb)
		}
		take := min(d.peb-len(d.buf), len(p))
		d.buf = append(d.buf, p[:take]...)
		p = p[take:]
		if len(d.buf) == d.peb {
			d.err = d.eraseblock(d.buf)
			d.buf = d.buf[:0]
		}
	}
	return n, nil
}

// eraseblock adds one PEB of the image to its volume's digest.
func (d *rootfsDigest) eraseblock(b []byte) error {
	if string(b[:4]) != ubiECMagic {
		if bytes.Count(b, []byte{0xff}) == len(b) {
			return nil // an empty eraseblock: ubiformat writes it erased
		}
		return errors.New("the rootfs image has an eraseblock without a UBI header")
	}
	vidOff := int(binary.BigEndian.Uint32(b[16:]))
	dataOff := int(binary.BigEndian.Uint32(b[20:]))
	if vidOff < 64 || vidOff+ubiVIDHeaderSize > d.peb || dataOff < vidOff+ubiVIDHeaderSize || dataOff >= d.peb {
		return fmt.Errorf("the rootfs image's UBI header offsets (%d, %d) do not fit a %d-byte eraseblock", vidOff, dataOff, d.peb)
	}
	vid := b[vidOff : vidOff+ubiVIDHeaderSize]
	if bytes.Count(vid, []byte{0xff}) == len(vid) {
		return nil // no volume header: a free eraseblock
	}
	if string(vid[:4]) != ubiVIDMagic {
		return errors.New("the rootfs image has a bad UBI volume header")
	}
	volType := vid[5]
	volID := binary.BigEndian.Uint32(vid[8:])
	lnum := binary.BigEndian.Uint32(vid[12:])
	dataSize := int(binary.BigEndian.Uint32(vid[20:]))
	dataPad := int(binary.BigEndian.Uint32(vid[28:]))
	if volID >= ubiInternalVolStart {
		return nil // the volume table: UBI rewrites it on attach (autoresize)
	}
	leb := d.peb - dataOff - dataPad
	if leb <= 0 || (volType != ubiDynamic && volType != ubiStatic) || lnum >= d.lebs {
		return fmt.Errorf("the rootfs image's volume %d has a header this check does not know", volID)
	}
	v := d.vols[volID]
	if v == nil {
		v = &volumeDigest{leb: leb, static: volType == ubiStatic}
		d.vols[volID] = v
	}
	if v.leb != leb || v.static != (volType == ubiStatic) {
		return fmt.Errorf("the rootfs image's volume %d changes shape partway", volID)
	}
	data := b[dataOff : dataOff+leb]
	next := uint32(len(v.sums))
	if v.static {
		// A static volume reads back as its data only, and has no holes.
		if lnum != next || dataSize > leb {
			return fmt.Errorf("the rootfs image's static volume %d is not in order", volID)
		}
		v.sums = append(v.sums, lebSum{crc32.ChecksumIEEE(data[:dataSize]), dataSize})
		return nil
	}
	if lnum < next {
		return fmt.Errorf("the rootfs image's volume %d is not in LEB order", volID)
	}
	// A LEB the image skips is unmapped, and an unmapped LEB reads as erased.
	if lnum > next {
		erased := lebSum{crc32.ChecksumIEEE(bytes.Repeat([]byte{0xff}, leb)), leb}
		for range lnum - next {
			v.sums = append(v.sums, erased)
		}
	}
	v.sums = append(v.sums, lebSum{crc32.ChecksumIEEE(data), leb})
	return nil
}

// verify reads every volume the image held back from the attached UBI device ubi and
// compares it with what was streamed. Any read error, a short read or a LEB that differs
// is an error; so is an image this follower could not make sense of.
func (d *rootfsDigest) verify(open func(string) (io.ReadCloser, error), ubi string) error {
	if d.err != nil {
		return fmt.Errorf("cannot verify the written root filesystem: %w", d.err)
	}
	if len(d.buf) != 0 {
		return errors.New("cannot verify the written root filesystem: the image ends inside an eraseblock")
	}
	if len(d.vols) == 0 {
		return errors.New("cannot verify the written root filesystem: the image holds no volume")
	}
	ids := make([]uint32, 0, len(d.vols))
	for id := range d.vols {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	buf := make([]byte, d.peb)
	for _, id := range ids {
		if err := d.vols[id].verify(open, "/dev/ubi"+ubi+"_"+strconv.FormatUint(uint64(id), 10), buf); err != nil {
			return err
		}
	}
	return nil
}

// verify reads the volume at dev back LEB by LEB, as far as the image filled it: past
// that the volume is free space (and autoresize grew it on attach).
func (v *volumeDigest) verify(open func(string) (io.ReadCloser, error), dev string, buf []byte) error {
	f, err := open(dev)
	if err != nil {
		return fmt.Errorf("reading the written root filesystem back: %w", err)
	}
	defer f.Close()
	var off int64
	for lnum, want := range v.sums {
		n, err := io.ReadFull(f, buf[:want.n])
		off += int64(n)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("the written root filesystem %s reads back %d bytes, short of LEB %d", dev, off, lnum)
		}
		if err != nil {
			return fmt.Errorf("reading the written root filesystem back from %s, LEB %d: %w", dev, lnum, err)
		}
		if crc32.ChecksumIEEE(buf[:n]) != want.crc {
			return fmt.Errorf("the written root filesystem %s does not read back as written: LEB %d differs", dev, lnum)
		}
	}
	return nil
}

// rootfsStream is the writer StartRootfs returns: the pipe into ubiformat, followed by the
// digest of what ubiformat accepted.
type rootfsStream struct {
	pw     *io.PipeWriter
	digest *rootfsDigest
}

func (s *rootfsStream) Write(p []byte) (int, error) {
	n, err := s.pw.Write(p)
	_, _ = s.digest.Write(p[:n])
	return n, err
}

func (s *rootfsStream) Close() error { return s.pw.Close() }

// CloseWithError makes ubiformat's read fail with err rather than end the image early.
func (s *rootfsStream) CloseWithError(err error) error { return s.pw.CloseWithError(err) }
