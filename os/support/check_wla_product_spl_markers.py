#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Static safety check for opt-in WLA product-SPL NAND markers."""
from pathlib import Path
import subprocess


ROOT = Path(__file__).resolve().parents[2]
BUILDER = ROOT / "os/bootloader/build-wla-product-spl.sh"
PATCH = ROOT / "os/bootloader/uboot/wla-product-spl-nand-markers.patch"


def main() -> None:
    builder = BUILDER.read_text()
    patch = PATCH.read_text()
    opt_in = '[ "\'"$NAND_MARKERS"\'" = 1 ] && git apply /repo/os/bootloader/uboot/wla-product-spl-nand-markers.patch'
    if "NAND_MARKERS=${WLA_PRODUCT_SPL_NAND_MARKERS:-}" not in builder or opt_in not in builder:
        raise SystemExit("marker patch is not explicit opt-in")
    if builder.index("git apply /repo/os/bootloader/uboot/wla-uboot-nand-base.patch") > builder.index(opt_in):
        raise SystemExit("marker patch must follow retained NAND pinmux patch")
    required = (
        "#define WLA_DIAG_FIRST_BLOCK\t(0xf280000 / CONFIG_SYS_NAND_BLOCK_SIZE)",
        "WLA_DIAG_FIRST_BLOCK + stage - 1",
        '"SOUNDTAP-SPL:01\\n", "SOUNDTAP-SPL:02\\n"',
        "wla_spl_diag_mark(1);",
        "wla_spl_diag_mark(2);",
        "NAND_CMD_ERASE1",
        "NAND_CMD_PAGEPROG",
        "WLA_DIAG_WAIT_US\t1000000",
        "inspect with nanddump --noecc",
        "writeb(buf[i], this->IO_ADDR_R)",
        "ndelay(150);",
    )
    for value in required:
        if value not in patch:
            raise SystemExit(f"missing marker safety contract: {value}")
    forbidden = ("0x00000 / CONFIG_SYS_NAND_BLOCK_SIZE", "0xcc80000", "0xfe00000")
    for value in forbidden:
        if value in patch.lower():
            raise SystemExit(f"marker patch references forbidden NAND region: {value}")
    if "this->write_buf" in patch:
        raise SystemExit("SPL marker patch must not call unavailable write_buf")
    uboot = ROOT / "os/bootloader/build/cache-wla-uboot/u-boot-2026.07"
    if not uboot.is_dir():
        raise SystemExit(f"missing cached U-Boot tree: {uboot}")
    subprocess.run(["git", "-C", str(uboot), "apply", "--check", str(PATCH)], check=True)
    if "--disable SPL_SYS_MALLOC --set-val SPL_SYS_MALLOC_F_LEN 0x2000" not in builder:
        raise SystemExit("product SPL malloc configuration changed")
    print("WLA product SPL NAND markers passed: explicit opt-in, mtd11 blocks 0-1 only")


if __name__ == "__main__":
    main()
