#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Hardening options the speaker kernel must have, or must not, plus the
options hardware needs that a fragment rewrite once dropped unnoticed.

    kernel_config_check.py --fragment os/board/image/linux.fragment   # lint, no build
    kernel_config_check.py output/os/images/kernel.config             # what was built
"""

import argparse
import sys
from pathlib import Path

DENY = (
    "KPROBES",
    "BINFMT_MISC",
    "NET_KEY",
    "XFRM_USER",
    "IP_PNP",
    "NFS_FS",
    "ROOT_NFS",
    "MAGIC_SYSRQ",
    "MODULE_FORCE_LOAD",
    "MODULE_FORCE_UNLOAD",
    # A per-build signing key makes every kernel build differ (ADR 0011); init.d sets
    # modules_disabled after the ST300's modules instead (ADR 0048).
    "MODULE_SIG",
    "IO_STRICT_DEVMEM",
    # USB drivers no model uses (linux.fragment says why): adapters, HID and controllers
    # that only widen what a plugged-in device can reach in the kernel.
    "USB_NET_AX88179_178A",
    "USB_NET_AX8817X",
    "AX88796B_PHY",
    "USB_NET_NET1080",
    "USB_NET_ZAURUS",
    "USB_NET_CDC_SUBSET",
    "USB_HID",
    "RTL_CARDS",
    "USB_DWC3",
    "USB_ISP1760",
)
REQUIRE = (
    "STRICT_KERNEL_RWX",
    "STACKPROTECTOR_STRONG",
    "STRICT_DEVMEM",
    # The WLA's two buttons are gpio-keys-polled in its device tree; without this
    # driver they do not exist, and no press can confirm a request.
    "KEYBOARD_GPIO_POLLED",
    # An oops panics, so a half-dead kernel resets instead of being fed to the watchdog.
    "PANIC_ON_OOPS",
)
# Seconds before a panic resets the board (below 0: at once); 0 waits for ever.
NONZERO = ("PANIC_TIMEOUT",)


def problems(text: str, fragment: bool) -> list[str]:
    lines = set(text.splitlines())
    out = []
    for name in DENY:
        on = f"CONFIG_{name}=y" in lines or f"CONFIG_{name}=m" in lines
        if on or (fragment and f"# CONFIG_{name} is not set" not in lines):
            out.append(f"CONFIG_{name} must be off")
    for name in REQUIRE:
        if f"CONFIG_{name}=y" not in lines:
            out.append(f"CONFIG_{name} must be y")
    for name in NONZERO:
        values = [line.split("=", 1)[1] for line in lines if line.startswith(f"CONFIG_{name}=")]
        if not values or not values[0].lstrip("-").isdigit() or int(values[0]) == 0:
            out.append(f"CONFIG_{name} must be set and not 0")
    return out


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("config", type=Path)
    parser.add_argument("--fragment", action="store_true", help="the input is linux.fragment")
    args = parser.parse_args()
    found = problems(args.config.read_text(), args.fragment)
    for line in found:
        print(f"{args.config}: {line}", file=sys.stderr)
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
