#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Assemble a SoundTap STU from repo-built payloads.

The release shape is --tri-model (os/installer/build-universal-stu.sh); --universal,
--stock-route and --self-contained are the WLA bring-up shapes. No shape reads stock bytes.

Requires: mkfs.ubifs + ubinize (mtd-utils) when it packs a rootfs itself
"""
import argparse
import hashlib
import json
import os
import shutil
import struct
import subprocess
import zlib
from collections.abc import Iterator, Sequence
from pathlib import Path
from typing import TypedDict

REPO = Path(__file__).resolve().parents[2]
NAND_GEOMETRY = json.loads((REPO / "os/board/nand-geometry.json").read_text())

# ── STU / UBI constants ────────────────────────────────────────────────────────
HDR        = 0x138
SIZE_OFF   = 0x98
IMGCRC_OFF = 0x12c
OVERLAP    = 0x14
PKG_SIZE_OFF  = 0x08
PKG_HCRC_OFF  = 0x10
PKG_HCRC2_OFF = 0x134
NAND_MTD8  = 0x6000000   # 96 MiB rootfs partition
GP_HEADER_OFFSET = 0x200
NAND_SPL = 0x20000
NAND_UBOOT = 0x1E0000

UBI_PEB      = 131072    # 128 KiB PEB
UBI_LEB      = 126976    # LEB = PEB - 2*min_io
UBI_MIN_IO   = 2048
UBI_MAX_LEBS = NAND_GEOMETRY["volume_lebs"]
UBI_VOLUME_LEBS = NAND_GEOMETRY["volume_lebs"]
UBIFS_FLG_SPACE_FIXUP = 4
UIMAGE_MAGIC = 0x27051956
UIMAGE_HEADER_SIZE = 64
INSTALLER_MIN_BYTES = 4096   # a repo-built ARM installer is never smaller than one page
MLO_MIN_DECLARED_SIZE = 4    # a GP header must declare at least one instruction
MLO_LOAD_MIN = 0x40200000    # the SRAM window the ROM loads the SPL into
MLO_LOAD_END = 0x40310000
MAX_KERNEL_BYTES = 4_980_736  # Keep the NAND builder's 256 KiB slot reserve.
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
# Offline ABI probe only. It models stock coordinator's eight names without a
# stock container or any path through the release CLI.
WLA_PROBE_MODULES = ST10_MODULES
# The universal chain: our installer first, because stock executes module 0 by position, then
# the four payloads under their own names. No Bose vocabulary anywhere in it.
SOUNDTAP_MODULES = [
    "soundtap-installer", "soundtap-kernel", "soundtap-rootfs", "soundtap-spl", "soundtap-uboot",
]
SOUNDTAP_TRI_MODULES = [
    "soundtap-installer",
    "soundtap-kernel.rhino", "soundtap-kernel.binky", "soundtap-kernel.spotty",
    "soundtap-rootfs",
    "soundtap-spl.binky", "soundtap-uboot.binky",
    "soundtap-spl.spotty",
]
# ST10 and WLA only: the ST20 kernel is a separate build, and a package that carries no spotty
# payload cannot be installed on one by accident. Same shape as the tri package otherwise.
SOUNDTAP_DUO_MODULES = [name for name in SOUNDTAP_TRI_MODULES if not name.endswith(".spotty")]
MODEL_ROOTFS_LIMITS = {"rhino": 86_114_304, "binky": 89_653_248, "spotty": 84_279_296}
WLA_STOCK_ORDINALS = (1, 2, 3, 4, 2, 2, 3, 3)


class Profile(TypedDict, total=False):
    """A package target: its module chain, container magic and stock size budgets."""

    modules: list[str]
    magic: bytes
    stock_sha256: str
    stock_ubi_module_size: int
    stock_ubi_bytes: int
    stock_stu_bytes: int


ByteData = bytes | bytearray
Module = tuple[int, int, int, str]

PROFILES: dict[str, Profile] = {
    "st10": {
        "modules": ST10_MODULES,
        "stock_sha256": "cc36bd1345ae1fce66b7ffd9186e762f07d49f271358c659a89b62550359d22e",
        "stock_ubi_module_size": 86_114_304,
        "stock_ubi_bytes": 86_114_304,
        "stock_stu_bytes": 95_906_836,
    },
    "st20": {
        "modules": ST20_MODULES,
        "stock_sha256": "9cdff040f7e2f4d001d1ec1cab28ce27b3606814ff60e0a45f49d5f8c5cf5ab6",
        "stock_ubi_module_size": 84_279_296,
        "stock_ubi_bytes": 84_279_296,
        "stock_stu_bytes": 105_879_988,
    },
    "wla": {
        "modules": WLA_MODULES,
        "stock_ubi_module_size": 89_653_248,
        "stock_ubi_bytes": 89_653_248,
        "stock_stu_bytes": 99_445_800,
    },
    # The package stock Bose firmware itself accepts on :17008: the stock module chain and
    # container magic, carrying our own installer in module 1 and our own images after it.
    # The 2026-09-13 capture (os/tests/fixtures/wla-stock-17008-handshake-20260913.txt) is what proved this shape works.
    "wla-stock-route": {
        "modules": ST10_MODULES,
        "stock_ubi_module_size": 89_653_248,
        "stock_ubi_bytes": 89_653_248,
        "stock_stu_bytes": 99_445_800,
    },
    # One package for every route. Stock authenticates the four magic bytes at 0x004 and
    # nothing else about the shape -- it takes module 0 by position, not by name -- so a
    # package may wear BOSE and still carry our own vocabulary. Module 0 is our installer;
    # the four payloads use the names the installer and internal/stu both read.
    # See docs/decisions/0008-one-universal-stu.md.
    "soundtap": {
        "modules": SOUNDTAP_MODULES,
        "magic": b"BOSE",
        "stock_ubi_module_size": 89_653_248,
        "stock_ubi_bytes": 89_653_248,
        "stock_stu_bytes": 95_906_836,  # the ST10 budget: one file must fit the smallest
    },
    "soundtap-duo": {
        "modules": SOUNDTAP_DUO_MODULES,
        "magic": b"BOSE",
        "stock_ubi_module_size": 86_114_304,  # smallest of the two: ST10
        "stock_ubi_bytes": 86_114_304,
        "stock_stu_bytes": 95_906_836,
    },
    "soundtap-tri": {
        "modules": SOUNDTAP_TRI_MODULES,
        "magic": b"BOSE",
        "stock_ubi_module_size": 84_279_296,  # smallest per-model rootfs budget: ST20
        "stock_ubi_bytes": 84_279_296,
        "stock_stu_bytes": 95_906_836,  # one file must fit the smallest package budget: ST10
    },
    # Which field does stock's authentication actually read? Measured 2026-09-18, a native
    # RTCH package was refused with failureCode=authentication_error where the same images in
    # a BOSE container installed, and only two things differ in shape: the magic at 0x004 and
    # the name of module 0. These two targets separate them -- one changes the magic alone,
    # the other the names alone -- so a single upload each says which it is.
    # See docs/decisions/0008-one-universal-stu.md.
    "wla-probe-bose-magic": {
        "modules": WLA_MODULES,
        "magic": b"BOSE",
        "stock_ubi_module_size": 89_653_248,
        "stock_ubi_bytes": 89_653_248,
        "stock_stu_bytes": 99_445_800,
    },
    "wla-probe-rtch-names": {
        "modules": ST10_MODULES,
        "magic": b"RTCH",
        "stock_ubi_module_size": 89_653_248,
        "stock_ubi_bytes": 89_653_248,
        "stock_stu_bytes": 99_445_800,
    },
    "wla-probe": {
        "modules": WLA_PROBE_MODULES,
        "stock_ubi_module_size": 89_653_248,
        "stock_ubi_bytes": 89_653_248,
        "stock_stu_bytes": 99_445_800,
    },
    "wla-stock": {
        "modules": ST10_MODULES,
        "stock_sha256": "ecd3f316ad53292b9841c94e2f81cfa15ef81dc12ad79376514ea1d130ee61e2",
        "stock_ubi_module_size": 89_653_248,
        "stock_ubi_bytes": 89_653_248,
        "stock_stu_bytes": 99_445_800,
    },
}
# Existing callers and tests use the ST10 name; keep it as the default profile alias.
EXPECTED_MODULES = ST10_MODULES

# The image may not grow past what Bose ships. U-Boot loads update payloads at
# loadaddr=0x82000000 into 128 MiB of DRAM (`mem=128M` in nand_args), so the window from
# loadaddr to the top of RAM is exactly 96.0 MiB — minus U-Boot's own relocated code,
# malloc arena and stack at the top. The stock 27.0.6 package sits 4.5 MiB under that
# ceiling, which is not slack to spend: it is the margin the vendor sized against.
STOCK_UBI_BYTES = PROFILES["st10"]["stock_ubi_bytes"]
STOCK_STU_BYTES = PROFILES["st10"]["stock_stu_bytes"]


def _be(buf: ByteData, o: int) -> int:
    """Return the big-endian 32-bit word at offset ``o``."""
    return struct.unpack_from(">I", buf, o)[0]


def walk_modules(buf: ByteData) -> Iterator[Module]:
    """Yield (header offset, image offset, image size, name) for each module in the chain."""
    n, h = len(buf), 0
    while h + HDR <= n:
        size = _be(buf, h + SIZE_OFF)
        img  = h + HDR
        if size == 0 or img + size > n:
            break
        name = bytes(buf[h + 0x18: h + 0x18 + 40]).split(b"\0")[0].decode("latin1", "replace")
        yield (h, img, size, name)
        h = img + size - OVERLAP


def module_image_crc_ok(buf: ByteData, h: int, img: int, size: int) -> bool:
    """Return whether the module image matches the CRC its header stores."""
    return (zlib.crc32(bytes(buf[img: img + size])) & 0xFFFFFFFF) == _be(buf, h + IMGCRC_OFF)


def module_header_crc_ok(buf: ByteData, h: int) -> bool:
    """Return whether the module header matches its own header CRC."""
    return (zlib.crc32(bytes(buf[h + 0x14: h + PKG_HCRC2_OFF])) & 0xFFFFFFFF) == _be(buf, h + PKG_HCRC2_OFF)


def module_base_name(name: str) -> str:
    """Return the module name without its model suffix."""
    for model in ("binky", "rhino", "spotty"):
        suffix = "." + model
        if name.endswith(suffix):
            return name[:-len(suffix)]
    return name


def module_model(name: str) -> str | None:
    """Return the model a module name is suffixed with, or None for a shared module."""
    suffix = name.rsplit(".", 1)[-1]
    return suffix if suffix in MODEL_ROOTFS_LIMITS else None


def profile_for_modules(names: list[str], target: str | None = None) -> tuple[str, Profile]:
    """Return the target and profile whose module chain is ``names``, or raise."""
    if target is not None:
        profile = PROFILES.get(target)
        if profile is None or names != profile["modules"]:
            msg = f"module chain mismatch for {target}: {names}"
            raise RuntimeError(msg)
        return target, profile
    matches = [name for name, profile in PROFILES.items() if names == profile["modules"]]
    if len(matches) != 1:
        msg = f"module chain mismatch for {target or 'supported targets'}: {names}"
        raise RuntimeError(msg)
    return matches[0], PROFILES[matches[0]]


def validate_stu(buf: ByteData, target: str | None = None) -> list[Module]:
    """Check the container, every module's CRCs and the size budgets; return the modules."""
    modules = list(walk_modules(buf))
    names = [name for _h, _img, _size, name in modules]
    profile_name, profile = profile_for_modules(names, target)
    expected_magic = b"RTCH" if profile_name == "wla" else b"BOSE"
    if bytes(buf[4:8]) != expected_magic:
        msg = f"package magic is {bytes(buf[4:8])!r}, expected {expected_magic!r}"
        raise RuntimeError(msg)
    if _be(buf, PKG_SIZE_OFF) != len(buf):
        msg = "package size mismatch"
        raise RuntimeError(msg)
    if _be(buf, PKG_HCRC_OFF) != (zlib.crc32(bytes(buf[:PKG_HCRC_OFF])) & 0xFFFFFFFF):
        msg = "package header CRC invalid"
        raise RuntimeError(msg)
    if len(buf) > profile["stock_stu_bytes"]:
        msg = (
            f"package {len(buf):,} B exceeds Bose streamer baseline "
            f"{profile['stock_stu_bytes']:,} B"
        )
        raise RuntimeError(
            msg,
        )
    if modules[-1][1] + modules[-1][2] != len(buf):
        msg = "trailing or truncated package data"
        raise RuntimeError(msg)
    for h, img, size, name in modules:
        if size <= OVERLAP:
            msg = f"{name}: module size does not exceed the header overlap"
            raise RuntimeError(msg)
        if not module_header_crc_ok(buf, h):
            msg = f"{name}: header CRC invalid"
            raise RuntimeError(msg)
        if not module_image_crc_ok(buf, h, img, size):
            msg = f"{name}: image CRC invalid"
            raise RuntimeError(msg)
        base_name = module_base_name(name)
        if base_name in ("ubi.img", "soundtap-rootfs") and size > NAND_MTD8:
            msg = f"{name} {size:,} B > rootfs partition {NAND_MTD8:,} B"
            raise RuntimeError(msg)
        rootfs_limit = MODEL_ROOTFS_LIMITS.get(module_model(name), profile["stock_ubi_module_size"])
        if base_name in ("ubi.img", "soundtap-rootfs") and size > rootfs_limit:
            msg = (
                f"{name} {size:,} B exceeds rootfs baseline "
                f"{rootfs_limit:,} B"
            )
            raise RuntimeError(
                msg,
            )
    return modules


def validate_recovery_payloads(
        stock: ByteData, candidate: ByteData, changed: frozenset[str] | set[str] = frozenset({"ubi.img"}),
        target: str | None = None) -> None:
    """Require every boot/recovery payload except rootfs to remain byte-identical."""
    stock_modules = list(walk_modules(stock))
    candidate_modules = list(walk_modules(candidate))
    profile_name, _profile = profile_for_modules([module[3] for module in stock_modules], target)
    candidate_profile, _candidate = profile_for_modules(
        [module[3] for module in candidate_modules], target,
    )
    if candidate_profile != profile_name:
        msg = "candidate target differs from stock recovery container"
        raise RuntimeError(msg)
    for index, ((_, stock_img, stock_size, stock_name),
                (candidate_header, candidate_img, candidate_size, candidate_name)) in enumerate(
                    zip(stock_modules, candidate_modules, strict=False)):
        stock_header = stock_modules[index][0]
        if stock_name != candidate_name:
            msg = "recovery module order changed"
            raise RuntimeError(msg)
        stock_head = bytearray(stock[stock_header:stock_header + HDR])
        candidate_head = bytearray(candidate[candidate_header:candidate_header + HDR])
        if index == 0:
            for offset in (PKG_SIZE_OFF, PKG_HCRC_OFF, PKG_HCRC2_OFF):
                stock_head[offset:offset + 4] = b"\0" * 4
                candidate_head[offset:offset + 4] = b"\0" * 4
        if stock_name in changed:
            for offset in (SIZE_OFF, IMGCRC_OFF, PKG_HCRC2_OFF):
                stock_head[offset:offset + 4] = b"\0" * 4
                candidate_head[offset:offset + 4] = b"\0" * 4
        if candidate_head != stock_head:
            msg = f"{stock_name}: stock module header changed"
            raise RuntimeError(msg)
        if stock_name in changed:
            continue
        # The last 0x14 bytes overlap the next module header and are packaging, not payload.
        overlap = OVERLAP if index + 1 < len(stock_modules) else 0
        stock_payload = bytes(stock[stock_img:stock_img + stock_size - overlap])
        candidate_payload = bytes(candidate[candidate_img:candidate_img + candidate_size - overlap])
        if stock_payload != candidate_payload:
            msg = f"{stock_name}: stock boot/recovery payload changed"
            raise RuntimeError(msg)


def sh(cmd: Sequence[str]) -> subprocess.CompletedProcess[bytes]:
    """Run ``cmd`` and raise with its stderr when it fails."""
    r = subprocess.run(cmd, check=False, capture_output=True)
    if r.returncode:
        msg = f"{cmd[0]} failed:\n{r.stderr.decode()}"
        raise RuntimeError(msg)
    return r


def md5file(p: Path) -> str:
    """Return the MD5 of a file, printed as a transfer checksum."""
    return hashlib.md5(p.read_bytes(), usedforsecurity=False).hexdigest()


def preflight_tools(*tools: str) -> None:
    """Exit unless every named tool is on PATH."""
    missing = [t for t in tools if shutil.which(t) is None]
    if missing:
        msg = f"missing: {', '.join(missing)}  (install mtd-utils)"
        raise SystemExit(msg)


def pack_ubifs(rootfs: Path, out: Path) -> None:
    """Pack the rootfs tree into a UBIFS image, with reproducible timestamps when asked."""
    source_date_epoch = os.environ.get("SOURCE_DATE_EPOCH")
    if source_date_epoch is not None:
        try:
            epoch = int(source_date_epoch)
        except ValueError as error:
            msg = "SOURCE_DATE_EPOCH must be an integer"
            raise RuntimeError(msg) from error
        if epoch <= 0:
            msg = "SOURCE_DATE_EPOCH must be positive"
            raise RuntimeError(msg)
        for path in (rootfs, *rootfs.rglob("*")):
            os.utime(path, (epoch, epoch), follow_symlinks=False)
    device_table = out.parent / "device-table.txt"
    device_table.write_text(
        "/dev d 0755 0 0 - - - - -\n"
        "/dev/console c 0600 0 0 5 1 - - -\n"
        "/dev/null c 0666 0 0 1 3 - - -\n",
    )
    command = ["mkfs.ubifs",
               "-r", str(rootfs),
               "-m", str(UBI_MIN_IO),
               "-e", str(UBI_LEB),
               "-c", str(UBI_MAX_LEBS)]
    if NAND_GEOMETRY["space_fixup"]:
        command.append("-F")
    sh([*command, "-D", str(device_table), "-o", str(out)])
    print(f"  ubifs: {out.stat().st_size:,} B")


def pack_ubi(ubifs: Path, out: Path) -> None:
    """Wrap a UBIFS image in a one-volume UBI image and validate it."""
    cfg = ubifs.parent / "ubinize.cfg"
    cfg.write_text(
        "[rootfs]\nmode=ubi\n"
        f"image={ubifs}\n"
        f"vol_size={UBI_VOLUME_LEBS * UBI_LEB}\n"
        "vol_id=0\nvol_name=rootfs\nvol_type=dynamic\nvol_flags=autoresize\n",
    )
    sh(["ubinize", "-o", str(out),
        "-p", str(UBI_PEB), "-m", str(UBI_MIN_IO), "-s", str(UBI_MIN_IO),
        str(cfg)])
    validate_ubi(out)
    print(f"  ubi:   {out.stat().st_size:,} B")


def validate_ubi(path: Path) -> None:
    """Check the UBI image's alignment, autoresize flag, superblock and geometry."""
    data = path.read_bytes()
    if len(data) % UBI_PEB:
        msg = "UBI image is not PEB-aligned"
        raise RuntimeError(msg)
    # Volume-table record 0 starts at the data area of layout PEB 0.
    record = UBI_MIN_IO * 2
    reserved_pebs = struct.unpack_from(">I", data, record)[0]
    if data[record + 144] & 1 == 0:
        msg = "rootfs UBI volume must use stock autoresize semantics"
        raise RuntimeError(msg)
    for peb in range(len(data) // UBI_PEB):
        vid = peb * UBI_PEB + UBI_MIN_IO
        if data[vid:vid + 4] != b"UBI!":
            continue
        vol_id, lnum = struct.unpack_from(">II", data, vid + 8)
        if vol_id == 0 and lnum == 0:
            superblock = peb * UBI_PEB + UBI_MIN_IO * 2
            break
    else:
        msg = "rootfs UBIFS superblock is missing"
        raise RuntimeError(msg)
    if data[superblock:superblock + 4] != b"1\x18\x10\x06":
        msg = "rootfs UBIFS superblock magic is invalid"
        raise RuntimeError(msg)
    flags = struct.unpack_from("<I", data, superblock + 28)[0]
    if bool(flags & UBIFS_FLG_SPACE_FIXUP) != NAND_GEOMETRY["space_fixup"]:
        msg = "rootfs UBIFS free-space fixup differs from NAND contract"
        raise RuntimeError(msg)
    leb_cnt, max_leb_cnt = struct.unpack_from("<II", data, superblock + 40)
    if (reserved_pebs != UBI_VOLUME_LEBS or not 0 < leb_cnt <= reserved_pebs
            or max_leb_cnt != UBI_MAX_LEBS):
        msg = f"unsafe UBI geometry: volume={reserved_pebs}, leb_cnt={leb_cnt}, max={max_leb_cnt}"
        raise RuntimeError(
            msg,
        )


def replace_module(
        buf: bytearray, name: str, payload: bytes, *, shared_tail: bool = False,
        include_overlap: bool = False,
) -> bytearray:
    """Return a copy of the container with one module's payload replaced and its header updated."""
    modules = list(walk_modules(buf))
    index = next((i for i, module in enumerate(modules) if module[3] == name), None)
    if index is None:
        msg = f"container.bin missing {name}"
        raise RuntimeError(msg)
    h, img, size, _ = modules[index]
    overlap = OVERLAP if index + 1 < len(modules) else 0
    if shared_tail:
        if not overlap or payload[-overlap:] != b"\xff" * overlap:
            msg = f"{name}: shared module tail must be erased padding"
            raise RuntimeError(msg)
        if not include_overlap:
            payload = payload[:-overlap]
    region = payload + bytes(buf[img + size - overlap:img + size])
    header = bytearray(buf[h:h + HDR])
    struct.pack_into(">I", header, SIZE_OFF, len(region))
    struct.pack_into(">I", header, IMGCRC_OFF, zlib.crc32(region) & 0xFFFFFFFF)
    struct.pack_into(">I", header, PKG_HCRC2_OFF,
                     zlib.crc32(bytes(header[0x14:PKG_HCRC2_OFF])) & 0xFFFFFFFF)
    return bytearray(bytes(buf[:h]) + bytes(header) + region + bytes(buf[img + size:]))


# Stock stamps the payload modules -- not the executables -- with a "carries version" flag at
# 0x14 and one NUL-terminated ASCII string at 0xA8. On the original WLA package that string is
# "27.0.6.46330.5043500" for linuxPatched, the same version followed by the variant list
# "@sm2/spotty+mojo+binky" for ubi.img, and "U-Boot 2014.07-05722-g93f9891" for MLO and
# u-boot.img. Measured 2026-09-13.
MODULE_VERSION_FLAG_OFF = 0x14
MODULE_VERSION_OFF = 0xA8
MODULE_VERSION_MAX = IMGCRC_OFF - MODULE_VERSION_OFF - 1


def assemble_self_contained_stu(
        payloads: dict[str, bytes], out: Path, target: str = "wla",
        versions: dict[str, str] | None = None) -> None:
    """Write an experimental complete STU chain without a vendor container.

    ``versions`` maps a module name to the version string stock stores in its header. Stock's
    updater reads that field before it extracts anything, so a package that leaves it empty is
    rejected with ``software_failure`` before a single module executes.
    """
    expected = PROFILES[target]["modules"]
    if list(payloads) != expected:
        msg = "self-contained payloads do not match the target module chain"
        raise RuntimeError(msg)
    versions = versions or {}
    if set(versions) - set(expected):
        msg = "version metadata names a module outside the chain"
        raise RuntimeError(msg)
    headers = []
    for index, name in enumerate(expected):
        payload = payloads[name]
        if not payload:
            msg = f"{name}: empty self-contained payload"
            raise RuntimeError(msg)
        header = bytearray(HDR)
        header[0x18:0x18 + len(name)] = name.encode("ascii")
        size = len(payload) + (OVERLAP if index + 1 < len(expected) else 0)
        struct.pack_into(">II", header, SIZE_OFF, size, size)
        stock_shaped = target in ("wla-probe", "wla-stock-route", "wla-probe-rtch-names")
        ordinal = WLA_STOCK_ORDINALS[index] if stock_shaped else index + 1
        struct.pack_into(">I", header, 0xA0, ordinal)
        if stock_shaped and name in ("ARMUpdate", "UbootUpdate"):
            struct.pack_into(">I", header, 0x130, 2)
        if name in versions:
            version = versions[name].encode("ascii")
            if len(version) > MODULE_VERSION_MAX:
                msg = f"{name}: version string does not fit the module header"
                raise RuntimeError(msg)
            struct.pack_into(">I", header, MODULE_VERSION_FLAG_OFF, 1)
            header[MODULE_VERSION_OFF:MODULE_VERSION_OFF + len(version)] = version
        headers.append(header)
    # The container magic. A profile may name its own; everything else follows the old rule,
    # which is RTCH for the native chain and BOSE for the stock-shaped ones.
    magic = PROFILES[target].get("magic", b"RTCH" if target == "wla" else b"BOSE")
    struct.pack_into(">I4sII", headers[0], 0, OVERLAP, magic, 0, 0x124)
    for index, header in enumerate(headers):
        shared = headers[index + 1][:OVERLAP] if index + 1 < len(headers) else b""
        struct.pack_into(">I", header, IMGCRC_OFF,
                         zlib.crc32(payloads[expected[index]] + shared) & 0xFFFFFFFF)
        struct.pack_into(">I", header, PKG_HCRC2_OFF,
                         zlib.crc32(bytes(header[0x14:PKG_HCRC2_OFF])) & 0xFFFFFFFF)
    image = bytearray(headers[0])
    for index, name in enumerate(expected):
        image += payloads[name]
        if index + 1 < len(expected):
            image += headers[index + 1]
    struct.pack_into(">I", image, PKG_SIZE_OFF, len(image))
    struct.pack_into(">I", image, PKG_HCRC_OFF, zlib.crc32(bytes(image[:PKG_HCRC_OFF])) & 0xFFFFFFFF)
    struct.pack_into(">I", image, PKG_HCRC2_OFF,
                     zlib.crc32(bytes(image[0x14:PKG_HCRC2_OFF])) & 0xFFFFFFFF)
    validate_stu(image, target)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_bytes(image)


def require_kernel_uimage(image: ByteData) -> None:
    """Raise unless the kernel is a legacy uImage within the reserved Pong budget."""
    if len(image) > MAX_KERNEL_BYTES or len(image) < UIMAGE_HEADER_SIZE or _be(image, 0) != UIMAGE_MAGIC:
        msg = "mainline uImage is invalid or exceeds the reserved Pong budget"
        raise RuntimeError(msg)


def bose_kernel_payload(path: Path) -> bytes:
    """Return the kernel payload with the Linub name stock U-Boot's fixsig repairs, padded."""
    image = bytearray(path.read_bytes())
    require_kernel_uimage(image)
    canonical = bytearray(image[:64])
    stored_crc = _be(canonical, 4)
    struct.pack_into(">I", canonical, 4, 0)
    if image[32:37] != b"Linux" or zlib.crc32(canonical) & 0xFFFFFFFF != stored_crc:
        msg = "mainline uImage must have a valid canonical Linux header"
        raise RuntimeError(msg)
    size = _be(image, 12)
    if size != len(image) - 64 or zlib.crc32(image[64:]) & 0xFFFFFFFF != _be(image, 24):
        msg = "mainline uImage payload CRC or size is invalid"
        raise RuntimeError(msg)
    image[32:37] = b"Linub"
    repaired = bytearray(image[:64])
    repaired[32:37] = b"Linux"
    struct.pack_into(">I", repaired, 4, 0)
    if zlib.crc32(repaired) & 0xFFFFFFFF != stored_crc:
        msg = "Bose fixsig simulation failed"
        raise RuntimeError(msg)
    padded_size = (len(image) + UBI_MIN_IO - 1) // UBI_MIN_IO * UBI_MIN_IO
    if padded_size - len(image) < OVERLAP:
        padded_size += UBI_MIN_IO
    return bytes(image) + b"\xff" * (padded_size - len(image))


def native_kernel_payload(path: Path) -> bytes:
    """Return the kernel payload unchanged after checking its header, padded."""
    image = path.read_bytes()
    require_kernel_uimage(image)
    header = bytearray(image[:64])
    stored_crc = _be(header, 4)
    struct.pack_into(">I", header, 4, 0)
    size = _be(image, 12)
    if (image[32:37] != b"Linux" or zlib.crc32(header) & 0xFFFFFFFF != stored_crc
            or size != len(image) - 64
            or zlib.crc32(image[64:]) & 0xFFFFFFFF != _be(image, 24)):
        msg = "mainline uImage has an invalid canonical header or payload"
        raise RuntimeError(msg)
    padded_size = (len(image) + UBI_MIN_IO - 1) // UBI_MIN_IO * UBI_MIN_IO
    if padded_size - len(image) < OVERLAP:
        padded_size += UBI_MIN_IO
    return image + b"\xff" * (padded_size - len(image))


def require_kernel_model(path: Path, model: str) -> None:
    """Raise unless the uImage names the kernel built for ``model``."""
    image = path.read_bytes()
    if len(image) < UIMAGE_HEADER_SIZE:
        msg = f"{path}: mainline uImage is truncated"
        raise RuntimeError(msg)
    name = image[32:64].split(b"\0", 1)[0].decode("ascii", "strict")
    if name != f"Linux-6.12 SoundTap {model}":
        msg = f"{path}: kernel name is {name!r}, expected model {model!r}"
        raise RuntimeError(msg)


def native_spl_payload(mlo: bytes) -> bytes:
    """Return the MLO followed by the erased overlap the next module header takes."""
    # STU chains replace each nonfinal module's trailing overlap with next header.
    # Preserve raw MLO tail; NAND's following bytes must remain erased.
    return mlo + b"\xff" * OVERLAP


def validate_mlo(image: bytes) -> None:
    """Check an image destined for mtd0, where the mask ROM reads it.

    The shape is not a guess: it was read off a stock WLA's own mtd0, the image
    that board actually boots -- a TI configuration header, a GP header at 0x200
    giving size and load address, then the SPL. mkimage's omapimage output has the
    same shape, and its size field counts everything after the configuration
    header, the GP header included.

    The raw vectors-at-zero form is the USB path's, and belongs in no partition.
    """
    if len(image) <= GP_HEADER_OFFSET + 8 or len(image) > NAND_SPL:
        msg = f"MLO is invalid or exceeds mtd0 {NAND_SPL:,} B"
        raise RuntimeError(msg)
    if image[:4] != b"\x40\x00\x00\x00" or b"CHSETTINGS" not in image[:GP_HEADER_OFFSET]:
        msg = "MLO lacks the TI configuration header the ROM reads from NAND"
        raise RuntimeError(msg)
    size, load = struct.unpack_from("<II", image, GP_HEADER_OFFSET)
    # The stock image declares less than it stores -- its bytes run past the size
    # field, to a page boundary -- so require only that what it declares is present.
    # The exact-fit rule belongs to our own build, in os/support/check_wla_product_spl.py.
    if size < MLO_MIN_DECLARED_SIZE or GP_HEADER_OFFSET + size > len(image):
        msg = "MLO GP header declares more than the image holds"
        raise RuntimeError(msg)
    if load & 3 or load < MLO_LOAD_MIN or load + size > MLO_LOAD_END:
        msg = "MLO has an invalid TI GP/SPL header"
        raise RuntimeError(msg)
    # No check on the first instruction: the stock SPL starts with a branch, not
    # U-Boot's vector table. That U-Boot's own build emits vectors there is asserted
    # where it is built, in os/support/check_wla_product_spl.py.


def validate_uboot(image: bytes) -> None:
    """Raise unless the image is a legacy uImage that fits mtd4."""
    if len(image) < UIMAGE_HEADER_SIZE or len(image) > NAND_UBOOT or _be(image, 0) != UIMAGE_MAGIC:
        msg = f"u-boot.img is invalid or exceeds mtd4 {NAND_UBOOT:,} B"
        raise RuntimeError(msg)
    header = bytearray(image[:64])
    stored_crc = _be(header, 4)
    struct.pack_into(">I", header, 4, 0)
    size = _be(image, 12)
    if (size != len(image) - 64
            or zlib.crc32(header) & 0xFFFFFFFF != stored_crc
            or zlib.crc32(image[64:]) & 0xFFFFFFFF != _be(image, 24)):
        msg = "u-boot.img has an invalid legacy uImage header"
        raise RuntimeError(msg)


def validate_sizes(ubi_bytes: int, stu_bytes: int, target: str = "st10") -> None:
    """Refuse a package larger than the stock one — see STOCK_*_BYTES for why."""
    profile = PROFILES[target]
    for what, got, limit in (("ubi.img", ubi_bytes, profile["stock_ubi_bytes"]),
                             ("STU", stu_bytes, profile["stock_stu_bytes"])):
        if got > limit:
            msg = (
                f"{what} is {got:,} B, {got - limit:,} B over the stock budget of {limit:,} B "
                f"({got / 2**20:.1f} MiB vs {limit / 2**20:.1f} MiB) — trim the rootfs"
            )
            raise RuntimeError(
                msg,
            )
    print(f"  size: ubi {ubi_bytes / 2**20:.1f} MiB, STU {stu_bytes / 2**20:.1f} MiB "
               f"({target} stock budget {profile['stock_stu_bytes'] / 2**20:.1f} MiB)")


# Stock's coordinator extracts and runs module 1 and streams the rest into it, so the three
# other executable slots exist only to keep the chain the shape stock's spec describes. Our
# installer parses them out of the stream and ignores them; nothing executes them.
UNUSED_MODULE_NOTICE = (
    b"soundtap-wla: this slot exists to keep the stock module chain shape. "
    b"The SoundTap installer performs the whole install itself and never runs it.\n"
)

# The running 27.0.6 build's revisions, one patch level up, so stock reads the package as an
# upgrade. The variant list keeps binky in scope (measured 2026-09-13).
STOCK_ROUTE_VERSIONS = {
    "linuxPatched": "27.0.7.46330.5043500",
    "ubi.img": "27.0.7.46330.5043500@sm2/spotty+mojo+binky",
    "MLO": "U-Boot 2014.07-05722-g93f9891",
    "u-boot.img": "U-Boot 2014.07-05722-g93f9891",
}


# The same revisions as the stock-route package, under our own module names, so stock still
# reads the file as an upgrade. Keyed by the names this chain uses.
UNIVERSAL_VERSIONS = {
    "soundtap-kernel": "27.0.7.46330.5043500",
    "soundtap-rootfs": "27.0.7.46330.5043500@sm2/spotty+mojo+binky",
    "soundtap-spl": "U-Boot 2014.07-05722-g93f9891",
    "soundtap-uboot": "U-Boot 2014.07-05722-g93f9891",
}


TRI_VERSIONS = {
    name: ("U-Boot 2014.07-05722-g93f9891" if module_base_name(name) in
           ("soundtap-spl", "soundtap-uboot") else
           "27.0.7.46330.5043500@sm2/spotty+mojo+binky" if module_base_name(name) ==
           "soundtap-rootfs" else "27.0.7.46330.5043500")
    for name in SOUNDTAP_TRI_MODULES if name != "soundtap-installer"
}


def read_installer(path: Path) -> bytes:
    """Return the installer binary, refusing anything but a repo-built ARM ELF."""
    installer = path.read_bytes()
    if installer[:4] != b"\x7fELF" or len(installer) < INSTALLER_MIN_BYTES:
        msg = "--installer must be the repo-built ARM installer binary"
        raise RuntimeError(msg)
    return installer


def read_boot_payloads(args: argparse.Namespace) -> tuple[bytes, bytes]:
    """Return the --mlo and --u-boot images after validating both."""
    mlo, uboot = args.mlo.read_bytes(), args.u_boot.read_bytes()
    validate_mlo(mlo)
    validate_uboot(uboot)
    return mlo, uboot


def build_tri_model_stu(args: argparse.Namespace) -> None:
    """Rhino + binky, and spotty too when its kernel is passed."""
    installer = read_installer(args.installer)
    mlo, uboot = read_boot_payloads(args)
    require_kernel_model(args.rhino_kernel, "st10")
    if args.spotty_kernel:
        require_kernel_model(args.spotty_kernel, "st20")
        if not args.spotty_mlo:
            msg = (
                "an ST20 kernel needs --spotty-mlo: its ROM boots NAND before USB, "
                               "so it takes the USB-first SPL"
            )
            raise RuntimeError(msg)
        spotty_mlo = args.spotty_mlo.read_bytes()
        validate_mlo(spotty_mlo)
    # Rhino and binky use one universal kernel. Board identity, DTB and bootargs come from
    # the shared U-Boot environment, not from bytes baked into the image.
    if args.binky_kernel.read_bytes() != args.rhino_kernel.read_bytes():
        msg = "rhino and binky must use the same universal kernel bytes"
        raise RuntimeError(msg)
    payloads = {
        "soundtap-installer": installer,
        "soundtap-kernel.rhino": bose_kernel_payload(args.rhino_kernel),
        "soundtap-kernel.binky": bose_kernel_payload(args.binky_kernel),
        "soundtap-rootfs": args.rhino_rootfs.read_bytes(),
        "soundtap-spl.binky": native_spl_payload(mlo),
        "soundtap-uboot.binky": uboot,
    }
    if args.spotty_kernel:
        payloads["soundtap-kernel.spotty"] = bose_kernel_payload(args.spotty_kernel)
        payloads["soundtap-spl.spotty"] = native_spl_payload(spotty_mlo)
    target = "soundtap-tri" if args.spotty_kernel else "soundtap-duo"
    order = PROFILES[target]["modules"]
    payloads = {name: payloads[name] for name in order}
    versions = {name: TRI_VERSIONS[name] for name in order if name in TRI_VERSIONS}
    assemble_self_contained_stu(payloads, args.out, target, versions)


def build_universal_stu(args: argparse.Namespace, ubi: Path) -> None:
    """One package for every route: stock's :17008 and SoundTap's own web API.

    Stock authenticates the four magic bytes at 0x004 and takes module 0 by position, not by
    name (measured 2026-09-18, docs/decisions/0008-one-universal-stu.md), so the
    file wears BOSE and everything else is ours. The installer in module 0 reads the four
    payload names, and so does internal/stu, which is what makes it one file rather than two.

    The kernel carries the Linub name for the same reason the stock-route package does: the
    fallback arm of the slot_boot recipe reads the raw kernel partition and runs fixsig over
    it, and that is the path proven on hardware.
    """
    installer = read_installer(args.installer)
    mlo, uboot = read_boot_payloads(args)
    payloads = {
        "soundtap-installer": installer,
        "soundtap-kernel": bose_kernel_payload(args.kernel),
        "soundtap-rootfs": ubi.read_bytes(),
        "soundtap-spl": native_spl_payload(mlo),
        "soundtap-uboot": uboot,
    }
    assemble_self_contained_stu(payloads, args.out, "soundtap", UNIVERSAL_VERSIONS)


def build_stock_route_stu(args: argparse.Namespace, ubi: Path) -> None:
    """Assemble the stock module chain stock firmware accepts on :17008, carrying our payloads."""
    installer = read_installer(args.installer)
    mlo, uboot = read_boot_payloads(args)
    payloads = {
        "SoftwareUpdateInstaller": installer,
        "ARMUpdate": UNUSED_MODULE_NOTICE,
        "UbootUpdate": UNUSED_MODULE_NOTICE,
        "SCMReset_Update": UNUSED_MODULE_NOTICE,
        # Stock U-Boot repairs the five name bytes with fixsig before bootm, exactly as it does
        # for Bose's own kernel, so the stored image carries the Linub name.
        "linuxPatched": bose_kernel_payload(args.kernel),
        "ubi.img": ubi.read_bytes(),
        "MLO": native_spl_payload(mlo),
        "u-boot.img": uboot,
    }
    assemble_self_contained_stu(payloads, args.out, "wla-stock-route", STOCK_ROUTE_VERSIONS)


def build_parser() -> argparse.ArgumentParser:
    """Return the command-line parser for every package shape."""
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--rootfs",    type=Path, default=REPO / "os/rootfs")
    ap.add_argument("--work-dir",  type=Path, default=Path("/tmp/soundtap-build"))
    ap.add_argument("--out",       type=Path, default=REPO / "dist/Update_soundtap-firmware.stu")
    ap.add_argument("--kernel",    type=Path, help="mainline uImage; enables kernel+rootfs STU")
    ap.add_argument("--ubi",       type=Path, help="prebuilt UBI image; skips rootfs packing")
    ap.add_argument("--self-contained", action="store_true",
                    help="assemble native four-module WLA chain")
    ap.add_argument("--mlo", type=Path, help="TI GP-headered WLA SPL/MLO")
    ap.add_argument("--u-boot", type=Path, help="legacy-uImage WLA U-Boot")
    ap.add_argument("--target", choices=sorted(PROFILES), default="st10")
    ap.add_argument("--installer", type=Path,
                    help="repo-built WLA installer for module 1 of the stock-route package")
    ap.add_argument("--stock-route", action="store_true",
                    help="assemble the package stock Bose firmware accepts on :17008")
    ap.add_argument("--universal", action="store_true",
                    help="assemble the one package both routes read: BOSE magic, our names")
    ap.add_argument("--tri-model", action="store_true",
                    help="offline rhino+binky+spotty package with model-suffixed payloads")
    for model in ("rhino", "binky", "spotty"):
        ap.add_argument(f"--{model}-kernel", type=Path)
    ap.add_argument("--spotty-mlo", type=Path, help="ST20 NAND SPL (out-st20-spl/MLO)")
    ap.add_argument("--rhino-rootfs", type=Path,
                    help="shared tri-model rootfs carrying every model's DTB and config")
    return ap


def check_tri_model_args(ap: argparse.ArgumentParser, args: argparse.Namespace) -> None:
    """Refuse a --tri-model call without its inputs, or a tri/duo target without --tri-model."""
    if args.tri_model:
        tri_inputs = (args.installer, args.rhino_kernel, args.rhino_rootfs, args.binky_kernel,
                      args.mlo, args.u_boot)
        wanted = "soundtap-tri" if args.spotty_kernel else "soundtap-duo"
        if args.target != wanted or not all(tri_inputs):
            ap.error(f"--tri-model requires --target {wanted}, --installer, --rhino-kernel, "
                     "--binky-kernel, one shared rootfs, --mlo and --u-boot; pass "
                     "--spotty-kernel with --target soundtap-tri to include the ST20")
        if args.self_contained or args.stock_route or args.universal or args.ubi:
            ap.error("--tri-model assembles its own container and payload set")
    elif args.target in ("soundtap-tri", "soundtap-duo"):
        ap.error(f"--target {args.target} requires --tri-model")


def check_universal_args(ap: argparse.ArgumentParser, args: argparse.Namespace) -> None:
    """Refuse a --universal call without its inputs, or the soundtap target without --universal."""
    if args.universal:
        if args.target != "soundtap" or not args.installer or not args.kernel \
                or not args.ubi or not args.mlo or not args.u_boot:
            ap.error("--universal requires --target soundtap, --installer, --kernel, --ubi, "
                     "--mlo and --u-boot")
        if args.self_contained or args.stock_route:
            ap.error("--universal assembles its own container")
    elif args.target == "soundtap":
        ap.error("--target soundtap requires --universal")


def check_stock_route_args(ap: argparse.ArgumentParser, args: argparse.Namespace) -> None:
    """Refuse a --stock-route call without its inputs, or its target without --stock-route."""
    if args.stock_route:
        if args.target != "wla-stock-route" or not args.installer or not args.kernel \
                or not args.ubi or not args.mlo or not args.u_boot:
            ap.error("--stock-route requires --target wla-stock-route, --installer, --kernel, "
                     "--ubi, --mlo and --u-boot")
        if args.self_contained:
            ap.error("--stock-route assembles its own container")
    elif args.target == "wla-stock-route":
        ap.error("--target wla-stock-route requires --stock-route")


def check_mode_args(ap: argparse.ArgumentParser, args: argparse.Namespace) -> None:
    """Exit with a usage error unless exactly one complete package shape was asked for."""
    if args.target == "wla-probe":
        ap.error("wla-probe is offline-only and has no release CLI path")
    check_tri_model_args(ap, args)
    check_universal_args(ap, args)
    check_stock_route_args(ap, args)
    if args.self_contained and (args.target != "wla" or not args.kernel or not args.mlo or not args.u_boot):
        ap.error("--self-contained requires --target wla, --kernel, --mlo and --u-boot")
    if not (args.self_contained or args.stock_route or args.universal or args.tri_model):
        ap.error("give --tri-model, --universal, --stock-route or --self-contained")

    if args.ubi and args.rootfs != REPO / "os/rootfs":
        ap.error("--ubi and --rootfs are mutually exclusive")


def required_rootfs_files(target: str) -> list[str]:
    """Return every rootfs file the runtime of ``target`` depends on."""
    required = [
        "bin/busybox",                  # init and shell target
        "opt/soundtap/soundtapd",         # the daemon
        "usr/bin/librespot",            # Spotify Connect — always shipped
        "etc/init.d/soundtap",           # boot + AUDIO_MIX gate + fifo
        "etc/asound.conf.bus",          # arbiter-mode ALSA routing (source bus)
        "etc/shairport-sync.conf",      # AirPlay
        "opt/soundtap/airplay-source.sh",
        "opt/soundtap/bt.sh",            # Bluetooth HCI/A2DP supervisor
        "opt/soundtap/bt-ctl.sh",        # Bluetooth AVRCP control
        "opt/soundtap/bt-source.sh",
        "opt/soundtap/spotify-source.sh",
    ]
    if target == "st10":
        required += [
            "boot/rhino_sm2.dtb",       # ST10 U-Boot loads this before the kernel
            "usr/sbin/hostapd",         # setup AP (soundtapd's Wi-Fi manager)
        ]
    elif target == "st20":
        required += [
            "boot/spotty_scm.dtb",      # stock ST20 U-Boot DTB path
        ]
    else:
        required += [
            "boot/binky_sm2.dtb",       # stock WLA U-Boot DTB path
            "usr/sbin/hostapd",         # setup AP (soundtapd's Wi-Fi manager)
        ]
    return required


def rootfs_executables(target: str) -> list[str]:
    """Return the rootfs files that must carry an executable bit for ``target``."""
    executables = [
        "bin/busybox",
        "opt/soundtap/soundtapd",
        "usr/bin/librespot",
        "etc/init.d/soundtap",
        "opt/soundtap/airplay-source.sh",
        "opt/soundtap/bt.sh",
        "opt/soundtap/bt-ctl.sh",
        "opt/soundtap/bt-source.sh",
        "opt/soundtap/spotify-source.sh",
    ]
    if target in ("st10", "wla"):
        executables += ["usr/sbin/hostapd"]
    return executables


def check_rootfs(rootfs: Path, target: str) -> None:
    """Exit unless the rootfs tree holds every file, link and executable bit the runtime needs.

    A broken rootfs build (missing overlay, renamed path) fails here instead of shipping a
    firmware that boots without audio routing or source hooks.
    """
    missing = [p for p in required_rootfs_files(target) if not (rootfs / p).is_file()]
    if missing:
        raise SystemExit(
            f"{rootfs} is missing required files (build the Buildroot rootfs first): "
            + ", ".join(missing),
        )
    for link in ("init", "sbin/init"):
        path = rootfs / link
        if not path.is_symlink() or path.readlink() != Path("/bin/busybox"):
            msg = f"{path} must be a symlink to /bin/busybox"
            raise SystemExit(msg)
    not_executable = [p for p in rootfs_executables(target) if not (rootfs / p).stat().st_mode & 0o111]
    if not_executable:
        raise SystemExit(
            f"{rootfs} has non-executable boot files: " + ", ".join(not_executable),
        )


def prepare_ubi(args: argparse.Namespace) -> Path | None:
    """Validate the given UBI image, or pack one from the rootfs; return the UBI path."""
    args.work_dir.mkdir(parents=True, exist_ok=True)
    ubi = args.ubi
    if args.tri_model:
        validate_ubi(args.rhino_rootfs)
    elif ubi:
        validate_ubi(ubi)
    else:
        ubifs = args.work_dir / "soundtap.ubifs"
        ubi = args.work_dir / "poc_ubi.img"
        print("=== mkfs.ubifs ===")
        pack_ubifs(args.rootfs, ubifs)
        print("=== ubinize ===")
        pack_ubi(ubifs, ubi)
    return ubi


def build_self_contained_stu(args: argparse.Namespace, ubi: Path) -> None:
    """Assemble the native four-module WLA chain."""
    mlo, uboot = read_boot_payloads(args)
    assemble_self_contained_stu({
        "soundtap-kernel": native_kernel_payload(args.kernel),
        "soundtap-rootfs": ubi.read_bytes(),
        "soundtap-spl": native_spl_payload(mlo),
        "soundtap-uboot": uboot,
    }, args.out, args.target)


def assemble_package(args: argparse.Namespace, ubi: Path | None) -> None:
    """Assemble the package shape the command line asked for."""
    if args.tri_model:
        build_tri_model_stu(args)
    elif args.universal:
        build_universal_stu(args, ubi)
    elif args.stock_route:
        build_stock_route_stu(args, ubi)
    elif args.self_contained:
        build_self_contained_stu(args, ubi)


def main() -> None:
    """Check the command line and the rootfs, then build and write the STU."""
    ap = build_parser()
    args = ap.parse_args()
    check_mode_args(ap, args)
    if not args.ubi and not args.tri_model:
        preflight_tools("mkfs.ubifs", "ubinize")
        check_rootfs(args.rootfs, args.target)

    ubi = prepare_ubi(args)
    print("=== assemble STU ===")
    assemble_package(args, ubi)
    print(f"\n=== DONE ===\n  {args.out}\n  md5: {md5file(args.out)}")


if __name__ == "__main__":
    main()
