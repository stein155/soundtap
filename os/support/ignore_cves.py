#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every CVE os/ignore-cves.mk ignores carries a reason, and the reason holds where it can be
checked offline.

    ignore_cves.py [--kernel-config output/os/images/kernel.config]

One CVE per `<PKG>_IGNORE_CVES +=` line, ending in `# <reason>` in one of the forms the
file's header lists. What is checked:

- fixed in 6.12.N: N is at or below the kernel soundtap_defconfig pins, and that is 6.12;
- bug came in X.Y: X.Y is after the pinned kernel's 6.12;
- CONFIG_X off: linux.fragment does not turn X on (=y or =m); with --kernel-config, the
  built configuration has it off or does not have it at all;
- CONFIG_X=m: linux.fragment does not build X in; with --kernel-config, X is =m or off;
- fixed by our patch: a patch of ours for that package names the CVE;
- not built for Linux: only for a package other than the kernel, which names a CONFIG_ symbol.

"fixed in/before V" for a package other than the kernel, "not built for Linux" (the file and
the build condition it names) and "maintainer accepted" are records, not checked further.
Without --kernel-config a symbol only the omap2plus defconfig or a Kconfig dependency turns
off is taken on trust; `make image-check` passes the real one.
"""

import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
ENTRY = re.compile(r"^(\w+)_IGNORE_CVES\s*\+=\s*(.*?)\s*$")
CVE = re.compile(r"CVE-\d{4}-\d{4,}")
FIX_612 = re.compile(r"\bfixed in 6\.12\.(\d+)\b")
CAME_IN = re.compile(r"\bbug came in (\d+)\.(\d+)\b")
OFF = re.compile(r"\b(CONFIG_\w+) off\b")
MODULE = re.compile(r"\b(CONFIG_\w+)=m\b")
PATCH = re.compile(r"\bfixed by our patch\b")
NOT_BUILT = re.compile(r"\bnot built for Linux\b")
OTHER = re.compile(r"\bfixed (?:in|before) \d[\w.]*|\bmaintainer accepted\b")


def kernel_version(defconfig: str) -> tuple[int, ...]:
    m = re.search(r'^BR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE="([\d.]+)"', defconfig, re.M)
    return tuple(int(x) for x in m[1].split(".")) if m else ()


def config_values(text: str) -> dict[str, str]:
    values = {}
    for line in text.splitlines():
        if m := re.match(r"(CONFIG_\w+)=(.*)", line):
            values[m[1]] = m[2]
        elif m := re.match(r"# (CONFIG_\w+) is not set", line):
            values[m[1]] = "n"
    return values


def patches_naming(root: Path, package: str, cve: str) -> list[Path]:
    name = package.lower().replace("_", "-")
    dirs = [root / "os/package" / name, root / "os/board/common/patches" / name]
    return [p for d in dirs if d.is_dir() for p in sorted(d.glob("*.patch")) if cve in p.read_text(errors="replace")]


class Context:
    """What a reason is checked against."""

    def __init__(self, root: Path, kernel_config: str | None):
        self.root = root
        self.version = kernel_version((root / "os/configs/soundtap_defconfig").read_text())
        self.fragment = config_values((root / "os/board/image/linux.fragment").read_text())
        self.built = config_values(kernel_config) if kernel_config is not None else None

    def pinned(self) -> str:
        return ".".join(map(str, self.version)) or "not pinned"


def kernel_reasons(ctx: Context, package: str, cve: str, reason: str) -> tuple[int, list[str]]:
    """The kernel forms in reason: how many, and what does not hold."""
    forms, out = 0, []
    for fix in FIX_612.findall(reason):
        forms += 1
        if package != "LINUX":
            out.append(f"{cve}: 'fixed in 6.12.N' is for the kernel")
        elif ctx.version[:2] == (6, 12) and int(fix) > ctx.version[2]:
            out.append(f"{cve} is fixed in 6.12.{fix}, after the pinned {ctx.pinned()}")
    for major, minor in CAME_IN.findall(reason):
        forms += 1
        if (int(major), int(minor)) <= ctx.version[:2]:
            out.append(f"{cve} came in {major}.{minor}, which the pinned kernel includes")
    for symbol in OFF.findall(reason):
        forms += 1
        if ctx.fragment.get(symbol, "n") in ("y", "m"):
            out.append(f"{cve}: linux.fragment sets {symbol}={ctx.fragment[symbol]}")
        if ctx.built is not None and ctx.built.get(symbol, "n") in ("y", "m"):
            out.append(f"{cve}: the built kernel has {symbol}={ctx.built[symbol]}")
    for symbol in MODULE.findall(reason):
        forms += 1
        if ctx.fragment.get(symbol) == "y":
            out.append(f"{cve}: linux.fragment builds {symbol} in")
        if ctx.built is not None and ctx.built.get(symbol, "n") == "y":
            out.append(f"{cve}: the built kernel has {symbol}=y")
    return forms, out


def entry_problems(ctx: Context, package: str, cve: str, reason: str) -> list[str]:
    if not reason:
        return [f"{cve} has no reason"]
    forms, out = kernel_reasons(ctx, package, cve, reason)
    if PATCH.search(reason):
        forms += 1
        if not patches_naming(ctx.root, package, cve):
            out.append(f"{cve}: no patch of ours for {package.lower()} names it")
    if NOT_BUILT.search(reason):
        forms += 1
        if package == "LINUX":
            out.append(f"{cve}: code the kernel does not build names its symbol, 'CONFIG_X off'")
    forms += len(OTHER.findall(reason))
    if forms == 0:
        out.append(f"{cve}: the reason {reason!r} is none of the forms the header lists")
    return out


def problems(root: Path, kernel_config: str | None = None) -> list[str]:
    ctx = Context(root, kernel_config)
    mk = (root / "os/ignore-cves.mk").read_text()
    out, seen = [], set()
    if ctx.version[:2] != (6, 12):
        out.append(f"the kernel is {ctx.pinned()}, not 6.12: every LINUX entry needs checking again")
    header = re.search(r"^# Linux ([\d.]+) ", mk, re.M)
    if header and tuple(int(x) for x in header[1].split(".")) != ctx.version:
        out.append(f"the header names Linux {header[1]}, the defconfig pins {ctx.pinned()}")
    for n, line in enumerate(mk.splitlines(), 1):
        if not (m := ENTRY.match(line)):
            continue
        value, _, reason = m[2].partition("#")
        cves = value.split()
        where = f"os/ignore-cves.mk:{n}"
        if len(cves) != 1 or not CVE.fullmatch(cves[0]):
            out.append(f"{where}: one CVE per line, not {value.strip()!r}")
            continue
        if cves[0] in seen:
            out.append(f"{where}: {cves[0]} is listed twice")
        seen.add(cves[0])
        out.extend(f"{where}: {p}" for p in entry_problems(ctx, m[1], cves[0], reason.strip()))
    return out


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--kernel-config", type=Path, help="the built kernel's .config")
    parser.add_argument("--root", type=Path, default=ROOT)
    args = parser.parse_args(argv[1:])
    config = args.kernel_config.read_text() if args.kernel_config else None
    found = problems(args.root, config)
    for p in found:
        print(p, file=sys.stderr)
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
