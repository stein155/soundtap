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
	"slices"
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
	// The EC header in front of the VID header: 64 bytes, the VID header's offset at 16 and
	// the data's at 20.
	ubiECHeaderSize = 64
	ubiECVIDOff     = 16
	ubiECDataOff    = 20
)

// The ways the image or the slot fails the follower. A message with values reads them
// around its sentinel, as it always has.
var (
	errGeometryUnknown = errors.New("the rootfs partition geometry is unknown")
	errNoECHeader      = errors.New("the rootfs image has an eraseblock without a UBI header")
	errHeaderOffsets   = errors.New("the rootfs image's UBI header offsets")
	errBadVIDHeader    = errors.New("the rootfs image has a bad UBI volume header")
	errUnknownVolume   = errors.New("has a header this check does not know")
	errChangesShape    = errors.New("changes shape partway")
	errStaticOrder     = errors.New("is not in order")
	errLEBOrder        = errors.New("is not in LEB order")
	errEndsInside      = errors.New("cannot verify the written root filesystem: the image ends inside an eraseblock")
	errNoVolume        = errors.New("cannot verify the written root filesystem: the image holds no volume")
	errShortOfLEB      = errors.New("short of LEB")
	errLEBDiffers      = errors.New("does not read back as written: LEB")
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
	digest := &rootfsDigest{peb: 0, lebs: 0, buf: nil, vols: map[uint32]*volumeDigest{}, err: nil}
	if peb <= 0 || partition < peb {
		digest.err = errGeometryUnknown

		return digest
	}

	// A partition has far fewer than 2^32 eraseblocks.
	digest.peb, digest.lebs = int(peb), uint32(partition/peb) //nolint:gosec // see above.

	return digest
}

// Write never fails: the write to NAND must not depend on this follower. What it could not
// follow is reported by verify, before the slot can be selected.
func (d *rootfsDigest) Write(chunk []byte) (int, error) {
	size := len(chunk)
	for len(chunk) > 0 && d.err == nil {
		if d.buf == nil {
			d.buf = make([]byte, 0, d.peb)
		}

		take := min(d.peb-len(d.buf), len(chunk))
		d.buf = append(d.buf, chunk[:take]...)
		chunk = chunk[take:]

		if len(d.buf) == d.peb {
			d.err = d.eraseblock(d.buf)
			d.buf = d.buf[:0]
		}
	}

	return size, nil
}

// eraseblock adds one PEB of the image to its volume's digest.
func (d *rootfsDigest) eraseblock(peb []byte) error {
	if string(peb[:len(ubiECMagic)]) != ubiECMagic {
		if bytes.Count(peb, []byte{0xff}) == len(peb) {
			return nil // an empty eraseblock: ubiformat writes it erased
		}

		return errNoECHeader
	}

	vidOff, dataOff, err := d.headerOffsets(peb)
	if err != nil {
		return err
	}

	vid := peb[vidOff : vidOff+ubiVIDHeaderSize]
	if bytes.Count(vid, []byte{0xff}) == len(vid) {
		return nil // no volume header: a free eraseblock
	}

	if string(vid[:len(ubiVIDMagic)]) != ubiVIDMagic {
		return errBadVIDHeader
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
		return fmt.Errorf("the rootfs image's volume %d %w", volID, errUnknownVolume)
	}

	volume := d.vols[volID]
	if volume == nil {
		volume = &volumeDigest{leb: leb, static: volType == ubiStatic, sums: nil}
		d.vols[volID] = volume
	}

	if volume.leb != leb || volume.static != (volType == ubiStatic) {
		return fmt.Errorf("the rootfs image's volume %d %w", volID, errChangesShape)
	}

	return volume.add(volID, lnum, dataSize, peb[dataOff:dataOff+leb])
}

// headerOffsets reads where the EC header puts the VID header and the data, and refuses
// offsets that do not fit the eraseblock.
func (d *rootfsDigest) headerOffsets(peb []byte) (int, int, error) {
	vidOff := int(binary.BigEndian.Uint32(peb[ubiECVIDOff:]))
	dataOff := int(binary.BigEndian.Uint32(peb[ubiECDataOff:]))

	if vidOff < ubiECHeaderSize || vidOff+ubiVIDHeaderSize > d.peb ||
		dataOff < vidOff+ubiVIDHeaderSize || dataOff >= d.peb {
		return 0, 0, fmt.Errorf("%w (%d, %d) do not fit a %d-byte eraseblock", errHeaderOffsets, vidOff, dataOff, d.peb)
	}

	return vidOff, dataOff, nil
}

// add records LEB lnum of the volume, whose data is the LEB's whole data area and dataSize
// what a static volume holds in it.
func (v *volumeDigest) add(volID, lnum uint32, dataSize int, data []byte) error {
	leb := len(data)

	next := uint32(len(v.sums)) //nolint:gosec // bounded by the partition's LEB count, a uint32.
	if v.static {
		// A static volume reads back as its data only, and has no holes.
		if lnum != next || dataSize > leb {
			return fmt.Errorf("the rootfs image's static volume %d %w", volID, errStaticOrder)
		}

		v.sums = append(v.sums, lebSum{crc32.ChecksumIEEE(data[:dataSize]), dataSize})

		return nil
	}

	if lnum < next {
		return fmt.Errorf("the rootfs image's volume %d %w", volID, errLEBOrder)
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

// verify reads every volume the image held back from the attached UBI device verifyUBI and
// compares it with what was streamed. Any read error, a short read or a LEB that differs
// is an error; so is an image this follower could not make sense of.
func (d *rootfsDigest) verify(open func(string) (io.ReadCloser, error)) error {
	if d.err != nil {
		return fmt.Errorf("cannot verify the written root filesystem: %w", d.err)
	}

	if len(d.buf) != 0 {
		return errEndsInside
	}

	if len(d.vols) == 0 {
		return errNoVolume
	}

	ids := make([]uint32, 0, len(d.vols))
	for id := range d.vols {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	buf := make([]byte, d.peb)
	for _, id := range ids {
		err := d.vols[id].verify(open, "/dev/ubi"+verifyUBI+"_"+strconv.FormatUint(uint64(id), 10), buf)
		if err != nil {
			return err
		}
	}

	return nil
}

// verify reads the volume at dev back LEB by LEB, as far as the image filled it: past
// that the volume is free space (and autoresize grew it on attach).
func (v *volumeDigest) verify(open func(string) (io.ReadCloser, error), dev string, buf []byte) error {
	volume, err := open(dev)
	if err != nil {
		return fmt.Errorf("reading the written root filesystem back: %w", err)
	}
	defer volume.Close()

	var off int64

	for lnum, want := range v.sums {
		got, readErr := io.ReadFull(volume, buf[:want.n])

		off += int64(got)
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			return fmt.Errorf("the written root filesystem %s reads back %d bytes, %w %d",
				dev, off, errShortOfLEB, lnum)
		}

		if readErr != nil {
			return fmt.Errorf("reading the written root filesystem back from %s, LEB %d: %w", dev, lnum, readErr)
		}

		if crc32.ChecksumIEEE(buf[:got]) != want.crc {
			return fmt.Errorf("the written root filesystem %s %w %d differs", dev, errLEBDiffers, lnum)
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

// Write feeds ubiformat, and the digest what ubiformat took.
func (s *rootfsStream) Write(chunk []byte) (int, error) {
	written, err := s.pw.Write(chunk)
	_, _ = s.digest.Write(chunk[:written])

	if err != nil {
		return written, fmt.Errorf("streaming the rootfs to ubiformat: %w", err)
	}

	return written, nil
}

// Close ends the image ubiformat reads.
func (s *rootfsStream) Close() error {
	err := s.pw.Close()
	if err != nil {
		return fmt.Errorf("closing the rootfs stream: %w", err)
	}

	return nil
}

// CloseWithError makes ubiformat's read fail with err rather than end the image early.
func (s *rootfsStream) CloseWithError(err error) error {
	closeErr := s.pw.CloseWithError(err)
	if closeErr != nil {
		return fmt.Errorf("closing the rootfs stream: %w", closeErr)
	}

	return nil
}
