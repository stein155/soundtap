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
# mkimage's GP header: size and load address, counted in the size it declares.
GP_HEADER_SIZE = 8
# `check_wla_product_spl.py OUT [STU]`: the argv lengths with and without the STU.
ARGC_WITHOUT_STU = 2
ARGC_WITH_STU = 3
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
    """Return the value of key= in PROVENANCE."""
    for line in provenance.splitlines():
        if line.startswith(f"{key}="):
            return line.removeprefix(f"{key}=")
    msg = f"missing provenance: {key}"
    raise SystemExit(msg)


def packaged_mlo(stu: Path) -> bytes:
    """Return the soundtap-spl module of the STU, without the overlap."""
    data = stu.read_bytes()
    for _, image, size, name in stu_check.modules(data):
        if name == "soundtap-spl":
            return data[image:image + size - stu_check.OVERLAP]
    msg = "STU has no soundtap-spl module"
    raise SystemExit(msg)


def check_rom_ceiling(gp_size: int, mlo_size: int) -> int:
    """Check the MLO fits under the stock ROM's measured ceiling and return its NAND pages."""
    if gp_size > ROM_GP_SIZE_LIMIT:
        msg = f"MLO GP payload {gp_size:#x} exceeds measured stock ROM ceiling {ROM_GP_SIZE_LIMIT:#x}"
        raise SystemExit(msg)
    pages = (mlo_size + NAND_PAGE_SIZE - 1) // NAND_PAGE_SIZE
    if pages > ROM_PAGE_LIMIT:
        msg = f"MLO needs {pages} NAND pages, exceeds measured stock ROM ceiling {ROM_PAGE_LIMIT}"
        raise SystemExit(msg)
    return pages


def check_packaged_mlo(out: Path, stu: Path, provenance: str) -> None:
    """Check the STU carries this MLO and the provenance names it and its config."""
    mlo = (out / "MLO").read_bytes()
    digest = hashlib.sha256(mlo).hexdigest()
    if provenance_value(provenance, "mlo_sha256") != digest:
        msg = "MLO does not match provenance mlo_sha256"
        raise SystemExit(msg)
    if provenance_value(provenance, "config_sha256") != hashlib.sha256((out / ".config").read_bytes()).hexdigest():
        msg = "product SPL config does not match provenance config_sha256"
        raise SystemExit(msg)
    if packaged_mlo(stu) != mlo + b"\xff" * stu_check.OVERLAP:
        msg = "STU MLO payload does not match product SPL MLO"
        raise SystemExit(msg)


def main(out: Path, stu: Path | None = None) -> None:
    """Check the product SPL build in out, and its copy in the STU when given."""
    mlo = (out / "MLO").read_bytes()
    config = (out / ".config").read_text()
    symbols = (out / "spl-symbols.txt").read_text()
    disassembly = (out / "spl-disassembly.txt").read_text()
    provenance = (out / "PROVENANCE").read_text()
    spl = (out / "u-boot-spl.bin").read_bytes()
    size = check_mlo_layout(mlo, spl)
    pages = check_rom_ceiling(size, len(mlo))
    if len(mlo) > PARTITION_LIMIT:
        msg = f"MLO {len(mlo)} exceeds mtd0 128KiB"
        raise SystemExit(msg)
    if (out / "MLO.sha256").read_text().split()[0] != hashlib.sha256(mlo).hexdigest():
        msg = "MLO.sha256 does not match MLO"
        raise SystemExit(msg)
    check_config(config)
    check_nand_loader(symbols, disassembly)
    check_provenance(out, provenance)
    if stu is not None:
        check_packaged_mlo(out, stu, provenance)
    print(f"product WLA SPL passed: NAND loader, MLO={len(mlo)} bytes/{pages} pages, GP={size:#x}")


def check_mlo_layout(mlo: bytes, spl: bytes) -> int:
    """Check the MLO is the ROM's NAND headers around exactly this SPL, and return the GP size."""
    # This SPL is booted from NAND by the mask ROM, never over USB, so it carries the
    # header the ROM reads there: a TI configuration header, then a GP header giving
    # size and load address, then the SPL.
    # records the same layout read out of the stock mtd0 on real hardware. The raw
    # vectors-at-zero form belongs to the USB path and is checked in
    # os/support/check_wla_uboot_ram.py.
    if mlo[:4] != bytes.fromhex("40000000") or b"CHSETTINGS" not in mlo[:GP_HEADER_OFFSET]:
        msg = "MLO lacks the TI configuration header the ROM reads from NAND"
        raise SystemExit(msg)
    size, load = struct.unpack_from("<II", mlo, GP_HEADER_OFFSET)
    if load != SPL_LOAD_ADDRESS:
        msg = f"MLO GP header loads {load:#x}, not {SPL_LOAD_ADDRESS:#x}"
        raise SystemExit(msg)
    # mkimage counts its own 8-byte GP header in the size it writes. Pinned rather than
    # explained: what the ROM does with a size field is not something this repo has
    # measured, and the stock image's own size field is smaller than the bytes present.
    if size != len(spl) + GP_HEADER_SIZE:
        msg = f"MLO GP header declares {size} bytes, expected {len(spl) + GP_HEADER_SIZE}"
        raise SystemExit(msg)
    body = GP_HEADER_OFFSET + GP_HEADER_SIZE
    if mlo[body:body + 4] != bytes.fromhex("0f0000ea"):
        msg = "MLO has no ARM SPL vectors after its GP header"
        raise SystemExit(msg)
    if mlo[body:] != spl or len(mlo) != body + len(spl):
        msg = "MLO body is not exactly spl/u-boot-spl.bin"
        raise SystemExit(msg)
    return size


def check_config(config: str) -> None:
    """Check the SPL is configured for NAND and carries no lab transport."""
    for value in REQUIRED_CONFIG:
        if value not in config.splitlines():
            msg = f"missing {value}"
            raise SystemExit(msg)
    for value in ABSENT_CONFIG:
        if value in config.splitlines():
            msg = f"unexpected lab feature {value}"
            raise SystemExit(msg)


def check_nand_loader(symbols: str, disassembly: str) -> None:
    """Check the SPL loads a legacy U-Boot image from NAND and keeps no lab loader."""
    if " spl_nand_load_image" not in symbols:
        msg = "SPL lacks NAND U-Boot loader"
        raise SystemExit(msg)
    if " spl_parse_image_header" not in symbols:
        msg = "SPL lacks legacy image parser"
        raise SystemExit(msg)
    nand_loader = disassembly.split("<spl_nand_load_image>:", 1)[1].split("\n\n", 1)[0]
    for symbol in ("<nand_init>", "<spl_nand_load_element>"):
        if symbol not in nand_loader:
            msg = f"NAND loader does not call {symbol}"
            raise SystemExit(msg)
    nand_element = disassembly.split("<spl_nand_load_element>:", 1)[1].split("\n\n", 1)[0]
    if "<spl_parse_image_header>" not in nand_element:
        msg = "NAND loader does not parse U-Boot image header"
        raise SystemExit(msg)
    for symbol in ("spl_usb", "spl_net", "spl_mmc", "spl_dfu", "usb_ether", "cpsw"):
        if symbol in symbols:
            msg = f"SPL retains lab loader {symbol}"
            raise SystemExit(msg)


def check_provenance(out: Path, provenance: str) -> None:
    """Check PROVENANCE pins the source and container, and the build left its records."""
    for value in ("source_revision=ece349ade2973e220f524ce59e59711cc919263f",
                   "source_sha256=78e8bfc382fe388f9b55aa1daf8c563522a037779b5d4c349d1415e381f1243e",
                   "container=debian:bookworm-slim@sha256:", "config_sha256=", "mlo_sha256="):
        if value not in provenance:
            msg = f"missing provenance: {value}"
            raise SystemExit(msg)
    for name in ("u-boot-spl.map", "spl-size.txt", "spl-disassembly.txt"):
        if not (out / name).is_file() or not (out / name).stat().st_size:
            msg = f"missing {name}"
            raise SystemExit(msg)


if __name__ == "__main__":
    if not ARGC_WITHOUT_STU <= len(sys.argv) <= ARGC_WITH_STU:
        msg = f"usage: {sys.argv[0]} OUT [STU]"
        raise SystemExit(msg)
    main(Path(sys.argv[1]), Path(sys.argv[2]) if len(sys.argv) == ARGC_WITH_STU else None)
