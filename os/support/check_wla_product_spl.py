#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Check product WLA SPL loads U-Boot from NAND, not lab transports."""
import hashlib
import struct
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import stu_check

ROM_GP_SIZE_LIMIT = 0x7428
ROM_PAGE_LIMIT = 15
NAND_PAGE_SIZE = 0x800
GP_HEADER_OFFSET = 0x200
SPL_LOAD_ADDRESS = 0x402F0400
PARTITION_LIMIT = 128 * 1024
REQUIRED_CONFIG = (
    'CONFIG_DEFAULT_DEVICE_TREE="am335x-soundtap-wla"', "CONFIG_SPL_MTD=y",
    "CONFIG_SPL_LOAD_BLOCK=y", "CONFIG_SPL_NAND_IDENT=y",
    "CONFIG_SPL_NAND_SUPPORT=y", "CONFIG_SPL_NAND_DRIVERS=y",
    "CONFIG_SPL_NAND_ECC=y", "CONFIG_SPL_NAND_BASE=y",
    "CONFIG_SPL_NAND_AM33XX_BCH=y",
    "CONFIG_SPL_LEGACY_IMAGE_FORMAT=y", "CONFIG_NAND_OMAP_GPMC=y",
    "CONFIG_NAND_OMAP_ELM=y", "CONFIG_NAND_OMAP_ECCSCHEME_BCH8_CODE_HW=y",
    "CONFIG_SYS_NAND_U_BOOT_LOCATIONS=y", "CONFIG_SYS_NAND_U_BOOT_OFFS=0x80000",
    "CONFIG_SYS_NAND_U_BOOT_OFFS_REDUND=0x180000", "CONFIG_SPL_LEGACY_IMAGE_CRC_CHECK=y",
    "CONFIG_SYS_NAND_PAGE_SIZE=0x800", "CONFIG_SYS_NAND_OOBSIZE=0x40",
    "CONFIG_SYS_NAND_BLOCK_SIZE=0x20000", "CONFIG_SYS_NAND_ONFI_DETECTION=y",
    "CONFIG_SPL_TEXT_BASE=0x402f0400", "CONFIG_SPL_SIZE_LIMIT=0x7420",
)
ABSENT_CONFIG = (
    "CONFIG_SPL_LOAD_FIT=y", "CONFIG_SPL_OS_BOOT=y", "CONFIG_SPL_I2C=y",
    "CONFIG_SPL_DM_I2C=y", "CONFIG_SPL_POWER=y", "CONFIG_SPL_POWER_TPS65910=y",
    "CONFIG_SPL_PMIC=y", "CONFIG_SPL_SERIAL=y", "CONFIG_SPL_YMODEM_SUPPORT=y",
    "CONFIG_SPL_WDT=y", "CONFIG_SPL_FAT_SUPPORT=y", "CONFIG_SPL_LIBDISK_SUPPORT=y",
    "CONFIG_SPL_FS_FAT=y", "CONFIG_SPL_SYS_MALLOC=y",
    "CONFIG_SPL_USB_GADGET=y", "CONFIG_SPL_USB_ETHER=y", "CONFIG_SPL_NET=y",
    "CONFIG_SPL_ETH=y", "CONFIG_SPL_MMC=y", "CONFIG_MMC=y", "CONFIG_USB=y",
    "CONFIG_USB_GADGET=y", "CONFIG_NET=y", "CONFIG_CMD_DHCP=y",
    "CONFIG_CMD_TFTPBOOT=y", "CONFIG_CMD_BOOTP=y", "CONFIG_CMD_MMC=y",
    "CONFIG_USB_ETHER=y", "CONFIG_USB_ETH_RNDIS=y", "CONFIG_USB_GADGET_DOWNLOAD=y",
    "CONFIG_USB_FUNCTION_ACM=y",
    "CONFIG_SPL_MUSB_NEW=y", "CONFIG_SPL_DM_USB_GADGET=y",
    "CONFIG_SPL_FALCON_BOOT_MMCSD=y", "CONFIG_SPL_SYS_MMCSD_RAW_MODE=y",
)


def provenance_value(provenance: str, key: str) -> str:
    for line in provenance.splitlines():
        if line.startswith(f"{key}="):
            return line.removeprefix(f"{key}=")
    raise SystemExit(f"missing provenance: {key}")


def packaged_mlo(stu: Path) -> bytes:
    data = stu.read_bytes()
    for _, image, size, name in stu_check.modules(data):
        if name == "soundtap-spl":
            return data[image:image + size - stu_check.OVERLAP]
    raise SystemExit("STU has no soundtap-spl module")


def check_rom_ceiling(gp_size: int, mlo_size: int) -> int:
    if gp_size > ROM_GP_SIZE_LIMIT:
        raise SystemExit(f"MLO GP payload {gp_size:#x} exceeds measured stock ROM ceiling {ROM_GP_SIZE_LIMIT:#x}")
    pages = (mlo_size + NAND_PAGE_SIZE - 1) // NAND_PAGE_SIZE
    if pages > ROM_PAGE_LIMIT:
        raise SystemExit(f"MLO needs {pages} NAND pages, exceeds measured stock ROM ceiling {ROM_PAGE_LIMIT}")
    return pages


def check_packaged_mlo(out: Path, stu: Path, provenance: str) -> None:
    mlo = (out / "MLO").read_bytes()
    digest = hashlib.sha256(mlo).hexdigest()
    if provenance_value(provenance, "mlo_sha256") != digest:
        raise SystemExit("MLO does not match provenance mlo_sha256")
    if provenance_value(provenance, "config_sha256") != hashlib.sha256((out / ".config").read_bytes()).hexdigest():
        raise SystemExit("product SPL config does not match provenance config_sha256")
    if packaged_mlo(stu) != mlo + b"\xff" * stu_check.OVERLAP:
        raise SystemExit("STU MLO payload does not match product SPL MLO")


def main(out: Path, stu: Path | None = None) -> None:
    mlo = (out / "MLO").read_bytes()
    config = (out / ".config").read_text()
    symbols = (out / "spl-symbols.txt").read_text()
    disassembly = (out / "spl-disassembly.txt").read_text()
    provenance = (out / "PROVENANCE").read_text()
    # This SPL is booted from NAND by the mask ROM, never over USB, so it carries the
    # header the ROM reads there: a TI configuration header, then a GP header giving
    # size and load address, then the SPL.
    # records the same layout read out of the stock mtd0 on real hardware. The raw
    # vectors-at-zero form belongs to the USB path and is checked in
    # os/support/check_wla_uboot_ram.py.
    spl = (out / "u-boot-spl.bin").read_bytes()
    if mlo[:4] != bytes.fromhex("40000000") or b"CHSETTINGS" not in mlo[:GP_HEADER_OFFSET]:
        raise SystemExit("MLO lacks the TI configuration header the ROM reads from NAND")
    size, load = struct.unpack_from("<II", mlo, GP_HEADER_OFFSET)
    if load != SPL_LOAD_ADDRESS:
        raise SystemExit(f"MLO GP header loads {load:#x}, not {SPL_LOAD_ADDRESS:#x}")
    # mkimage counts its own 8-byte GP header in the size it writes. Pinned rather than
    # explained: what the ROM does with a size field is not something this repo has
    # measured, and the stock image's own size field is smaller than the bytes present.
    if size != len(spl) + 8:
        raise SystemExit(f"MLO GP header declares {size} bytes, expected {len(spl) + 8}")
    body = GP_HEADER_OFFSET + 8
    if mlo[body:body + 4] != bytes.fromhex("0f0000ea"):
        raise SystemExit("MLO has no ARM SPL vectors after its GP header")
    if mlo[body:] != spl or len(mlo) != body + len(spl):
        raise SystemExit("MLO body is not exactly spl/u-boot-spl.bin")
    pages = check_rom_ceiling(size, len(mlo))
    if len(mlo) > PARTITION_LIMIT:
        raise SystemExit(f"MLO {len(mlo)} exceeds mtd0 128KiB")
    if (out / "MLO.sha256").read_text().split()[0] != hashlib.sha256(mlo).hexdigest():
        raise SystemExit("MLO.sha256 does not match MLO")
    for value in REQUIRED_CONFIG:
        if value not in config.splitlines():
            raise SystemExit(f"missing {value}")
    for value in ABSENT_CONFIG:
        if value in config.splitlines():
            raise SystemExit(f"unexpected lab feature {value}")
    if " spl_nand_load_image" not in symbols:
        raise SystemExit("SPL lacks NAND U-Boot loader")
    if " spl_parse_image_header" not in symbols:
        raise SystemExit("SPL lacks legacy image parser")
    nand_loader = disassembly.split("<spl_nand_load_image>:", 1)[1].split("\n\n", 1)[0]
    for symbol in ("<nand_init>", "<spl_nand_load_element>"):
        if symbol not in nand_loader:
            raise SystemExit(f"NAND loader does not call {symbol}")
    nand_element = disassembly.split("<spl_nand_load_element>:", 1)[1].split("\n\n", 1)[0]
    if "<spl_parse_image_header>" not in nand_element:
        raise SystemExit("NAND loader does not parse U-Boot image header")
    for symbol in ("spl_usb", "spl_net", "spl_mmc", "spl_dfu", "usb_ether", "cpsw"):
        if symbol in symbols:
            raise SystemExit(f"SPL retains lab loader {symbol}")
    for value in ("source_revision=ece349ade2973e220f524ce59e59711cc919263f",
                   "source_sha256=78e8bfc382fe388f9b55aa1daf8c563522a037779b5d4c349d1415e381f1243e",
                   "container=debian:bookworm-slim@sha256:", "config_sha256=", "mlo_sha256="):
        if value not in provenance:
            raise SystemExit(f"missing provenance: {value}")
    for name in ("u-boot-spl.map", "spl-size.txt", "spl-disassembly.txt"):
        if not (out / name).is_file() or not (out / name).stat().st_size:
            raise SystemExit(f"missing {name}")
    if stu is not None:
        check_packaged_mlo(out, stu, provenance)
    print(f"product WLA SPL passed: NAND loader, MLO={len(mlo)} bytes/{pages} pages, GP={size:#x}")


if __name__ == "__main__":
    if not 2 <= len(sys.argv) <= 3:
        raise SystemExit(f"usage: {sys.argv[0]} OUT [STU]")
    main(Path(sys.argv[1]), Path(sys.argv[2]) if len(sys.argv) == 3 else None)
