#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Independent strict STU validation, recovery comparison and UBI extraction."""

import hashlib
import json
import struct
import sys
import zlib
from collections.abc import Callable, Collection
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
NAND_GEOMETRY = json.loads((REPO / "os/board/nand-geometry.json").read_text())

HDR = 0x138
OVERLAP = 0x14
SIZE_OFF = 0x98
IMGCRC_OFF = 0x12C
HEADER_CRC_OFF = 0x134
PACKAGE_SIZE_OFF = 0x08
PACKAGE_CRC_OFF = 0x10
NAND_ROOTFS_SIZE = 0x6000000
NAND_SPL_SIZE = 0x20000
NAND_UBOOT_SIZE = 0x1E0000
MAX_KERNEL_BYTES = 4_980_736
UBI_PEB = 131_072
UBI_MIN_IO = 2_048
UBI_LEB = 126_976
UBI_VOLUME_LEBS = NAND_GEOMETRY["volume_lebs"]
UBIFS_FLG_SPACE_FIXUP = 4
UIMAGE_MAGIC = 0x27051956
UIMAGE_HEADER_SIZE = 64
# A uImage's name field, after the 32 bytes before it.
UIMAGE_NAME_OFF = 32
ERASED_BYTE = 0xFF
# The TI GP header of an MLO: size and load address.
GP_HEADER_SIZE = 8
# Where the ROM lets a GP image load: the AM335x's internal SRAM.
SRAM_START = 0x40200000
SRAM_END = 0x40310000
# The first ARM vector a raw SPL starts with.
ARM_VECTOR_LEN = 4
UBI_VOLUME_NAME = b"rootfs"
# The smallest UBIFS node: its common header and more.
UBIFS_MIN_NODE_LEN = 64
# The installer plus at least one module after it.
MIN_MODULES_WITH_PAYLOAD = 2
ST10_MODULES = [
    "SoftwareUpdateInstaller", "ARMUpdate", "UbootUpdate", "SCMReset_Update",
    "linuxPatched", "ubi.img", "MLO", "u-boot.img",
]
ST20_MODULES = [
    "SoftwareUpdateInstaller", "ARMUpdate", "CC2540_Update", "SMSC_Update",
    "UbootUpdate", "SCMReset_Update", "linuxPatched", "ubi.img",
    "SCMBLC_CC2540_6.0.18.738.bin",
    "Bootloader_CE2_Release_8MB_3.x-3_8_0-942",
    "App_BD_DMP 3.x-3_8_0-9423_ER.bcd", "MLO", "u-boot.img",
]
WLA_MODULES = ["soundtap-kernel", "soundtap-rootfs", "soundtap-spl", "soundtap-uboot"]
SOUNDTAP_MODULES = ["soundtap-installer", *WLA_MODULES]
SOUNDTAP_TRI_MODULES = [
    "soundtap-installer",
    "soundtap-kernel.rhino", "soundtap-kernel.binky", "soundtap-kernel.spotty",
    "soundtap-rootfs",
    "soundtap-spl.binky", "soundtap-uboot.binky",
    "soundtap-spl.spotty",
]
# ST10 + WLA, no ST20 payload at all; see os/installer/build-stu.py.
SOUNDTAP_DUO_MODULES = [n for n in SOUNDTAP_TRI_MODULES if not n.endswith(".spotty")]
MODEL_ROOTFS_LIMITS = {"rhino": 86_114_304, "binky": 89_653_248, "spotty": 84_279_296}
ST300_MODULES = [
    "SoftwareUpdateInstaller", "ARMUpdate", "UbootUpdate", "STP_Update",
    "linuxPatched", "ubi.img", "MLO", "u-boot.img",
    "ginger_1.16.7.5043495.bos",
]
PROFILES = {
    "st10": (ST10_MODULES, 86_114_304, 95_906_836),
    "st20": (ST20_MODULES, 84_279_296, 105_879_988),
    "wla": (WLA_MODULES, 89_653_248, 99_445_800),
    "wla-stock": (ST10_MODULES, 89_653_248, 99_445_800),
    # The package stock accepts on :17008: stock chain and magic, our installer and images.
    "wla-stock-route": (ST10_MODULES, 89_653_248, 99_445_800),
    # Stock ginger's own package, for offline identity checks. SoundTap itself installs on
    # the ST300 through the universal "soundtap" package below.
    "st300": (ST300_MODULES, 89_653_248, 102_384_736),
    # The universal package: BOSE magic because that is all stock authenticates, our own
    # module names because it never reads them, and our installer in module 0 because stock
    # takes the first module by position. One file for both install routes.
    "soundtap": (SOUNDTAP_MODULES, 89_653_248, 95_906_836),
    "soundtap-tri": (SOUNDTAP_TRI_MODULES, 84_279_296, 95_906_836),
    "soundtap-duo": (SOUNDTAP_DUO_MODULES, 86_114_304, 95_906_836),
    # Which field does stock authenticate? One changes the magic alone, the other the module
    # names alone; see docs/decisions/0008-one-universal-stu.md.
    "wla-probe-bose-magic": (WLA_MODULES, 89_653_248, 99_445_800),
    "wla-probe-rtch-names": (ST10_MODULES, 89_653_248, 99_445_800),
}

# The container magic each profile carries. Everything not named here is BOSE, except the
# native chain.
PROFILE_MAGIC = {
    "wla": b"RTCH",
    "wla-probe-rtch-names": b"RTCH",
    "wla-probe-bose-magic": b"BOSE",
}
ST10_TEMPLATE_SHA256 = "cc36bd1345ae1fce66b7ffd9186e762f07d49f271358c659a89b62550359d22e"
EXPECTED = ST10_MODULES

# A whole package or one of its images, as read from disk or sliced out of one.
Buffer = bytes | bytearray
# One module: its header offset, its image offset, its size and its name.
Module = tuple[int, int, int, str]
# A profile: its module chain, its stock rootfs module size and its stock package size.
Profile = tuple[list[str], int, int]


def be(data: Buffer, offset: int) -> int:
    """Return the big-endian 32-bit integer at offset."""
    if offset + 4 > len(data):
        msg = "truncated integer"
        raise ValueError(msg)
    return struct.unpack_from(">I", data, offset)[0]


def modules(data: Buffer) -> list[Module]:
    """Return every module of the package, in order."""
    result, header = [], 0
    while header + HDR <= len(data):
        size = be(data, header + SIZE_OFF)
        image = header + HDR
        if size == 0 or image + size > len(data):
            break
        raw_name = bytes(data[header + 0x18:header + 0x40])
        name = raw_name.split(b"\0", 1)[0].decode("ascii", "strict")
        result.append((header, image, size, name))
        header = image + size - OVERLAP
    return result


def profile_for_names(names: list[str], target: str | None = None) -> tuple[str, Profile]:
    """Return the profile whose module chain is names, the target's when given."""
    if target is not None:
        profile = PROFILES.get(target)
        if profile is None or names != profile[0]:
            msg = f"module chain mismatch for {target}: {names}"
            raise ValueError(msg)
        return target, profile
    matches = [name for name, (modules, _ubi_size, _stu_size) in PROFILES.items()
               if names == modules]
    if len(matches) != 1:
        msg = f"module chain mismatch for {target or 'supported targets'}: {names}"
        raise ValueError(msg)
    return matches[0], PROFILES[matches[0]]


def module_base_name(name: str) -> str:
    """Return the module name without its model suffix."""
    for model in ("binky", "rhino", "spotty"):
        suffix = "." + model
        if name.endswith(suffix):
            return name[:-len(suffix)]
    return name


def module_model(name: str) -> str | None:
    """Return the model a module name is for, or None when it names none."""
    suffix = name.rsplit(".", 1)[-1]
    return suffix if suffix in MODEL_ROOTFS_LIMITS else None


def validate(data: Buffer, target: str | None = None, *, allow_package_size_mismatch: bool = False) -> list[Module]:
    """Check the package's structure and every CRC, and return its modules."""
    found = modules(data)
    names = [name for _header, _image, _size, name in found]
    profile_name, (_expected, stock_ubi_module_size, stock_stu_size) = profile_for_names(
        names, target,
    )
    expected_magic = PROFILE_MAGIC.get(profile_name, b"RTCH" if profile_name == "wla" else b"BOSE")
    if data[4:8] != expected_magic:
        msg = f"package magic is {bytes(data[4:8])!r}, expected {expected_magic!r}"
        raise ValueError(msg)
    if not allow_package_size_mismatch and be(data, PACKAGE_SIZE_OFF) != len(data):
        msg = "package size mismatch"
        raise ValueError(msg)
    if be(data, PACKAGE_CRC_OFF) != zlib.crc32(data[:PACKAGE_CRC_OFF]) & 0xFFFFFFFF:
        msg = "package header CRC mismatch"
        raise ValueError(msg)
    if len(data) > stock_stu_size:
        msg = "package exceeds the Bose streamer baseline"
        raise ValueError(msg)
    if found[-1][1] + found[-1][2] != len(data):
        msg = "trailing or truncated data"
        raise ValueError(msg)
    for header, image, size, name in found:
        sparse_placeholder = (
            allow_package_size_mismatch and target in (None, "st10")
            and name == "ubi.img" and size == OVERLAP
        )
        if size <= OVERLAP and not sparse_placeholder:
            msg = f"{name}: module size does not exceed the header overlap"
            raise ValueError(msg)
        if sparse_placeholder:
            continue
        header_crc = zlib.crc32(data[header + OVERLAP:header + HEADER_CRC_OFF]) & 0xFFFFFFFF
        if be(data, header + HEADER_CRC_OFF) != header_crc:
            msg = f"{name}: header CRC mismatch"
            raise ValueError(msg)
        image_crc = zlib.crc32(data[image:image + size]) & 0xFFFFFFFF
        if be(data, header + IMGCRC_OFF) != image_crc:
            msg = f"{name}: image CRC mismatch"
            raise ValueError(msg)
        base_name = module_base_name(name)
        if base_name in ("ubi.img", "soundtap-rootfs") and size > NAND_ROOTFS_SIZE:
            msg = f"{name} exceeds the 96 MiB partition"
            raise ValueError(msg)
        rootfs_limit = MODEL_ROOTFS_LIMITS.get(module_model(name), stock_ubi_module_size)
        if base_name in ("ubi.img", "soundtap-rootfs") and size > rootfs_limit:
            msg = f"{name} exceeds the Bose streamer baseline"
            raise ValueError(msg)
    return found


def validate_recovery(stock: Buffer, candidate: Buffer, changed: Collection[str] = frozenset({"ubi.img"}),
                      target: str | None = None) -> None:
    """Check the candidate keeps every Bose module header and payload except the changed ones."""
    sparse_st10 = hashlib.sha256(stock).hexdigest() == ST10_TEMPLATE_SHA256
    stock_modules = validate(stock, target, allow_package_size_mismatch=sparse_st10)
    candidate_modules = validate(candidate, target)
    stock_target, (expected, _ubi_size, _stu_size) = profile_for_names(
        [m[3] for m in stock_modules], target,
    )
    candidate_target, _candidate_profile = profile_for_names([m[3] for m in candidate_modules], target)
    if candidate_target != stock_target:
        msg = "candidate target differs from stock recovery container"
        raise ValueError(msg)
    for index, (stock_module, candidate_module) in enumerate(zip(stock_modules, candidate_modules, strict=False)):
        stock_header, stock_image, stock_size, name = stock_module
        candidate_header, candidate_image, candidate_size, _ = candidate_module
        stock_head = bytearray(stock[stock_header:stock_header + HDR])
        candidate_head = bytearray(candidate[candidate_header:candidate_header + HDR])
        if index == 0:
            for offset in (PACKAGE_SIZE_OFF, PACKAGE_CRC_OFF, HEADER_CRC_OFF):
                stock_head[offset:offset + 4] = b"\0" * 4
                candidate_head[offset:offset + 4] = b"\0" * 4
        if name in changed:
            for offset in (SIZE_OFF, IMGCRC_OFF, HEADER_CRC_OFF):
                stock_head[offset:offset + 4] = b"\0" * 4
                candidate_head[offset:offset + 4] = b"\0" * 4
        if candidate_head != stock_head:
            msg = f"{name}: Bose module header changed"
            raise ValueError(msg)
        if name in changed:
            continue
        overlap = OVERLAP if index + 1 < len(expected) else 0
        stock_payload = stock[stock_image:stock_image + stock_size - overlap]
        candidate_payload = candidate[candidate_image:candidate_image + candidate_size - overlap]
        if stock_payload != candidate_payload:
            msg = f"{name}: Bose recovery payload changed"
            raise ValueError(msg)


def read(path: str | Path) -> bytearray:
    """Return the file's bytes."""
    return bytearray(Path(path).read_bytes())


def accept(path: str | Path, target: str | None = None) -> bool:
    """Validate the package, print each module or the reason it is refused, and return whether it passed."""
    try:
        found = validate(read(path), target)
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    for index, (_header, _image, size, name) in enumerate(found, 1):
        print(f"  module {index}: {name:26s} {size:>11,d} B  header+image CRC OK")
    print(f"ACCEPT: {len(found)} modules, complete structure + all CRC32 fields valid")
    return True


def validate_same_payloads(main: Buffer, preview: Buffer, target: str | None = None) -> None:
    """Check the preview package differs from the main one in module 0 alone.

    The preview package (ADR 0133) is the universal package with module 0, the installer,
    swapped for the preview build: the same module chain, and every byte from module 1's
    header to the end the main package's. Headers carry no offsets, so a longer or shorter
    installer only moves them.
    """
    main_modules = validate(main, target)
    preview_modules = validate(preview, target)
    if [m[3] for m in main_modules] != [m[3] for m in preview_modules]:
        msg = "the two packages carry different module chains"
        raise ValueError(msg)
    if len(main_modules) < MIN_MODULES_WITH_PAYLOAD:
        msg = "no module after the installer"
        raise ValueError(msg)
    if main[main_modules[1][0]:] != preview[preview_modules[1][0]:]:
        msg = "a module after the installer differs from the main package's"
        raise ValueError(msg)


def same_payloads(main_path: str | Path, preview_path: str | Path, target: str | None = None) -> bool:
    """Compare the two packages, print the verdict and return whether they carry the same payloads."""
    try:
        validate_same_payloads(read(main_path), read(preview_path), target)
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    print("SAME PAYLOADS: only module 0, the installer, differs from the main package")
    return True


def recovery(candidate_path: str | Path, stock_path: str | Path, target: str | None = None) -> bool:
    """Compare the candidate with Bose's package, print the verdict and return whether only the rootfs changed."""
    try:
        validate_recovery(read(stock_path), read(candidate_path), target=target)
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    print("RECOVERY OK: every Bose payload except rootfs is unchanged")
    return True


def validate_mainline_kernel(data: Buffer, found: list[Module], target: str = "st10") -> None:
    """Check the package's kernel is our mainline uImage for the target and survives Bose's fixsig."""
    names = [module[3] for module in found]
    target, (expected, _ubi_size, _stu_size) = profile_for_names(names, target)
    kernel_name = "soundtap-kernel" if target == "wla" else "linuxPatched"
    index = expected.index(kernel_name)
    _header, image, size, _name = found[index]
    kernel = bytearray(data[image:image + size - OVERLAP])
    if len(kernel) > MAX_KERNEL_BYTES or len(kernel) < UIMAGE_HEADER_SIZE or be(kernel, 0) != UIMAGE_MAGIC:
        msg = "mainline kernel is invalid or too large"
        raise ValueError(msg)
    expected_prefix = b"Linux" if target == "wla" else b"Linub"
    if kernel[32:37] != expected_prefix:
        msg = "mainline kernel has wrong source marker"
        raise ValueError(msg)
    expected_kernel = "binky" if target in ("wla", "wla-stock") else target
    expected_name = expected_prefix + f"-6.12 SoundTap {expected_kernel}".encode()
    if kernel[32:64].rstrip(b"\0") != expected_name:
        msg = f"mainline kernel identity does not match {target}"
        raise ValueError(msg)
    logical_end = 64 + be(kernel, 12)
    if logical_end > len(kernel) or be(kernel, 24) != zlib.crc32(kernel[64:logical_end]) & 0xFFFFFFFF:
        msg = "mainline uImage data CRC or size mismatch"
        raise ValueError(msg)
    if any(byte != ERASED_BYTE for byte in kernel[logical_end:]):
        msg = "mainline uImage padding is not erased"
        raise ValueError(msg)
    repaired = bytearray(kernel[:64])
    stored_crc = be(repaired, 4)
    # Bose fixsig restores the stored Linub marker to Linux before iminfo checks CRC.
    repaired[32:37] = b"Linux"
    struct.pack_into(">I", repaired, 4, 0)
    if zlib.crc32(repaired) & 0xFFFFFFFF != stored_crc:
        msg = "mainline uImage does not survive Bose fixsig"
        raise ValueError(msg)
    # No appended device tree, and that is the requirement rather than an omission.
    # CONFIG_ARM_APPENDED_DTB=y means a tree glued to the zImage wins over the one the
    # bootloader passes, chosen/bootargs included -- so an appended tree puts the command
    # line and the root slot back inside the image, which is what stopped A/B switching
    # working on the WLA and what keeps a kernel tied to one model. The per-model tree
    # comes out of the root filesystem now: stock U-Boot loads
    # /boot/${variant}_${module_type}.dtb from the slot it selected and hands it to bootm,
    # and every root filesystem carries both trees.
    dtb = kernel.rfind(b"\xd0\r\xfe\xed", 64, logical_end)
    if dtb >= 0 and dtb + be(kernel, dtb + 4) == logical_end:
        msg = "mainline kernel still carries an appended DTB"
        raise ValueError(msg)


def validate_mlo(image: Buffer) -> None:
    """Check the MLO is a TI GP image that loads into SRAM and fits the SPL partition."""
    if len(image) < GP_HEADER_SIZE or len(image) > NAND_SPL_SIZE:
        msg = "MLO is invalid or exceeds the 128 KiB SPL partition"
        raise ValueError(msg)
    size, load = struct.unpack_from("<II", image)
    if (size != len(image) - GP_HEADER_SIZE or load & 3
            or load < SRAM_START or load + size > SRAM_END):
        msg = "MLO has an invalid TI GP/SPL header"
        raise ValueError(msg)


def validate_raw_mlo(image: Buffer) -> None:
    """Check the raw MLO starts with ARM vectors and fits the SPL partition."""
    if not ARM_VECTOR_LEN <= len(image) <= NAND_SPL_SIZE:
        msg = "raw MLO is invalid or exceeds the 128 KiB SPL partition"
        raise ValueError(msg)
    if image[:4] != b"\x0f\x00\x00\xea":
        msg = "raw MLO must start with ARM SPL vectors"
        raise ValueError(msg)


def validate_uboot(image: Buffer) -> None:
    """Check u-boot.img is a legacy uImage with valid CRCs that fits the U-Boot partition."""
    if len(image) < UIMAGE_HEADER_SIZE or len(image) > NAND_UBOOT_SIZE or be(image, 0) != UIMAGE_MAGIC:
        msg = "u-boot.img is invalid or exceeds the 1.875 MiB U-Boot partition"
        raise ValueError(msg)
    header = bytearray(image[:64])
    stored_crc = be(header, 4)
    struct.pack_into(">I", header, 4, 0)
    size = be(image, 12)
    if (size != len(image) - 64
            or zlib.crc32(header) & 0xFFFFFFFF != stored_crc
            or zlib.crc32(image[64:]) & 0xFFFFFFFF != be(image, 24)):
        msg = "u-boot.img has an invalid legacy uImage header"
        raise ValueError(msg)


def ubi_crc(chunk: Buffer) -> int:
    """Return UBI's CRC32 of chunk: zlib's, inverted."""
    return (zlib.crc32(chunk) ^ 0xFFFFFFFF) & 0xFFFFFFFF


def validate_ubi(data: Buffer) -> None:
    """Check the UBI image's headers, its volume table and the rootfs UBIFS superblock against the NAND contract."""
    if len(data) % UBI_PEB or len(data) < UBI_PEB * 3:
        msg = "ubi.img has invalid PEB alignment or length"
        raise ValueError(msg)
    validate_ubi_headers(data)
    validate_ubi_volume_table(data)
    validate_ubifs_superblock(data, ubifs_superblock(data))


def validate_ubi_headers(data: Buffer) -> None:
    """Check every PEB's EC and VID header and their CRCs."""
    for peb in range(len(data) // UBI_PEB):
        ec = peb * UBI_PEB
        if data[ec:ec + 4] != b"UBI#":
            msg = f"ubi.img PEB {peb} has no EC header"
            raise ValueError(msg)
        if (data[ec + 4] != 1 or be(data, ec + 16) != UBI_MIN_IO
                or be(data, ec + 20) != UBI_MIN_IO * 2):
            msg = "ubi.img has unsafe EC header geometry"
            raise ValueError(msg)
        if be(data, ec + 60) != ubi_crc(data[ec:ec + 60]):
            msg = f"ubi.img PEB {peb} has an invalid EC header CRC"
            raise ValueError(msg)
        vid = ec + UBI_MIN_IO
        if data[vid:vid + 4] != b"UBI!" or data[vid + 4] != 1:
            msg = f"ubi.img PEB {peb} has an invalid VID header"
            raise ValueError(msg)
        if be(data, vid + 60) != ubi_crc(data[vid:vid + 60]):
            msg = f"ubi.img PEB {peb} has an invalid VID header CRC"
            raise ValueError(msg)


def validate_ubi_volume_table(data: Buffer) -> None:
    """Check the volume table holds the one rootfs volume of the stock geometry."""
    record = UBI_MIN_IO * 2
    name_len = struct.unpack_from(">H", data, record + 14)[0]
    if (be(data, record) != UBI_VOLUME_LEBS or be(data, record + 4) != 1
            or be(data, record + 8) != 0 or data[record + 12] != 1
            or name_len != len(UBI_VOLUME_NAME) or data[record + 16:record + 22] != UBI_VOLUME_NAME
            or data[record + 144] != 1):
        msg = "rootfs UBI volume table differs from the stock geometry"
        raise ValueError(msg)
    if be(data, record + 168) != ubi_crc(data[record:record + 168]):
        msg = "rootfs UBI volume table CRC is invalid"
        raise ValueError(msg)


def ubifs_superblock(data: Buffer) -> int:
    """Return the offset of the UBIFS superblock, the data of volume 0's LEB 0."""
    for peb in range(len(data) // UBI_PEB):
        vid = peb * UBI_PEB + UBI_MIN_IO
        vol_id, lnum = struct.unpack_from(">II", data, vid + 8)
        if vol_id == 0 and lnum == 0:
            return peb * UBI_PEB + UBI_MIN_IO * 2
    msg = "rootfs UBIFS superblock is missing"
    raise ValueError(msg)


def validate_ubifs_superblock(data: Buffer, superblock: int) -> None:
    """Check the UBIFS superblock's magic, CRC, free-space fixup and LEB geometry."""
    if data[superblock:superblock + 4] != b"1\x18\x10\x06":
        msg = "rootfs UBIFS superblock magic is invalid"
        raise ValueError(msg)
    node_len = struct.unpack_from("<I", data, superblock + 16)[0]
    if (node_len < UBIFS_MIN_NODE_LEN or superblock + node_len > len(data)
            or struct.unpack_from("<I", data, superblock + 4)[0]
            != ubi_crc(data[superblock + 8:superblock + node_len])):
        msg = "rootfs UBIFS superblock CRC or length is invalid"
        raise ValueError(msg)
    flags = struct.unpack_from("<I", data, superblock + 28)[0]
    leb_cnt, max_leb_cnt = struct.unpack_from("<II", data, superblock + 40)
    if bool(flags & UBIFS_FLG_SPACE_FIXUP) != NAND_GEOMETRY["space_fixup"]:
        msg = "rootfs UBIFS free-space fixup differs from NAND contract"
        raise ValueError(msg)
    if not 0 < leb_cnt <= UBI_VOLUME_LEBS or max_leb_cnt != UBI_VOLUME_LEBS:
        msg = "rootfs UBIFS has unsafe LEB geometry"
        raise ValueError(msg)


def validate_ubi_module(candidate: Buffer, found: list[Module]) -> None:
    """Check the package's ubi.img is whole eraseblocks and a valid UBI image."""
    _ubi_header, ubi_image, ubi_size, _ubi_name = next(
        module for module in found if module[3] == "ubi.img"
    )
    if (ubi_size - OVERLAP) % UBI_PEB:
        msg = "ubi.img payload is not eraseblock-aligned"
        raise ValueError(msg)
    validate_ubi(candidate[ubi_image:ubi_image + ubi_size - OVERLAP])


def validate_recovery_mainline(stock: Buffer, candidate: Buffer, target: str) -> None:
    """Check the candidate changes only Bose's kernel and rootfs, for our mainline ones."""
    found = validate(candidate, target)
    validate_ubi_module(candidate, found)
    validate_recovery(stock, candidate, {"linuxPatched", "ubi.img"}, target)
    validate_mainline_kernel(candidate, found, target)


def recovery_mainline(candidate_path: str | Path, stock_path: str | Path, target: str = "st10") -> bool:
    """Check the candidate against Bose's package, print the verdict and return whether only kernel and rootfs changed."""
    try:
        stock = read(stock_path)
        candidate = read(candidate_path)
        validate_recovery_mainline(stock, candidate, target)
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    print("MAINLINE RECOVERY OK: only kernel/rootfs changed; Bose fixsig and USB recovery preserved")
    return True


def validate_recovery_mainline_bootloaders(stock: Buffer, candidate: Buffer, target: str) -> None:
    """Check the candidate changes only kernel, rootfs, MLO and U-Boot, and really replaces both bootloaders."""
    found = validate(candidate, target)
    validate_ubi_module(candidate, found)
    validate_recovery(
        stock, candidate, {"linuxPatched", "ubi.img", "MLO", "u-boot.img"}, target,
    )
    validate_mainline_kernel(candidate, found, target)
    payloads = {}
    for index, (_header, image, size, name) in enumerate(found):
        overlap = OVERLAP if index + 1 < len(found) else 0
        payloads[name] = candidate[image:image + size - overlap]
    validate_mlo(payloads["MLO"])
    validate_uboot(payloads["u-boot.img"])
    stock_found = validate(stock, target)
    for name in ("MLO", "u-boot.img"):
        index = PROFILES[target][0].index(name)
        _h, image, size, _name = stock_found[index]
        overlap = OVERLAP if index + 1 < len(stock_found) else 0
        if payloads[name] == stock[image:image + size - overlap]:
            msg = f"{name} was not replaced"
            raise ValueError(msg)


def recovery_mainline_bootloaders(candidate_path: str | Path, stock_path: str | Path, target: str = "wla") -> bool:
    """Check a bootloader-replacing candidate against Bose's package, print the verdict and return whether it passed."""
    # Every model may have its bootloader replaced. The Wireless Link was first
    # only because it is the one with a proven way back: its mask ROM is reachable
    # over USB, so a bad MLO is recoverable without opening anything. That is a
    # reason to prove USB recovery on a model before writing its bootloader, not a
    # reason for this checker to refuse the package.
    try:
        stock, candidate = read(stock_path), read(candidate_path)
        validate_recovery_mainline_bootloaders(stock, candidate, target)
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    print(f"{target.upper()} BOOTLOADER STU OK: only kernel/rootfs/MLO/U-Boot changed")
    return True


def extract_ubi(path: str | Path, output: str | Path, target: str | None = None) -> bool:
    """Write the package's rootfs module, without the overlap, to output and return whether it could."""
    data = read(path)
    try:
        found = validate(data, target)
    except (UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    _header, image, size, _name = next(module for module in found if module[3] in ("ubi.img", "soundtap-rootfs"))
    Path(output).write_bytes(data[image:image + size - OVERLAP])
    print(f"extracted ubi.img: {size - OVERLAP:,} B -> {output}")
    return True


def extract_kernel(path: str | Path, output: str | Path, target: str = "st10") -> bool:
    """Write the package's checked mainline kernel, without the overlap, to output and return whether it could."""
    data = read(path)
    try:
        found = validate(data, target)
        validate_mainline_kernel(data, found, target)
    except (UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    name = "soundtap-kernel" if target == "wla" else "linuxPatched"
    index = PROFILES[target][0].index(name)
    _header, image, size, _name = found[index]
    Path(output).write_bytes(data[image:image + size - OVERLAP])
    print(f"extracted {name}: {size - OVERLAP:,} B -> {output}")
    return True


# Each command: what it runs, how many paths it takes, and the target when none is given.
COMMANDS: dict[str, tuple[Callable[..., bool], int, str | None]] = {
    "accept": (accept, 1, None),
    "same-payloads": (same_payloads, 2, None),
    "recovery": (recovery, 2, None),
    "recovery-mainline": (recovery_mainline, 2, "st10"),
    "recovery-mainline-bootloaders": (recovery_mainline_bootloaders, 2, "wla"),
    "extract-ubi": (extract_ubi, 2, None),
    "extract-kernel": (extract_kernel, 2, "st10"),
}


def main(argv: list[str]) -> int:
    """Run the command argv names and return its exit code."""
    command = argv[1] if len(argv) > 1 else ""
    if command in COMMANDS:
        run, paths, default_target = COMMANDS[command]
        args = argv[2:]
        if len(args) in (paths, paths + 1):
            target = args[paths] if len(args) == paths + 1 else default_target
            return 0 if run(*args[:paths], target) else 1
    msg = ("usage: stu_check.py accept FILE [TARGET] | same-payloads FILE PREVIEW [TARGET] | recovery FILE STOCK [TARGET]"
           " | recovery-mainline[-bootloaders] FILE STOCK [TARGET] | extract-{ubi,kernel} FILE OUT [TARGET]")
    raise SystemExit(msg)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
