#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Static safety check for opt-in WLA product-SPL NAND markers."""
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BUILDER = ROOT / "os/bootloader/build-wla-product-spl.sh"
PATCH = ROOT / "os/bootloader/uboot/wla-product-spl-nand-markers.patch"


def main() -> None:
    """Check that the marker patch is opt-in, applies and writes only its diagnostic blocks."""
    builder = BUILDER.read_text()
    patch = PATCH.read_text()
    opt_in = '[ "\'"$NAND_MARKERS"\'" = 1 ] && git apply /repo/os/bootloader/uboot/wla-product-spl-nand-markers.patch'
    if "NAND_MARKERS=${WLA_PRODUCT_SPL_NAND_MARKERS:-}" not in builder or opt_in not in builder:
        msg = "marker patch is not explicit opt-in"
        raise SystemExit(msg)
    if builder.index("git apply /repo/os/bootloader/uboot/wla-uboot-nand-base.patch") > builder.index(opt_in):
        msg = "marker patch must follow retained NAND pinmux patch"
        raise SystemExit(msg)
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
            msg = f"missing marker safety contract: {value}"
            raise SystemExit(msg)
    forbidden = ("0x00000 / CONFIG_SYS_NAND_BLOCK_SIZE", "0xcc80000", "0xfe00000")
    for value in forbidden:
        if value in patch.lower():
            msg = f"marker patch references forbidden NAND region: {value}"
            raise SystemExit(msg)
    if "this->write_buf" in patch:
        msg = "SPL marker patch must not call unavailable write_buf"
        raise SystemExit(msg)
    uboot = ROOT / "os/bootloader/build/cache-wla-uboot/u-boot-2026.07"
    if not uboot.is_dir():
        msg = f"missing cached U-Boot tree: {uboot}"
        raise SystemExit(msg)
    subprocess.run(["git", "-C", str(uboot), "apply", "--check", str(PATCH)], check=True)
    if "--disable SPL_SYS_MALLOC --set-val SPL_SYS_MALLOC_F_LEN 0x2000" not in builder:
        msg = "product SPL malloc configuration changed"
        raise SystemExit(msg)
    print("WLA product SPL NAND markers passed: explicit opt-in, mtd11 blocks 0-1 only")


if __name__ == "__main__":
    main()
