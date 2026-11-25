#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Check that a device tree's `mem=` matches the memory it declares.

The WLA shipped with `mem=256M` against a `memory@80000000` node of 0x10000000 on a
board with 128 MiB. Linux believed both, allocated into the half that is not there, and
the upper addresses aliased onto the lower ones: soundtapd died a few minutes in with a Go
stack full of ASCII and "panic during panic". U-Boot's get_ram_size() had been reporting
0x08000000 the whole time, and stock Bose U-Boot passes mem=128M.

Nothing compared the two numbers, so this does. A device tree may leave either out -- the
RAM-boot trees carry no memory node -- but where both exist they must agree, and whatever
they state must be the figure every SoundTouch here has.

    check_dts_memory.py [--expect 128M] [DTS...]   default: os/bootloader/*.dts
"""

import re
import sys
from pathlib import Path

BOOTARGS = re.compile(r'bootargs\s*=\s*"([^"]*)"')
MEM_ARG = re.compile(r"\bmem=(\d+)([MmKkGg])")
MEMORY_NODE = re.compile(r"memory@[0-9a-fA-F]+\s*\{[^}]*?reg\s*=\s*<\s*(0x[0-9a-fA-F]+|\d+)\s+(0x[0-9a-fA-F]+|\d+)\s*>", re.DOTALL)

SCALE = {"k": 1 << 10, "m": 1 << 20, "g": 1 << 30}

# The house figure. Every SoundTouch measured so far -- ST10, ST20, WLA, across both
# module types -- has 128 MiB, and a model that really differs should say so in its own
# device tree along with how it was measured. Overstating is the dangerous direction:
# the upper addresses alias onto the lower ones and Linux destroys its own data.
DEFAULT_MEMORY = 128 << 20
# `--expect SIZE` takes two arguments.
EXPECT_ARGC = 2


def declared_sizes(text: str) -> tuple[int | None, int | None]:
    """Return (mem= bytes or None, memory node bytes or None)."""
    arg = None
    args = BOOTARGS.search(text)
    if args:
        found = MEM_ARG.search(args.group(1))
        if found:
            arg = int(found.group(1)) * SCALE[found.group(2).lower()]
    node = None
    reg = MEMORY_NODE.search(text)
    if reg:
        node = int(reg.group(2), 0)
    return arg, node


def parse_size(text: str) -> int:
    """Return the bytes in a size like 128M."""
    found = re.fullmatch(r"(\d+)([MmKkGg])", text.strip())
    if not found:
        msg = f"expected a size like 128M, got {text!r}"
        raise ValueError(msg)
    return int(found.group(1)) * SCALE[found.group(2).lower()]


def check(path: Path, expect: int | None = None) -> str | None:
    """Return why the tree's mem= and memory node disagree with each other or the expected size, or None."""
    if expect is None:
        expect = DEFAULT_MEMORY
    arg, node = declared_sizes(path.read_text())
    if arg is not None and node is not None and arg != node:
        return (f"{path}: bootargs says mem={arg // (1 << 20)}M but memory@ declares "
                f"{node // (1 << 20)}M; Linux trusts both and corrupts itself when they differ")
    for name, value in (("bootargs mem=", arg), ("memory@ node", node)):
        if value is not None and value != expect:
            return (f"{path}: {name} is {value // (1 << 20)}M, not the "
                    f"{expect // (1 << 20)}M every SoundTouch here has. If this board "
                    f"really differs, measure it -- get_ram_size() and the stock "
                    f"bootloader's mem= -- and change DEFAULT_MEMORY or this target's "
                    f"MEMORY deliberately")
    return None


def main(argv: list[str]) -> int:
    """Check every tree named, or every SoundTap tree, and return the exit code."""
    argv = argv[1:]
    expect = None
    if len(argv) >= EXPECT_ARGC and argv[0] == "--expect":
        expect = parse_size(argv[1])
        argv = argv[2:]
    paths = [Path(a) for a in argv]
    if not paths:
        root = Path(__file__).resolve().parents[2]
        paths = sorted((root / "os/bootloader").glob("am335x-soundtap-*.dts"))
    problems = [message for message in (check(p, expect) for p in paths) if message]
    for message in problems:
        print(message, file=sys.stderr)
    if problems:
        return 1
    print(f"device tree memory sizes agree with mem= in {len(paths)} trees")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
