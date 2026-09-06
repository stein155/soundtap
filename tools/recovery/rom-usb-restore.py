#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Repair a speaker's boot chain over mask-ROM USB with this release's SPL and U-Boot.

    rom-usb-restore.py --model st10|st20|wla|st300 (--images DIR | --stu FILE) [options]

The board comes up from its mask ROM over USB: the ROM takes an SPL, the SPL takes a U-Boot
that runs from RAM, and that U-Boot fetches one blob from the host, checks it against the
length and crc32 it was built for, and runs the script at its front. The script writes NAND
the way the installers write a boot chain (ADR 0009): the spare U-Boot in NAND.U-Boot blocks
8-13, SPL copies 1, 2, 3 and 0, then the first U-Boot copy in blocks 0-7, last. Block 14 of
NAND.U-Boot (the redundant environment) and NAND.U-Boot-Env are left alone unless
--erase-env says otherwise.

Every write is in place, one 128 KiB block at a time, and read back before the next. Before
anything is erased the script reads the bad-block marker of every block it will program; a
bad one stops the run with nothing written. NAND write and read in U-Boot skip bad blocks,
which would move data into the block behind; refusing is what keeps the spare and the first
copy apart, and a padded backup partition at the offsets it was read from.

--backup DIR --partition NAME restores partitions from the board's own backup, taken with
tools/recovery/nand-backup-ssh.sh: `mtdN-<Name>.oob` files with their SHA256SUMS. Anything
stock comes from there and nowhere else; the repository holds no Bose bytes (ADR 0049). A
backup goes in further sessions of at most 16 MiB each, so one partition may need several.
With --backup-only the release's boot chain is not written, and only then may a backup's
NAND.SPL* or NAND.U-Boot be restored: that is the full way back to the board's own NAND.

--dry-run prints every step, with the exact flash ranges, and builds, writes and touches
nothing. --json prints the same plan as JSON.

Without --dry-run each session builds its RAM U-Boot (os/bootloader/build-wla-uboot.sh with
WLA_UBOOT_NAND_SCRIPT, Docker), checks the loader inputs without USB, asks for a typed
confirmation, then runs tools/recovery/rom-usb-loader. The loader must run as root: in Docker
(the default, a Linux host with /dev/bus/usb) or natively (--loader native, run this as root).
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shlex
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import zlib
from dataclasses import dataclass, field
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]

BLOCK = 0x20000
PAGE = 0x800
OOB = 0x40

# The table every model shares (app/internal/flash/recipes.go, mtdparts).
PARTITIONS = [
    ("NAND.SPL", 0x0, 0x20000),
    ("NAND.SPL.backup1", 0x20000, 0x20000),
    ("NAND.SPL.backup2", 0x40000, 0x20000),
    ("NAND.SPL.backup3", 0x60000, 0x20000),
    ("NAND.U-Boot", 0x80000, 0x1E0000),
    ("NAND.U-Boot-Env", 0x260000, 0x20000),
    ("NAND.Kernel-Ping", 0x280000, 0x500000),
    ("NAND.Kernel-Pong", 0x780000, 0x500000),
    ("NAND.File-System-Ping", 0xC80000, 0x6000000),
    ("NAND.File-System-Pong", 0x6C80000, 0x6000000),
    ("NAND.Persistent-Storage", 0xCC80000, 0x2600000),
    ("NAND.Update-Staging", 0xF280000, 0xB80000),
    ("NAND.Manufacturing-Data", 0xFE00000, 0x40000),
]
PART = {label: (offset, size) for label, offset, size in PARTITIONS}
BOOT_CHAIN = {"NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3", "NAND.U-Boot"}
UBOOT = PART["NAND.U-Boot"][0]
# install.SecondUBootOffset and ubootenv.RedundantOffset: the spare U-Boot has blocks 8-13 of
# NAND.U-Boot, block 14 holds the redundant environment, so u-boot.img is at most 0xc0000.
SPARE_OFFSET = 0x100000
ENV_REDUNDANT_OFFSET = 0x1C0000
MAX_UBOOT = ENV_REDUNDANT_OFFSET - SPARE_OFFSET
MAX_SPL = 0x20000

# RAM on the board. The blob is fetched to LOAD; its script area holds the script image and
# the data blocks follow. The loader serves at most what this blob limit allows, and the
# restore pieces proven on the WLA were 16 MiB.
LOAD = 0x81000000
SCRIPT_AREA = 0x40000
DATA = LOAD + SCRIPT_AREA
BLOB_MAX = 0x1000000
READBACK = 0x84000000
RAW = 0x85000000
MAX_DATA_BLOCKS = (BLOB_MAX - SCRIPT_AREA) // BLOCK

TFTP_NAME = "soundtap-rom-usb"
MARK_DONE = "RU-complete"
LOADER_IMAGE = "soundtap-rom-usb-loader:bookworm"
LOADER_DIR = REPO / "tools/recovery/rom-usb-loader"
UBOOT_BUILDER = REPO / "os/bootloader/build-wla-uboot.sh"

STU_HDR = 0x138
STU_OVERLAP = 0x14
STU_SIZE_OFF = 0x98


@dataclass(frozen=True)
class Model:
    name: str
    variant: str
    title: str
    spl: str            # which SPL the release gives this model: "binky" (sm2) or "spotty"
    rom_stage: str      # "build": the RAM SPL of the U-Boot build; "release": the release SPL
    entry: str          # how this board's ROM gets to USB


MODELS = {
    "st10": Model("st10", "rhino", "SoundTouch 10", "binky", "build",
                  "Its ROM tries USB before NAND (SYSBOOT 01011b, measured 2026-09-19): connect "
                  "the micro-B port to this host with a plain cable, no OTG adapter, and power "
                  "the speaker on (docs/hardware/st10/usb-recovery-and-rom.md)."),
    "wla": Model("wla", "binky", "SoundTouch Wireless Link Adapter", "binky", "build",
                 "Its ROM tries USB before NAND (SYSBOOT 01011b): connect its USB port to this "
                 "host and power it on."),
    "st20": Model("st20", "spotty", "SoundTouch 20", "spotty", "release",
                  "Its ROM boots NAND before USB (SYSBOOT 10010b). Open the speaker, hold R/B# "
                  "(ICT pad 9 behind the NAND, through a meter on its 2000 uA range) low at "
                  "power-on for about 3 s (docs/hardware/st20/rom-usb-recovery.md)."),
    "st300": Model("st300", "ginger", "SoundTouch 300", "binky", "build",
                   "Unproven: nobody has measured how the ST300's ROM reaches USB. Establish "
                   "that, and a way back, before using this on a bar."),
}
IMAGE_PATHS = {"binky": "wla-spl/MLO", "spotty": "st20-spl/MLO", "uboot": "wla-uboot/u-boot.img"}


class Refused(Exception):
    """The inputs do not allow a safe plan; nothing has been done."""


def crc32(data: bytes) -> int:
    return zlib.crc32(data) & 0xFFFFFFFF


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


# --- the release's images ---------------------------------------------------------------

def check_mlo(mlo: bytes, what: str) -> None:
    """The NAND form of the SPL: TI configuration header, GP header, then a branch."""
    if not 0x20C <= len(mlo) <= MAX_SPL:
        raise Refused(f"{what}: {len(mlo)} bytes does not fit a 128 KiB SPL partition")
    if struct.unpack_from("<I", mlo)[0] != 0x40 or b"CHSETTINGS" not in mlo[:0x200]:
        raise Refused(f"{what}: no TI configuration header, not the form the ROM reads from NAND")
    size, load = struct.unpack_from("<II", mlo, 0x200)
    if load != 0x402F0400:
        raise Refused(f"{what}: loads to {load:#010x}, not AM335x SRAM at 0x402f0400")
    if 0x208 + size > len(mlo):
        raise Refused(f"{what}: declares {size} bytes but holds {len(mlo) - 0x208}")
    if mlo[0x20B] not in (0xEA, 0xEB):
        raise Refused(f"{what}: does not start with a branch to the reset handler")


def check_uboot(img: bytes, what: str) -> None:
    if len(img) < 64 or struct.unpack_from(">I", img)[0] != 0x27051956:
        raise Refused(f"{what}: not a legacy uImage")
    header = bytearray(img[:64])
    stored = struct.unpack_from(">I", header, 4)[0]
    struct.pack_into(">I", header, 4, 0)
    size, dcrc = struct.unpack_from(">I", img, 12)[0], struct.unpack_from(">I", img, 24)[0]
    if crc32(bytes(header)) != stored or size + 64 != len(img) or crc32(img[64:]) != dcrc:
        raise Refused(f"{what}: legacy uImage header or data CRC is wrong")
    if len(img) > MAX_UBOOT:
        raise Refused(f"{what}: {len(img):#x} bytes is over the {MAX_UBOOT:#x} a U-Boot copy may "
                      "take (NAND.U-Boot blocks 8-13; block 14 is the redundant environment)")


def read_sums(directory: Path) -> dict[str, str]:
    sums = directory / "SHA256SUMS"
    if not sums.is_file():
        raise Refused(f"{sums} is missing: the images are only taken with their checksums")
    table = {}
    for line in sums.read_text().splitlines():
        parts = line.split()
        if len(parts) == 2:
            table[parts[1].lstrip("*").removeprefix("./")] = parts[0].lower()
    return table


def images_from_dir(directory: Path, model: Model) -> tuple[bytes, bytes, dict]:
    sums = read_sums(directory)
    got = {}
    for key in (model.spl, "uboot"):
        rel = IMAGE_PATHS[key]
        path = directory / rel
        if not path.is_file() or path.is_symlink():
            raise Refused(f"{path} is missing or a symlink")
        data = path.read_bytes()
        if sums.get(rel) != sha256(data):
            raise Refused(f"{rel} does not match {directory / 'SHA256SUMS'}")
        got[key] = data
    return got[model.spl], got["uboot"], {"images": str(directory), "spl": IMAGE_PATHS[model.spl],
                                           "uboot": IMAGE_PATHS["uboot"]}


def stu_modules(data: bytes) -> dict[str, bytes]:
    """Module name -> payload, the chain internal/stu reads."""
    found, header = [], 0
    while header + STU_HDR <= len(data):
        size = struct.unpack_from(">I", data, header + STU_SIZE_OFF)[0]
        image = header + STU_HDR
        if size == 0 or image + size > len(data):
            break
        name = data[header + 0x18:header + 0x40].split(b"\0", 1)[0].decode("ascii", "replace")
        found.append((name, image, size))
        header = image + size - STU_OVERLAP
    modules = {}
    for index, (name, image, size) in enumerate(found):
        last = index == len(found) - 1
        modules[name] = data[image:image + size - (0 if last else STU_OVERLAP)]
    return modules


def images_from_stu(path: Path, model: Model) -> tuple[bytes, bytes, dict]:
    if not path.is_file():
        raise Refused(f"{path} is missing")
    modules = stu_modules(path.read_bytes())
    spl_name, uboot_name = f"soundtap-spl.{model.spl}", "soundtap-uboot.binky"
    for name in (spl_name, uboot_name):
        if name not in modules:
            raise Refused(f"{path} carries no {name}")
    spl = modules[spl_name]
    # build-stu.py's native_spl_payload: the MLO plus the overlap a next header would take.
    if spl.endswith(b"\xff" * STU_OVERLAP):
        spl = spl[:-STU_OVERLAP]
    return spl, modules[uboot_name], {"stu": str(path), "spl": spl_name, "uboot": uboot_name}


def rom_stage_spl(mlo: bytes) -> bytes:
    """The raw SPL the ROM takes over USB: the MLO without its 0x200 + 8 header bytes."""
    raw = mlo[0x208:]
    if len(raw) < 4 or raw[3] != 0xEA:
        raise Refused("the release SPL does not start with raw ARM vectors after its headers")
    return raw


# --- the board's own backup ---------------------------------------------------------------

def partition_by_name(name: str) -> str:
    if name.startswith("mtd") and name[3:].isdigit() and int(name[3:]) < len(PARTITIONS):
        return PARTITIONS[int(name[3:])][0]
    label = name if name.startswith("NAND.") else "NAND." + name
    if label not in PART:
        raise Refused(f"unknown partition {name}; use mtdN or a label such as NAND.Kernel-Pong")
    return label


def backup_data(directory: Path, label: str) -> bytes:
    """The data of one partition from nand-backup-ssh.sh's OOB dump, checked against its sums."""
    index = [p[0] for p in PARTITIONS].index(label)
    name = f"mtd{index}-{label.removeprefix('NAND.')}.oob"
    path = directory / name
    if not path.is_file() or path.is_symlink():
        raise Refused(f"{path} is missing or a symlink")
    raw = path.read_bytes()
    if read_sums(directory).get(name) != sha256(raw):
        raise Refused(f"{name} does not match {directory / 'SHA256SUMS'}")
    size = PART[label][1]
    if len(raw) != size // PAGE * (PAGE + OOB):
        raise Refused(f"{name} is {len(raw)} bytes; a {size:#x} partition with OOB is "
                      f"{size // PAGE * (PAGE + OOB)}")
    step = PAGE + OOB
    return b"".join(raw[i:i + PAGE] for i in range(0, len(raw), step))


# --- the plan -------------------------------------------------------------------------------

@dataclass
class Unit:
    """One range of NAND: erased whole, then its data blocks written in place."""
    step: str           # no spaces: it goes into the board's ${step}
    label: str
    partition: str
    start: int          # flash offset, block aligned
    end: int
    blocks: list[tuple[int, bytes]] = field(default_factory=list)   # (flash offset, 128 KiB)


@dataclass
class Session:
    index: int
    title: str
    units: list[Unit]

    def data_blocks(self) -> list[tuple[int, bytes]]:
        return [b for unit in self.units for b in unit.blocks]


def place(image: bytes, start: int, end: int, dense: bool) -> list[tuple[int, bytes]]:
    """The 128 KiB blocks image fills from start, padded with 0xff.

    dense: every block the image covers is written and must be good, as the SPL and the ROM
    read a boot image as consecutive good blocks. Otherwise a block of only 0xff is just
    erased, which is what a padded backup holds where the board had a bad block.
    """
    if len(image) > end - start:
        raise Refused(f"{len(image):#x} bytes do not fit {start:#x}..{end:#x}")
    blocks = []
    for off in range(0, len(image), BLOCK):
        chunk = image[off:off + BLOCK].ljust(BLOCK, b"\xff")
        if dense or chunk.count(0xFF) != BLOCK:
            blocks.append((start + off, chunk))
    return blocks


def boot_chain_units(mlo: bytes, uboot: bytes) -> list[Unit]:
    spare = UBOOT + SPARE_OFFSET
    units = [Unit("1-spare-uboot", "spare U-Boot", f"NAND.U-Boot+{SPARE_OFFSET:#x}", spare,
                  UBOOT + ENV_REDUNDANT_OFFSET,
                  place(uboot, spare, UBOOT + ENV_REDUNDANT_OFFSET, True))]
    for n, label in ((2, "NAND.SPL.backup1"), (3, "NAND.SPL.backup2"), (4, "NAND.SPL.backup3"),
                     (5, "NAND.SPL")):
        start, size = PART[label]
        units.append(Unit(f"{n}-{label.removeprefix('NAND.').lower()}",
                          "SPL" if label == "NAND.SPL" else f"SPL backup {label[-1]}",
                          label, start, start + size, place(mlo, start, start + size, True)))
    units.append(Unit("6-first-uboot", "first U-Boot", "NAND.U-Boot+0x0", UBOOT, spare,
                      place(uboot, UBOOT, spare, True)))
    return units


def env_redundant_unit(step: str) -> Unit:
    start = UBOOT + ENV_REDUNDANT_OFFSET
    return Unit(step, "redundant environment (erase)", f"NAND.U-Boot+{ENV_REDUNDANT_OFFSET:#x}",
                start, start + BLOCK)


def backup_units(directory: Path, labels: list[str], with_boot_chain: bool) -> list[Unit]:
    units = []
    if "NAND.U-Boot-Env" in labels and "NAND.U-Boot" not in labels:
        # The other copy would outrank or stand beside the restored one; the restored copy
        # is meant to be the environment this board boots with.
        units.append(env_redundant_unit("backup-env-redundant"))
    # A backup's own boot chain goes back in the order of stockBootSlots: U-Boot first, while
    # the SPL in place still has a spare to fall back to, then the SPL backups, NAND.SPL last.
    first = ["NAND.U-Boot", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3", "NAND.SPL"]
    for label in sorted(labels, key=lambda name: (first.index(name) if name in first else 9,
                                                  PART[name][0])):
        if with_boot_chain and label in BOOT_CHAIN:
            raise Refused(f"{label} is the boot chain this run writes from the release; restore "
                          "a backup's boot chain with --backup-only")
        start, size = PART[label]
        data = backup_data(directory, label)
        units.append(Unit(f"backup-{label.removeprefix('NAND.').lower()}", f"backup {label}",
                          label, start, start + size, place(data, start, start + size, False)))
    return units


def split(units: list[Unit]) -> list[list[Unit]]:
    """Units into sessions of at most MAX_DATA_BLOCKS distinct data blocks, cut at block edges."""
    sessions, current, used = [], [], set()

    def close():
        nonlocal current, used
        if current:
            sessions.append(current)
        current, used = [], set()

    for unit in units:
        start, part = unit.start, 0
        pending = list(unit.blocks)
        while True:
            room = MAX_DATA_BLOCKS - len(used)
            take, fresh = [], set()
            for flash, data in pending:
                key = sha256(data)
                if key not in used and key not in fresh:
                    if len(fresh) == room:
                        break
                    fresh.add(key)
                take.append((flash, data))
            rest = pending[len(take):]
            end = rest[0][0] if rest else unit.end
            if take or end > start:
                suffix = f".{part}" if (part or rest) else ""
                current.append(Unit(unit.step + suffix, unit.label, unit.partition, start, end, take))
                used |= fresh
                part += 1
            if not rest:
                break
            close()
            start, pending = end, rest
    close()
    return sessions


def plan(model: Model, mlo: bytes | None, uboot: bytes | None, backup: Path | None,
         labels: list[str], backup_only: bool, erase_env: bool) -> list[Session]:
    sessions = []
    if not backup_only:
        units = boot_chain_units(mlo, uboot)
        if erase_env:
            units.append(env_redundant_unit("7-env-redundant"))
            start, size = PART["NAND.U-Boot-Env"]
            units.append(Unit("8-env", "environment (erase)", "NAND.U-Boot-Env", start, start + size))
        sessions.append(Session(1, "the release's boot chain", units))
    if labels:
        units = backup_units(backup, labels, not backup_only)
        for group in split(units):
            sessions.append(Session(len(sessions) + 1, "the board's own backup", group))
    if not sessions:
        raise Refused("nothing to do: --backup-only needs --backup and --partition")
    return sessions


# --- what runs on the board -----------------------------------------------------------------

def script_and_blob(session: Session) -> tuple[str, bytes]:
    """The hush script U-Boot sources, and the blob that carries it and the data blocks."""
    addresses, payload = {}, bytearray()
    for _flash, data in session.data_blocks():
        key = sha256(data)
        if key not in addresses:
            addresses[key] = DATA + len(payload)
            payload += data
    ok = "if itest ${ok} == 1; then setenv ok 0; "
    lines = ["setenv ok 1", "setenv step bad-blocks"]
    seen = set()
    for flash, _data in session.data_blocks():
        if flash in seen:
            continue
        seen.add(flash)
        # Raw reads skip nothing: page 0 and page 1 of the block, each 2 KiB plus 64 B OOB.
        # Byte 0 of either OOB other than 0xff is the bad-block marker.
        lines.append(f"if nand read.raw {RAW:#x} {flash:#x} 2; then "
                     f"if itest.b *{RAW + PAGE:#x} != 0xff; then setenv ok 0; fi; "
                     f"if itest.b *{RAW + 2 * PAGE + OOB:#x} != 0xff; then setenv ok 0; fi; "
                     "else setenv ok 0; fi")
    # ${step} keeps the name of the step that failed: it only moves on while all is well.
    for unit in session.units:
        lines.append(f"if itest ${{ok}} == 1; then setenv step {unit.step}; fi")
        lines.append(ok + f"if nand erase {unit.start:#x} {unit.end - unit.start:#x}; then "
                     "setenv ok 1; fi; fi")
        for flash, data in unit.blocks:
            src, crc = addresses[sha256(data)], crc32(data)
            lines.append(ok + f"if nand write.trimffs {src:#x} {flash:#x} {BLOCK:#x}; then "
                         f"if nand read {READBACK:#x} {flash:#x} {BLOCK:#x}; then "
                         f"if crc32 -v {READBACK:#x} {BLOCK:#x} {crc:08x}; then "
                         "setenv ok 1; fi; fi; fi; fi")
    # Everything once more now that all of it is written, the way WriteBootloaders reads both
    # U-Boot copies again at the end.
    lines.append("if itest ${ok} == 1; then setenv step verify; fi")
    for flash, data in session.data_blocks():
        lines.append(ok + f"if nand read {READBACK:#x} {flash:#x} {BLOCK:#x}; then "
                     f"if crc32 -v {READBACK:#x} {BLOCK:#x} {crc32(data):08x}; then "
                     "setenv ok 1; fi; fi; fi")
    lines.append("if itest ${ok} == 1; then setenv stdout nc; setenv stderr nc; "
                 f"echo {MARK_DONE}; reset; fi")
    lines.append("setenv stdout nc; setenv stderr nc; echo RU-failed-${step}")
    script = "\n".join(lines) + "\n"
    image = script_image(script.encode("ascii"), f"soundtap rom-usb {session.index}")
    if len(image) > SCRIPT_AREA:
        raise Refused(f"session {session.index}: script of {len(image)} bytes exceeds {SCRIPT_AREA:#x}")
    blob = image.ljust(SCRIPT_AREA, b"\0") + bytes(payload)
    if len(blob) > BLOB_MAX:
        raise Refused(f"session {session.index}: blob of {len(blob):#x} bytes exceeds {BLOB_MAX:#x}")
    return script, blob


def script_image(body: bytes, name: str) -> bytes:
    """A legacy script uImage, what `mkimage -A arm -O linux -T script -C none` makes."""
    data = struct.pack(">II", len(body), 0) + body

    def header(hcrc: int) -> bytes:
        return struct.pack(">7I4B32s", 0x27051956, hcrc, 0, len(data), 0, 0, crc32(data),
                           5, 2, 6, 0, name.encode("ascii"))
    return header(crc32(header(0))) + data


def uboot_knob(blob: bytes) -> str:
    """WLA_UBOOT_NAND_SCRIPT: the length and crc32 the RAM U-Boot will accept, and nothing else."""
    return f"{len(blob):#x}:{crc32(blob):08x}"


# --- output ---------------------------------------------------------------------------------

def describe(model: Model, source: dict, mlo: bytes | None, uboot: bytes | None,
             sessions: list[Session]) -> dict:
    out = {"model": model.name, "variant": model.variant, "source": source,
           "rom_entry": model.entry,
           "rom_stage": "release SPL, raw" if model.rom_stage == "release"
           else "RAM SPL of the session's U-Boot build", "sessions": []}
    if mlo is not None:
        out["spl"] = {"bytes": len(mlo), "sha256": sha256(mlo)}
    if uboot is not None:
        out["uboot"] = {"bytes": len(uboot), "sha256": sha256(uboot), "limit": MAX_UBOOT}
    for session in sessions:
        script, blob = script_and_blob(session)
        steps = [{"op": "check-bad-blocks",
                  "blocks": sorted({f for f, _ in session.data_blocks()})}]
        for unit in session.units:
            steps.append({"op": "erase", "step": unit.step, "label": unit.label,
                          "partition": unit.partition, "start": unit.start, "end": unit.end})
            for flash, data in unit.blocks:
                steps.append({"op": "write", "step": unit.step, "label": unit.label,
                              "partition": unit.partition, "start": flash, "end": flash + BLOCK,
                              "crc32": f"{crc32(data):08x}"})
        steps.append({"op": "verify", "blocks": [f for f, _ in session.data_blocks()]})
        out["sessions"].append({
            "index": session.index, "title": session.title, "steps": steps,
            "blob": {"name": TFTP_NAME, "bytes": len(blob), "crc32": f"{crc32(blob):08x}",
                     "sha256": sha256(blob)},
            "script_bytes": len(script), "uboot_knob": uboot_knob(blob)})
    return out


def span(step: dict) -> str:
    return f"{step['start']:#x}..{step['end']:#x}".ljust(21)


def print_plan(info: dict, dry: bool) -> None:
    p = print
    p(f"ROM-USB restore for the {MODELS[info['model']].title} ({info['model']}, {info['variant']})"
      + (": dry run, nothing is built, written or sent over USB" if dry else ""))
    for key, value in info["source"].items():
        p(f"  {key:<8} {value}")
    if "spl" in info:
        p(f"  SPL      {info['spl']['bytes']} bytes, sha256 {info['spl']['sha256']}")
    if "uboot" in info:
        p(f"  U-Boot   {info['uboot']['bytes']} bytes (limit {MAX_UBOOT:#x}), "
          f"sha256 {info['uboot']['sha256']}")
    p(f"  ROM USB  {info['rom_entry']}")
    p(f"  ROM gets {info['rom_stage']}")
    for s in info["sessions"]:
        blob = s["blob"]
        p(f"\nsession {s['index']} of {len(info['sessions'])}: {s['title']}")
        p(f"  blob     {blob['name']}, {blob['bytes']:#x} bytes, crc32 {blob['crc32']}, "
          f"sha256 {blob['sha256']}")
        p(f"  build    WLA_UBOOT_NAND_SCRIPT={s['uboot_knob']} sh os/bootloader/build-wla-uboot.sh")
        p(f"  loader   rom-usb-loader --usb-spl ... --watch-file {blob['name']} ... "
          f"--watch-marker {MARK_DONE}")
        p("  on the board, in this order (flash offsets; NAND.U-Boot starts at 0x80000):")
        for step in s["steps"]:
            if step["op"] == "check-bad-blocks":
                p(f"    check   bad-block markers of {len(step['blocks'])} blocks; any bad one "
                  "stops the run before the first erase")
            elif step["op"] == "erase":
                p(f"    erase   {span(step)}  {step['label']} ({step['partition']})")
            elif step["op"] == "write":
                p(f"    write   {span(step)}  {step['label']}, read back, crc32 {step['crc32']}")
            else:
                p(f"    verify  all {len(step['blocks'])} written blocks read back again, "
                  f"then {MARK_DONE} and reset")
    touched = [(st["start"], st["end"]) for s in info["sessions"] for st in s["steps"]
               if st["op"] == "erase"]
    env = UBOOT + ENV_REDUNDANT_OFFSET
    p("")
    for label, (start, end) in (("redundant environment, NAND.U-Boot block 14", (env, env + BLOCK)),
                                ("NAND.U-Boot-Env", (env + BLOCK, env + 2 * BLOCK))):
        if not any(a < end and start < b for a, b in touched):
            p(f"untouched: {start:#x}..{end:#x} ({label})")


# --- running it -----------------------------------------------------------------------------

def run(cmd: list[str], **kw) -> None:
    print("+ " + shlex.join(cmd), flush=True)
    subprocess.run(cmd, check=True, **kw)


def build_session(session: Session, model: Model, mlo: bytes | None, work: Path) -> dict:
    script, blob = script_and_blob(session)
    sdir = work / f"session-{session.index}"
    (sdir / "load").mkdir(parents=True, exist_ok=True)
    (sdir / TFTP_NAME).write_bytes(blob)
    (sdir / "script.txt").write_text(script)
    out = sdir / "uboot"
    env = {**os.environ, "WLA_UBOOT_OUT": str(out), "WLA_UBOOT_NAND_SCRIPT": uboot_knob(blob)}
    run(["sh", str(UBOOT_BUILDER)], env=env)
    spl = rom_stage_spl(mlo) if model.rom_stage == "release" else (out / "MLO").read_bytes()
    (sdir / "load/MLO").write_bytes(spl)
    shutil.copyfile(out / "u-boot.img", sdir / "load/u-boot.img")
    return {"dir": sdir, "mlo": sha256(spl), "uboot": sha256((out / "u-boot.img").read_bytes()),
            "blob": sha256(blob)}


def loader_command(args, built: dict, check: bool) -> list[str]:
    sdir = built["dir"]
    inner = ["--usb-spl", "--mlo-sha256", built["mlo"], "--uboot-sha256", built["uboot"]]
    if args.loader == "docker":
        base = ["docker", "run", "--rm", "-v", f"{REPO}:/repo:ro", "-v", f"{sdir}:/session:ro",
                "-w", "/repo/tools/recovery/rom-usb-loader"]
        if not check:
            base[3:3] = ["--privileged", "-v", "/dev/bus/usb:/dev/bus/usb"]
        prefix, root = [*base, LOADER_IMAGE, "./rom-usb-loader"], "/session"
    else:
        prefix, root = [str(LOADER_DIR / "rom-usb-loader")], str(sdir)
    cmd = prefix + inner + [f"{root}/load/MLO", f"{root}/load/u-boot.img",
                            "--watch", str(args.watch),
                            "--watch-file", TFTP_NAME, built["blob"], f"{root}/{TFTP_NAME}",
                            "--watch-marker", MARK_DONE]
    return [*cmd, "--check"] if check else cmd


def confirm(session: dict, model: Model) -> None:
    print(f"\nSession {session['index']} erases and writes the ranges listed above on this "
          f"{model.title}. {model.entry}")
    answer = input("Type 'write' to bring it up over ROM USB and write, anything else stops: ")
    if answer.strip() != "write":
        raise Refused(f"session {session['index']} not confirmed; nothing was written in it")


def execute(args, model: Model, mlo: bytes | None, sessions: list[Session], info: dict) -> None:
    work = Path(args.work).resolve() if args.work else Path(tempfile.mkdtemp(prefix="rom-usb-restore-"))
    work.mkdir(parents=True, exist_ok=True)
    if args.loader == "docker":
        run(["sh", str(LOADER_DIR / "build.sh"), "all"])
    else:
        run(["make", "-C", str(LOADER_DIR), "all"])
    for session, described in zip(sessions, info["sessions"]):
        built = build_session(session, model, mlo, work)
        # The same command with --check reads every input and stops before libusb.
        run(loader_command(args, built, check=True))
        confirm(described, model)
        if args.power_cycle:
            run([*shlex.split(args.power_cycle), "off"])
            time.sleep(6)
        log = built["dir"] / "loader.log"
        cmd = loader_command(args, built, check=False)
        print("+ " + shlex.join(cmd) + f" > {log}", flush=True)
        with log.open("wb") as out:
            loader = subprocess.Popen(cmd, stdout=out, stderr=subprocess.STDOUT)
            if args.power_cycle:
                time.sleep(2)
                run([*shlex.split(args.power_cycle), "on"])
            else:
                print(f"Power the board on into ROM USB now; the loader waits 60 s. {model.entry}")
            code = loader.wait(timeout=args.watch + 300)
        if code != 0:
            raise Refused(f"session {session.index}: the loader ended with {code} and did not see "
                          f"{MARK_DONE}; see {log}. The board may hold a partial write: run this "
                          "session again, or read the ranges back before trusting them.")
        print(f"session {session.index}: {MARK_DONE} seen, the board reset itself")
    print("\nDone. Every block was read back on the board; the board boots its new chain now.")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0],
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--model", required=True, choices=sorted(MODELS))
    src = ap.add_mutually_exclusive_group()
    src.add_argument("--images", type=Path, help="a built output/os/images (with SHA256SUMS)")
    src.add_argument("--stu", type=Path, help="the release's universal STU")
    ap.add_argument("--backup", type=Path, help="the board's own nand-backup-ssh.sh directory")
    ap.add_argument("--partition", action="append", default=[],
                    help="a partition to restore from --backup (mtdN or label); repeatable")
    ap.add_argument("--backup-only", action="store_true",
                    help="restore only from --backup; do not write the release's boot chain")
    ap.add_argument("--erase-env", action="store_true",
                    help="also erase both environment copies, so U-Boot boots its default")
    ap.add_argument("--dry-run", action="store_true", help="print the plan; touch nothing")
    ap.add_argument("--json", action="store_true", help="print the plan as JSON; touch nothing")
    ap.add_argument("--loader", choices=("docker", "native"), default="docker")
    ap.add_argument("--power-cycle", metavar="CMD",
                    help="a command run as `CMD off` and `CMD on` around the loader start")
    ap.add_argument("--watch", type=int, default=900, help="seconds the loader watches (max 3600)")
    ap.add_argument("--work", help="where blobs, builds and logs go (default: a new temp dir)")
    args = ap.parse_args(argv)
    model = MODELS[args.model]
    try:
        if args.partition and not args.backup:
            raise Refused("--partition needs --backup")
        if args.backup and not args.partition:
            raise Refused("--backup needs at least one --partition: name what to restore")
        if args.erase_env and args.backup_only:
            raise Refused("--erase-env goes with the release's boot chain, not --backup-only")
        labels = list(dict.fromkeys(partition_by_name(n) for n in args.partition))
        if args.erase_env and {"NAND.U-Boot-Env", "NAND.U-Boot"} & set(labels):
            raise Refused("--erase-env and restoring the environment from the backup exclude "
                          "each other")
        if not 1 <= args.watch <= 3600:
            raise Refused("--watch must be 1..3600 seconds")
        need_images = not args.backup_only or model.rom_stage == "release"
        mlo = uboot = None
        source = {}
        if need_images:
            if args.images:
                mlo, uboot, source = images_from_dir(args.images, model)
            elif args.stu:
                mlo, uboot, source = images_from_stu(args.stu, model)
            else:
                raise Refused("give the release's images: --images DIR or --stu FILE")
            check_mlo(mlo, source["spl"])
            check_uboot(uboot, source["uboot"])
            if model.rom_stage == "release":
                rom_stage_spl(mlo)
        if args.backup:
            source["backup"] = str(args.backup)
        sessions = plan(model, mlo, uboot, args.backup, labels, args.backup_only, args.erase_env)
        info = describe(model, source, mlo, None if args.backup_only else uboot, sessions)
        if args.json:
            print(json.dumps(info, indent=1))
            return 0
        print_plan(info, args.dry_run)
        if args.dry_run:
            return 0
        execute(args, model, mlo, sessions, info)
        return 0
    except Refused as err:
        print(f"refused: {err}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
