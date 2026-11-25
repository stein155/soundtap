# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Copy-record geometry and checksums of a BCO app image built by bco/tools/bco-pack."""
import struct
import zlib
from pathlib import Path

# First copy record: its magic and the fill word that pads the records.
RECORD_MAGIC = 0xaaaa5555
RECORD_FILL = 0xa5a5a5a5
# The first stage's stack (it runs from SRAM there) and its first word, the branch to the +0x40 stub.
FIRST_STAGE_STACK = 0x40da00
FIRST_STAGE_BRANCH = 0xea000008
# Where the second record copies the app to.
APP_DEST = 0x8000


def copy_checksum(payload: bytes) -> int:
    """Mirror first-stage ARM verifier's 16-bit Fletcher sums (360-word blocks)."""
    a = b = 0xffff
    for start in range(0, len(payload) // 2, 360):
        for (word,) in struct.iter_unpack("<H", payload[start * 2 : min(start + 360, len(payload) // 2) * 2]):
            a += word
            b += a
        a = (a & 0xffff) + (a >> 16)
        b = (b & 0xffff) + (b >> 16)
    a = (a & 0xffff) + (a >> 16)
    b += b >> 16
    return (a | (b << 16)) & 0xffffffff


def _expect(condition: object) -> None:
    """Raise AssertionError when a check of the image does not hold."""
    if not condition:
        raise AssertionError


def check(path: Path, image_offset: int) -> None:
    """Check the copy records and checksums of the BCO image at `path` with its image at `image_offset`."""
    data = path.read_bytes()
    image_file, flash_base, image_len = struct.unpack_from("<3I", data, 0x30)
    _expect(image_file == image_offset)
    # First record: stack, first-stage length in 8-byte units, zlib CRC32 of the first stage.
    # The first stage starts right after the record with the branch to the +0x40 stub.
    _expect(struct.unpack_from("<3I", data, image_offset) == (RECORD_MAGIC, 1, RECORD_FILL))
    stack, units, first_crc = struct.unpack_from("<3I", data, image_offset + 0x0c)
    _expect(stack == FIRST_STAGE_STACK)
    first = data[image_offset + 0x18 : image_offset + 0x18 + units * 8]
    _expect(struct.unpack_from("<I", first)[0] == FIRST_STAGE_BRANCH)
    _expect(zlib.crc32(first) == first_crc)
    altered = bytearray(first)
    altered[4] ^= 1
    _expect(zlib.crc32(altered) != first_crc)
    # Past the first stage up to image +0x2000 is space padding no record covers.
    _expect(set(data[image_offset + 0x18 + units * 8 : image_offset + 0x2000]) == {0x20})
    _expect(struct.unpack_from("<I", data, image_offset + 0x1c)[0] == RECORD_FILL)
    dest, source, length = struct.unpack_from("<3I", data, image_offset + 0x20)
    _expect(dest == APP_DEST)
    # The app's flash section starts 0x80 before its image.
    image_flash = flash_base + 0x80
    _expect(source == (image_flash + 0x2000) & 0xffffff)
    _expect(length == image_len - 0x2000)
    expected = struct.unpack_from("<I", data, image_offset + 0x2c)[0]
    copied = data[image_offset + 0x2000 : image_offset + 0x2000 + length]
    _expect(len(copied) == length)
    _expect(copy_checksum(copied) == expected)
    altered = bytearray(copied)
    altered[0] ^= 1
    _expect(copy_checksum(altered) != expected)
    print(f"{path.name}: first stage {units * 8} bytes crc32 0x{first_crc:08x} OK; "
          f"flash 0x{source:x} -> RAM 0x60008000, {length} bytes, checksum 0x{expected:08x} OK")
