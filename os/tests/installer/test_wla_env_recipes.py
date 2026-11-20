#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Hold the installer's and soundtapd's copies of the U-Boot recipe templates to uboot_recipes.py.

The templates carry %s where the model differs -- the device tree, the kernel image, the
variant and the module type -- and both sides fill the same indices with the same arguments,
so one set of recipes serves binky, rhino and spotty. The filled result is checked against
this file by os/tests/installer/test_wla_installer_offline.py, which runs `installer --print-env`.

Several copies exist on purpose. The environment is written from four places: the Python tool,
on a board we can already reach; os/installer/module, which runs on the stock 3.14 kernel where
there is no Python at all; soundtapd; and U-Boot's USB-stick recovery. They must stay identical:
a recipe that differs between install routes is a board that boots differently depending on how
it was flashed.

Why a text check: the copies are literal arrays in four languages, and two of them (Go, the
U-Boot patch) cannot run here. Parsing the declared literals and comparing them byte for byte is
the one check that covers all four; the filled C result is also run, through `--print-env`.

The Python file is the copy to edit. Run this after changing it; it prints what to paste.
"""
import unittest
import importlib.util
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


def python_recipes():
    spec = importlib.util.spec_from_file_location(
        "wla_env", ROOT / "os" / "support" / "uboot_recipes.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return [entry.decode() for entry in module.TEMPLATES]


def c_recipes():
    source = (ROOT / "os/installer/module/installer.c").read_text()
    match = re.search(r"static const char \*const mainline_env_templates\[\] = \{(.*?)\n\};",
                      source, re.S)
    if not match:
        raise SystemExit("installer.c no longer declares mainline_env_templates[]")
    entries = []
    for literal in re.findall(r'((?:\s*"(?:[^"\\]|\\.)*")+)\s*,', match.group(1)):
        entries.append("".join(
            part.encode().decode("unicode_escape")
            for part in re.findall(r'"((?:[^"\\]|\\.)*)"', literal)))
    return entries


def uboot_recipes():
    """U-Boot's copy (rt_env_templates in uboot-2026.07-usb-recovery.patch), which the USB-stick
    recovery saves when the environment it finds lacks them."""
    patch = (ROOT / "os/bootloader/uboot/uboot-2026.07-usb-recovery.patch").read_text()
    match = re.search(r"^\+static const char \*const rt_env_templates\[\] = \{(.*?)^\+\};", patch, re.S | re.M)
    if not match:
        raise SystemExit("the USB recovery patch no longer declares rt_env_templates[]")
    body = "\n".join(line[1:] for line in match.group(1).split("\n"))
    entries = []
    for literal in re.findall(r'((?:\s*"(?:[^"\\]|\\.)*")+)\s*,', body):
        entries.append("".join(
            part.encode().decode("unicode_escape")
            for part in re.findall(r'"((?:[^"\\]|\\.)*)"', literal)))
    return entries


def go_recipes():
    """soundtapd's copy (internal/flash/recipes.go), which /api/firmware writes when it
    replaces the boot chain."""
    source = (ROOT / "app" / "internal" / "flash" / "recipes.go").read_text()
    match = re.search(r"var mainlineTemplates = \[\]string\{(.*?)\n\}", source, re.S)
    if not match:
        raise SystemExit("recipes.go no longer declares mainlineTemplates")
    entries = []
    for literal in re.findall(r'((?:\s*"(?:[^"\\]|\\.)*"\s*\+?)+)\s*,', match.group(1)):
        entries.append("".join(
            part.encode().decode("unicode_escape")
            for part in re.findall(r'"((?:[^"\\]|\\.)*)"', literal)))
    return entries


def main():
    wanted = python_recipes()
    status = 0
    for where, have in (("installer.c", c_recipes()), ("recipes.go", go_recipes()),
                        ("uboot-2026.07-usb-recovery.patch", uboot_recipes())):
        if wanted == have:
            print(f"{where} carries all {len(wanted)} recipe templates byte for byte")
            continue
        status = 1
        report(where, wanted, have)
    return status


def report(where, wanted, have):
    print(f"{where} and uboot_recipes.py disagree:", file=sys.stderr)
    for index in range(max(len(wanted), len(have))):
        a = wanted[index] if index < len(wanted) else "<missing>"
        b = have[index] if index < len(have) else "<missing>"
        if a != b:
            print(f"  [{index}] python: {a[:90]}", file=sys.stderr)
            print(f"       {where}: {b[:90]}", file=sys.stderr)


class Check(unittest.TestCase):
    def test_check(self):
        self.assertEqual(main(), 0)


if __name__ == "__main__":
    unittest.main()
