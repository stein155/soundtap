# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Copy-record geometry and checksums of a BCO app image built by bco/tools/bco-pack."""
import struct
import zlib
from pathlib import Path


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


def check(path: Path, image_offset: int) -> None:
    data = path.read_bytes()
    image_file, flash_base, image_len = struct.unpack_from("<3I", data, 0x30)
    assert image_file == image_offset
    # First record: stack, first-stage length in 8-byte units, zlib CRC32 of the first stage.
    # The first stage starts right after the record with the branch to the +0x40 stub.
    assert struct.unpack_from("<3I", data, image_offset) == (0xaaaa5555, 1, 0xa5a5a5a5)
    stack, units, first_crc = struct.unpack_from("<3I", data, image_offset + 0x0c)
    assert stack == 0x40da00
    first = data[image_offset + 0x18 : image_offset + 0x18 + units * 8]
    assert struct.unpack_from("<I", first)[0] == 0xea000008
    assert zlib.crc32(first) == first_crc
    altered = bytearray(first)
    altered[4] ^= 1
    assert zlib.crc32(altered) != first_crc
    # Past the first stage up to image +0x2000 is space padding no record covers.
    assert set(data[image_offset + 0x18 + units * 8 : image_offset + 0x2000]) == {0x20}
    assert struct.unpack_from("<I", data, image_offset + 0x1c)[0] == 0xa5a5a5a5
    dest, source, length = struct.unpack_from("<3I", data, image_offset + 0x20)
    assert dest == 0x8000
    # The app's flash section starts 0x80 before its image.
    image_flash = flash_base + 0x80
    assert source == (image_flash + 0x2000) & 0xffffff
    assert length == image_len - 0x2000
    expected = struct.unpack_from("<I", data, image_offset + 0x2c)[0]
    copied = data[image_offset + 0x2000 : image_offset + 0x2000 + length]
    assert len(copied) == length and copy_checksum(copied) == expected
    altered = bytearray(copied)
    altered[0] ^= 1
    assert copy_checksum(altered) != expected
    print(f"{path.name}: first stage {units * 8} bytes crc32 0x{first_crc:08x} OK; "
          f"flash 0x{source:x} -> RAM 0x60008000, {length} bytes, checksum 0x{expected:08x} OK")

