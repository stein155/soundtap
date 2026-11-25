# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The two U-Boot environment formats, read and written offline (uboot_recipes.py, the dry run).

Bose's U-Boot, and ours before the redundant environment, keep one copy: 8 KiB at the start
of NAND.U-Boot-Env, a little-endian CRC32 over the 8188 bytes after it, then NUL-separated
key=value entries. Ours keeps two copies of 32 KiB (ADR 0010): the primary at the start of
NAND.U-Boot-Env and the redundant one in the last erase block of NAND.U-Boot (flash
0x240000, NAND.U-Boot + 0x1c0000). Each copy is the CRC32, a flag byte, then the entries;
the CRC covers only the entries, and of two valid copies U-Boot boots the newer flag.

A whole environment is written as both copies: the redundant one first with flag 1, the
primary after it with flag 2. app/internal/ubootenv and os/installer/module/installer.c
write the same bytes for the same entries (app/internal/ubootenv/writers_test.go).
"""
import binascii
from pathlib import Path

LEGACY_SIZE = 0x2000
SIZE = 0x8000
REDUNDANT_OFFSET = 0x1C0000
FRESH_REDUNDANT_FLAG = 1
FRESH_PRIMARY_FLAG = 2
# An entry starting with 0xff is erased flash: the environment ends there.
ERASED = 0xFF


def _crc(body: bytes) -> bytes:
    return (binascii.crc32(body) & 0xFFFFFFFF).to_bytes(4, "little")


def _entries(body: bytes) -> list[bytes]:
    out = []
    for entry in body.split(b"\x00"):
        if not entry or entry[0] == ERASED:
            break
        out.append(entry)
    return out


def parse(data: bytes) -> tuple[list[bytes], int | None]:
    """Return (entries, flag) from a dump: flag is None for the single 8 KiB copy.

    A dump of a redundant copy is tried first, then the single copy; SystemExit when
    neither checks out.
    """
    if len(data) >= SIZE and _crc(data[5:SIZE]) == data[:4]:
        return _entries(data[5:SIZE]), data[4]
    if len(data) >= LEGACY_SIZE and _crc(data[4:LEGACY_SIZE]) == data[:4]:
        return _entries(data[4:LEGACY_SIZE]), None
    msg = f"no environment checks out in {len(data)} bytes, as one 32 KiB copy or as 8 KiB"
    raise SystemExit(msg)


def _body(entries: list[bytes], size: int) -> bytes:
    # U-Boot's env_export sorts by key: "nand_args" goes before "nand_args2" although the
    # whole entry "nand_args2=" sorts first.
    ordered = sorted(entries, key=lambda e: e.split(b"=", 1)[0])
    body = b"\x00".join(ordered) + b"\x00\x00"
    if len(body) > size:
        msg = f"environment is {len(body)} B, over the {size} B window"
        raise SystemExit(msg)
    # Zero padding, not 0xff: U-Boot zeroes its buffer before saving.
    return body + b"\x00" * (size - len(body))


def encode_legacy(entries: list[bytes]) -> bytes:
    """Return the single 8 KiB copy Bose's U-Boot reads."""
    body = _body(entries, LEGACY_SIZE - 4)
    return _crc(body) + body


def encode_copy(entries: list[bytes], flag: int) -> bytes:
    """Return one 32 KiB copy of the redundant environment."""
    body = _body(entries, SIZE - 5)
    return _crc(body) + bytes([flag]) + body


def encode_pair(entries: list[bytes]) -> tuple[bytes, bytes]:
    """Return (primary, redundant): the two copies of a whole environment."""
    return encode_copy(entries, FRESH_PRIMARY_FLAG), encode_copy(entries, FRESH_REDUNDANT_FLAG)


def write(out: str, entries: list[bytes], *, legacy: bool = False) -> list[str]:
    """Write OUT for NAND.U-Boot-Env and, for the redundant environment, OUT.redundant.

    OUT.redundant is for NAND.U-Boot + 0x1c0000. Returns the paths written, the one to
    write first first.
    """
    if legacy:
        with Path(out).open("wb") as f:
            f.write(encode_legacy(entries))
        return [out]
    primary, redundant = encode_pair(entries)
    with Path(out + ".redundant").open("wb") as f:
        f.write(redundant)
    with Path(out).open("wb") as f:
        f.write(primary)
    return [out + ".redundant", out]
