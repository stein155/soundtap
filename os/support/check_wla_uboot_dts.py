#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Reject WLA U-Boot DTS placement U-Boot cannot build."""

import sys
from pathlib import Path

USAGE_ARGC = 2


def main() -> None:
    """Check the WLA device tree is the default, in arch/arm/dts and in its Makefile."""
    tree = Path(sys.argv[1]) if len(sys.argv) == USAGE_ARGC else Path()
    config = (tree / ".config").read_text()
    if 'CONFIG_DEFAULT_DEVICE_TREE="am335x-soundtap-wla"' not in config.splitlines():
        msg = "WLA default device tree missing"
        raise SystemExit(msg)
    if "# CONFIG_OF_UPSTREAM is not set" not in config.splitlines():
        msg = "WLA DTS must use arch/arm/dts"
        raise SystemExit(msg)
    dts = tree / "arch/arm/dts/am335x-soundtap-wla.dts"
    if not dts.is_file():
        msg = f"WLA DTS missing: {dts}"
        raise SystemExit(msg)
    makefile = (dts.parent / "Makefile").read_text()
    if "dtb-y += am335x-soundtap-wla.dtb" not in makefile:
        msg = "WLA DTS missing from arch/arm/dts/Makefile"
        raise SystemExit(msg)
    print("WLA U-Boot DTS path/config passed")


if __name__ == "__main__":
    main()
