#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""What a correct install of a .stu must leave in NAND, as shell variable assignments.

Used by nandsim-install-test.sh. The guest compares what the real installer wrote with
these values, which are derived here, from the package alone and without any of the
installer's Go code, so a bug in the installer cannot also hide in the expectation.

    stu_expect.py package.stu rhino binky spotty

prints, for each variant, the sha256 and length of the kernel image it must write, and
once the rootfs volume: its sha256 and length as the UBI image defines them (every
logical eraseblock in order, unmapped ones as 0xff -- what /dev/ubiN_0 returns), plus
where the rootfs module starts and how long it is, so the guest can cut an upload in
the middle of it.
"""

import hashlib
import struct
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import stu_check

PEB = stu_check.UBI_PEB
LAYOUT_VOL = 0x7FFFEFFF
# The ST300 (ginger) takes the Wireless Link's kernel (install.VariantForModel).
KERNEL_OF = {"ginger": "binky"}


def pick(found, *names):
    for name in names:
        for _header, image, size, mod in found:
            if mod == name:
                return image, size
    raise SystemExit(f"package has none of {names}")


def ubi_volume(image: bytes, vol_id: int = 0):
    """Contents of one UBI volume, rebuilt from the PEBs of a ubinize image."""
    lebs, best, reserved = {}, {}, None
    for peb in range(len(image) // PEB):
        raw = image[peb * PEB:(peb + 1) * PEB]
        if raw[:4] != b"UBI#":
            continue
        vid_off, data_off = struct.unpack_from(">II", raw, 16)
        vid = raw[vid_off:vid_off + 64]
        if vid[:4] != b"UBI!":
            continue
        vol, lnum = struct.unpack_from(">II", vid, 8)
        sqnum = struct.unpack_from(">Q", vid, 40)[0]
        if vol == LAYOUT_VOL:
            # volume table record i is 172 bytes; reserved_pebs is its first field
            if lnum == 0 and reserved is None:
                reserved = struct.unpack_from(">I", raw, data_off + vol_id * 172)[0]
            continue
        if vol != vol_id or best.get(lnum, -1) > sqnum:
            continue
        best[lnum] = sqnum
        lebs[lnum] = raw[data_off:]
    if reserved is None or not lebs:
        raise SystemExit("no UBI volume table or no data for volume 0")
    leb_size = len(next(iter(lebs.values())))
    out = bytearray(b"\xff" * (reserved * leb_size))
    for lnum, data in lebs.items():
        out[lnum * leb_size:(lnum + 1) * leb_size] = data
    return bytes(out)


def main(argv):
    data = Path(argv[0]).read_bytes()
    found = stu_check.modules(data)
    for variant in argv[1:]:
        start, size = pick(found, f"soundtap-kernel.{variant}",
                           f"soundtap-kernel.{KERNEL_OF.get(variant, variant)}",
                           "soundtap-kernel", "linuxPatched")
        kernel = data[start:start + size - stu_check.OVERLAP]
        print(f"EXP_KERNEL_LEN_{variant}={len(kernel)}")
        print(f"EXP_KERNEL_SHA_{variant}={hashlib.sha256(kernel).hexdigest()}")
    start, size = pick(found, "soundtap-rootfs", "ubi.img")
    # The installer hands ubiformat the module or the module minus the overlap,
    # whichever is a whole number of eraseblocks (install.UBIBodySize).
    body = size if size % PEB == 0 else size - stu_check.OVERLAP
    assert body % PEB == 0, "rootfs module is not a whole number of eraseblocks"
    volume = ubi_volume(data[start:start + body])
    print(f"EXP_VOL_LEN={len(volume)}")
    print(f"EXP_VOL_SHA={hashlib.sha256(volume).hexdigest()}")
    print(f"EXP_ROOTFS_OFF={start}")
    print(f"EXP_ROOTFS_LEN={body}")


if __name__ == "__main__":
    main(sys.argv[1:])
