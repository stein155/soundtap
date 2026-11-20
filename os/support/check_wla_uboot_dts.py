#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Reject WLA U-Boot DTS placement U-Boot cannot build."""

import sys
from pathlib import Path


def main() -> None:
    tree = Path(sys.argv[1]) if len(sys.argv) == 2 else Path(".")
    config = (tree / ".config").read_text()
    if 'CONFIG_DEFAULT_DEVICE_TREE="am335x-soundtap-wla"' not in config.splitlines():
        raise SystemExit("WLA default device tree missing")
    if "# CONFIG_OF_UPSTREAM is not set" not in config.splitlines():
        raise SystemExit("WLA DTS must use arch/arm/dts")
    dts = tree / "arch/arm/dts/am335x-soundtap-wla.dts"
    if not dts.is_file():
        raise SystemExit(f"WLA DTS missing: {dts}")
    makefile = (dts.parent / "Makefile").read_text()
    if "dtb-y += am335x-soundtap-wla.dtb" not in makefile:
        raise SystemExit("WLA DTS missing from arch/arm/dts/Makefile")
    print("WLA U-Boot DTS path/config passed")


if __name__ == "__main__":
    main()
