#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Independent strict STU validation, recovery comparison and UBI extraction."""

import hashlib
import json
import struct
import sys
import zlib
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


def be(data, offset):
    if offset + 4 > len(data):
        raise ValueError("truncated integer")
    return struct.unpack_from(">I", data, offset)[0]


def modules(data):
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


def profile_for_names(names, target=None):
    if target is not None:
        profile = PROFILES.get(target)
        if profile is None or names != profile[0]:
            raise ValueError(f"module chain mismatch for {target}: {names}")
        return target, profile
    matches = [name for name, (modules, _ubi_size, _stu_size) in PROFILES.items()
               if names == modules]
    if len(matches) != 1:
        raise ValueError(f"module chain mismatch for {target or 'supported targets'}: {names}")
    return matches[0], PROFILES[matches[0]]


def module_base_name(name):
    for model in ("binky", "rhino", "spotty"):
        suffix = "." + model
        if name.endswith(suffix):
            return name[:-len(suffix)]
    return name


def module_model(name):
    suffix = name.rsplit(".", 1)[-1]
    return suffix if suffix in MODEL_ROOTFS_LIMITS else None


def validate(data, target=None, allow_package_size_mismatch=False):
    found = modules(data)
    names = [name for _header, _image, _size, name in found]
    profile_name, (_expected, stock_ubi_module_size, stock_stu_size) = profile_for_names(
        names, target
    )
    expected_magic = PROFILE_MAGIC.get(profile_name, b"RTCH" if profile_name == "wla" else b"BOSE")
    if data[4:8] != expected_magic:
        raise ValueError(f"package magic is {bytes(data[4:8])!r}, expected {expected_magic!r}")
    if not allow_package_size_mismatch and be(data, PACKAGE_SIZE_OFF) != len(data):
        raise ValueError("package size mismatch")
    if be(data, PACKAGE_CRC_OFF) != zlib.crc32(data[:PACKAGE_CRC_OFF]) & 0xFFFFFFFF:
        raise ValueError("package header CRC mismatch")
    if len(data) > stock_stu_size:
        raise ValueError("package exceeds the Bose streamer baseline")
    if found[-1][1] + found[-1][2] != len(data):
        raise ValueError("trailing or truncated data")
    for header, image, size, name in found:
        sparse_placeholder = (
            allow_package_size_mismatch and target in (None, "st10")
            and name == "ubi.img" and size == OVERLAP
        )
        if size <= OVERLAP and not sparse_placeholder:
            raise ValueError(f"{name}: module size does not exceed the header overlap")
        if sparse_placeholder:
            continue
        header_crc = zlib.crc32(data[header + OVERLAP:header + HEADER_CRC_OFF]) & 0xFFFFFFFF
        if be(data, header + HEADER_CRC_OFF) != header_crc:
            raise ValueError(f"{name}: header CRC mismatch")
        image_crc = zlib.crc32(data[image:image + size]) & 0xFFFFFFFF
        if be(data, header + IMGCRC_OFF) != image_crc:
            raise ValueError(f"{name}: image CRC mismatch")
        base_name = module_base_name(name)
        if base_name in ("ubi.img", "soundtap-rootfs") and size > NAND_ROOTFS_SIZE:
            raise ValueError(f"{name} exceeds the 96 MiB partition")
        rootfs_limit = MODEL_ROOTFS_LIMITS.get(module_model(name), stock_ubi_module_size)
        if base_name in ("ubi.img", "soundtap-rootfs") and size > rootfs_limit:
            raise ValueError(f"{name} exceeds the Bose streamer baseline")
    return found


def validate_recovery(stock, candidate, changed=frozenset({"ubi.img"}), target=None):
    sparse_st10 = hashlib.sha256(stock).hexdigest() == ST10_TEMPLATE_SHA256
    stock_modules = validate(stock, target, allow_package_size_mismatch=sparse_st10)
    candidate_modules = validate(candidate, target)
    stock_target, (expected, _ubi_size, _stu_size) = profile_for_names(
        [m[3] for m in stock_modules], target
    )
    candidate_target, _candidate_profile = profile_for_names([m[3] for m in candidate_modules], target)
    if candidate_target != stock_target:
        raise ValueError("candidate target differs from stock recovery container")
    for index, (stock_module, candidate_module) in enumerate(zip(stock_modules, candidate_modules)):
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
            raise ValueError(f"{name}: Bose module header changed")
        if name in changed:
            continue
        overlap = OVERLAP if index + 1 < len(expected) else 0
        stock_payload = stock[stock_image:stock_image + stock_size - overlap]
        candidate_payload = candidate[candidate_image:candidate_image + candidate_size - overlap]
        if stock_payload != candidate_payload:
            raise ValueError(f"{name}: Bose recovery payload changed")


def read(path):
    return bytearray(Path(path).read_bytes())


def accept(path, target=None):
    try:
        found = validate(read(path), target)
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    for index, (_header, _image, size, name) in enumerate(found, 1):
        print(f"  module {index}: {name:26s} {size:>11,d} B  header+image CRC OK")
    print(f"ACCEPT: {len(found)} modules, complete structure + all CRC32 fields valid")
    return True


def recovery(candidate_path, stock_path, target=None):
    try:
        validate_recovery(read(stock_path), read(candidate_path), target=target)
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    print("RECOVERY OK: every Bose payload except rootfs is unchanged")
    return True


def validate_mainline_kernel(data, found, target="st10"):
    names = [module[3] for module in found]
    target, (expected, _ubi_size, _stu_size) = profile_for_names(names, target)
    kernel_name = "soundtap-kernel" if target == "wla" else "linuxPatched"
    index = expected.index(kernel_name)
    _header, image, size, _name = found[index]
    kernel = bytearray(data[image:image + size - OVERLAP])
    if len(kernel) > MAX_KERNEL_BYTES or len(kernel) < 64 or be(kernel, 0) != 0x27051956:
        raise ValueError("mainline kernel is invalid or too large")
    expected_prefix = b"Linux" if target == "wla" else b"Linub"
    if kernel[32:37] != expected_prefix:
        raise ValueError("mainline kernel has wrong source marker")
    expected_kernel = "binky" if target in ("wla", "wla-stock") else target
    expected_name = expected_prefix + f"-6.12 SoundTap {expected_kernel}".encode()
    if kernel[32:64].rstrip(b"\0") != expected_name:
        raise ValueError(f"mainline kernel identity does not match {target}")
    logical_end = 64 + be(kernel, 12)
    if logical_end > len(kernel) or be(kernel, 24) != zlib.crc32(kernel[64:logical_end]) & 0xFFFFFFFF:
        raise ValueError("mainline uImage data CRC or size mismatch")
    if any(byte != 0xFF for byte in kernel[logical_end:]):
        raise ValueError("mainline uImage padding is not erased")
    repaired = bytearray(kernel[:64])
    stored_crc = be(repaired, 4)
    # Bose fixsig restores the stored Linub marker to Linux before iminfo checks CRC.
    repaired[32:37] = b"Linux"
    struct.pack_into(">I", repaired, 4, 0)
    if zlib.crc32(repaired) & 0xFFFFFFFF != stored_crc:
        raise ValueError("mainline uImage does not survive Bose fixsig")
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
        raise ValueError("mainline kernel still carries an appended DTB")


def validate_mlo(image):
    if len(image) < 8 or len(image) > NAND_SPL_SIZE:
        raise ValueError("MLO is invalid or exceeds the 128 KiB SPL partition")
    size, load = struct.unpack_from("<II", image)
    if (size != len(image) - 8 or load & 3
            or load < 0x40200000 or load + size > 0x40310000):
        raise ValueError("MLO has an invalid TI GP/SPL header")


def validate_raw_mlo(image):
    if not 4 <= len(image) <= NAND_SPL_SIZE:
        raise ValueError("raw MLO is invalid or exceeds the 128 KiB SPL partition")
    if image[:4] != b"\x0f\x00\x00\xea":
        raise ValueError("raw MLO must start with ARM SPL vectors")


def validate_uboot(image):
    if len(image) < 64 or len(image) > NAND_UBOOT_SIZE or be(image, 0) != 0x27051956:
        raise ValueError("u-boot.img is invalid or exceeds the 1.875 MiB U-Boot partition")
    header = bytearray(image[:64])
    stored_crc = be(header, 4)
    struct.pack_into(">I", header, 4, 0)
    size = be(image, 12)
    if (size != len(image) - 64
            or zlib.crc32(header) & 0xFFFFFFFF != stored_crc
            or zlib.crc32(image[64:]) & 0xFFFFFFFF != be(image, 24)):
        raise ValueError("u-boot.img has an invalid legacy uImage header")


def validate_ubi(data):
    def ubi_crc(chunk):
        return (zlib.crc32(chunk) ^ 0xFFFFFFFF) & 0xFFFFFFFF

    if len(data) % UBI_PEB or len(data) < UBI_PEB * 3:
        raise ValueError("ubi.img has invalid PEB alignment or length")
    for peb in range(len(data) // UBI_PEB):
        ec = peb * UBI_PEB
        if data[ec:ec + 4] != b"UBI#":
            raise ValueError(f"ubi.img PEB {peb} has no EC header")
        if (data[ec + 4] != 1 or be(data, ec + 16) != UBI_MIN_IO
                or be(data, ec + 20) != UBI_MIN_IO * 2):
            raise ValueError("ubi.img has unsafe EC header geometry")
        if be(data, ec + 60) != ubi_crc(data[ec:ec + 60]):
            raise ValueError(f"ubi.img PEB {peb} has an invalid EC header CRC")
        vid = ec + UBI_MIN_IO
        if data[vid:vid + 4] != b"UBI!" or data[vid + 4] != 1:
            raise ValueError(f"ubi.img PEB {peb} has an invalid VID header")
        if be(data, vid + 60) != ubi_crc(data[vid:vid + 60]):
            raise ValueError(f"ubi.img PEB {peb} has an invalid VID header CRC")
    record = UBI_MIN_IO * 2
    name_len = struct.unpack_from(">H", data, record + 14)[0]
    if (be(data, record) != UBI_VOLUME_LEBS or be(data, record + 4) != 1
            or be(data, record + 8) != 0 or data[record + 12] != 1
            or name_len != 6 or data[record + 16:record + 22] != b"rootfs"
            or data[record + 144] != 1):
        raise ValueError("rootfs UBI volume table differs from the stock geometry")
    if be(data, record + 168) != ubi_crc(data[record:record + 168]):
        raise ValueError("rootfs UBI volume table CRC is invalid")
    for peb in range(len(data) // UBI_PEB):
        vid = peb * UBI_PEB + UBI_MIN_IO
        vol_id, lnum = struct.unpack_from(">II", data, vid + 8)
        if vol_id == 0 and lnum == 0:
            superblock = peb * UBI_PEB + UBI_MIN_IO * 2
            break
    else:
        raise ValueError("rootfs UBIFS superblock is missing")
    if data[superblock:superblock + 4] != b"1\x18\x10\x06":
        raise ValueError("rootfs UBIFS superblock magic is invalid")
    node_len = struct.unpack_from("<I", data, superblock + 16)[0]
    if (node_len < 64 or superblock + node_len > len(data)
            or struct.unpack_from("<I", data, superblock + 4)[0]
            != ubi_crc(data[superblock + 8:superblock + node_len])):
        raise ValueError("rootfs UBIFS superblock CRC or length is invalid")
    flags = struct.unpack_from("<I", data, superblock + 28)[0]
    leb_cnt, max_leb_cnt = struct.unpack_from("<II", data, superblock + 40)
    if bool(flags & UBIFS_FLG_SPACE_FIXUP) != NAND_GEOMETRY["space_fixup"]:
        raise ValueError("rootfs UBIFS free-space fixup differs from NAND contract")
    if not 0 < leb_cnt <= UBI_VOLUME_LEBS or max_leb_cnt != UBI_VOLUME_LEBS:
        raise ValueError("rootfs UBIFS has unsafe LEB geometry")


def recovery_mainline(candidate_path, stock_path, target="st10"):
    try:
        stock = read(stock_path)
        candidate = read(candidate_path)
        found = validate(candidate, target)
        _ubi_header, ubi_image, ubi_size, _ubi_name = next(
            module for module in found if module[3] == "ubi.img"
        )
        if (ubi_size - OVERLAP) % UBI_PEB:
            raise ValueError("ubi.img payload is not eraseblock-aligned")
        validate_ubi(candidate[ubi_image:ubi_image + ubi_size - OVERLAP])
        validate_recovery(stock, candidate, {"linuxPatched", "ubi.img"}, target)
        validate_mainline_kernel(candidate, found, target)
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    print("MAINLINE RECOVERY OK: only kernel/rootfs changed; Bose fixsig and USB recovery preserved")
    return True


def recovery_mainline_bootloaders(candidate_path, stock_path, target="wla"):
    # Every model may have its bootloader replaced. The Wireless Link was first
    # only because it is the one with a proven way back: its mask ROM is reachable
    # over USB, so a bad MLO is recoverable without opening anything. That is a
    # reason to prove USB recovery on a model before writing its bootloader, not a
    # reason for this checker to refuse the package.
    try:
        stock, candidate = read(stock_path), read(candidate_path)
        found = validate(candidate, target)
        _ubi_header, ubi_image, ubi_size, _ubi_name = next(
            module for module in found if module[3] == "ubi.img"
        )
        if (ubi_size - OVERLAP) % UBI_PEB:
            raise ValueError("ubi.img payload is not eraseblock-aligned")
        validate_ubi(candidate[ubi_image:ubi_image + ubi_size - OVERLAP])
        validate_recovery(
            stock, candidate, {"linuxPatched", "ubi.img", "MLO", "u-boot.img"}, target
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
                raise ValueError(f"{name} was not replaced")
    except (OSError, UnicodeError, ValueError) as err:
        print(f"REJECT: {err}")
        return False
    print(f"{target.upper()} BOOTLOADER STU OK: only kernel/rootfs/MLO/U-Boot changed")
    return True


def extract_ubi(path, output, target=None):
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


def extract_kernel(path, output, target="st10"):
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


if __name__ == "__main__":
    command = sys.argv[1] if len(sys.argv) > 1 else ""
    if command == "accept" and len(sys.argv) in (3, 4):
        sys.exit(0 if accept(sys.argv[2], sys.argv[3] if len(sys.argv) == 4 else None) else 1)
    if command == "recovery" and len(sys.argv) in (4, 5):
        sys.exit(0 if recovery(sys.argv[2], sys.argv[3], sys.argv[4] if len(sys.argv) == 5 else None) else 1)
    if command == "recovery-mainline" and len(sys.argv) in (4, 5):
        sys.exit(0 if recovery_mainline(sys.argv[2], sys.argv[3], sys.argv[4] if len(sys.argv) == 5 else "st10") else 1)
    if command == "recovery-mainline-bootloaders" and len(sys.argv) in (4, 5):
        sys.exit(0 if recovery_mainline_bootloaders(sys.argv[2], sys.argv[3], sys.argv[4] if len(sys.argv) == 5 else "wla") else 1)
    if command == "extract-ubi" and len(sys.argv) in (4, 5):
        sys.exit(0 if extract_ubi(sys.argv[2], sys.argv[3], sys.argv[4] if len(sys.argv) == 5 else None) else 1)
    if command == "extract-kernel" and len(sys.argv) in (4, 5):
        sys.exit(0 if extract_kernel(sys.argv[2], sys.argv[3], sys.argv[4] if len(sys.argv) == 5 else "st10") else 1)
    sys.exit("usage: stu_check.py accept FILE [TARGET] | recovery FILE STOCK [TARGET] | recovery-mainline[-bootloaders] FILE STOCK [TARGET] | extract-{ubi,kernel} FILE OUT [TARGET]")
