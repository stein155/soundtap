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

The Python file is the copy to edit. Run this after changing it; it reports each copy that differs.
"""
import importlib.util
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


def python_recipes() -> list[str]:
    """Return the templates as uboot_recipes.py declares them: the copy to edit."""
    spec = importlib.util.spec_from_file_location(
        "wla_env", ROOT / "os" / "support" / "uboot_recipes.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return [entry.decode() for entry in module.TEMPLATES]


def c_recipes() -> list[str]:
    """Return the installer's copy (mainline_env_templates in os/installer/module/installer.c)."""
    source = (ROOT / "os/installer/module/installer.c").read_text()
    match = re.search(r"static const char \*const mainline_env_templates\[\] = \{(.*?)\n\};",
                      source, re.DOTALL)
    if not match:
        msg = "installer.c no longer declares mainline_env_templates[]"
        raise SystemExit(msg)
    return [
        "".join(part.encode().decode("unicode_escape")
                for part in re.findall(r'"((?:[^"\\]|\\.)*)"', literal))
        for literal in re.findall(r'((?:\s*"(?:[^"\\]|\\.)*")+)\s*,', match.group(1))
    ]


def uboot_recipes() -> list[str]:
    """Return U-Boot's copy (rt_env_templates in uboot-2026.07-usb-recovery.patch).

    The USB-stick recovery saves it when the environment it finds lacks the templates.
    """
    patch = (ROOT / "os/bootloader/uboot/uboot-2026.07-usb-recovery.patch").read_text()
    match = re.search(r"^\+static const char \*const rt_env_templates\[\] = \{(.*?)^\+\};", patch, re.DOTALL | re.MULTILINE)
    if not match:
        msg = "the USB recovery patch no longer declares rt_env_templates[]"
        raise SystemExit(msg)
    body = "\n".join(line[1:] for line in match.group(1).split("\n"))
    return [
        "".join(part.encode().decode("unicode_escape")
                for part in re.findall(r'"((?:[^"\\]|\\.)*)"', literal))
        for literal in re.findall(r'((?:\s*"(?:[^"\\]|\\.)*")+)\s*,', body)
    ]


def go_recipes() -> list[str]:
    """Return soundtapd's copy (internal/flash/recipes.go).

    /api/firmware writes it when it replaces the boot chain.
    """
    source = (ROOT / "app" / "internal" / "flash" / "recipes.go").read_text()
    match = re.search(r"func mainlineTemplates\(\) \[\]string \{\n\treturn \[\]string\{(.*?)\n\t\}", source, re.DOTALL)
    if not match:
        msg = "recipes.go no longer declares mainlineTemplates"
        raise SystemExit(msg)
    return [
        "".join(part.encode().decode("unicode_escape")
                for part in re.findall(r'"((?:[^"\\]|\\.)*)"', literal))
        for literal in re.findall(r'((?:\s*"(?:[^"\\]|\\.)*"\s*\+?)+)\s*,', match.group(1))
    ]


def report(where: str, wanted: list[str], have: list[str]) -> str:
    """Return the templates on which ``where`` and uboot_recipes.py disagree, one pair per index."""
    lines = [f"{where} and uboot_recipes.py disagree:"]
    for index in range(max(len(wanted), len(have))):
        a = wanted[index] if index < len(wanted) else "<missing>"
        b = have[index] if index < len(have) else "<missing>"
        if a != b:
            lines.append(f"  [{index}] python: {a[:90]}")
            lines.append(f"       {where}: {b[:90]}")
    return "\n".join(lines)


class Check(unittest.TestCase):
    """Every copy of the U-Boot recipe templates equals uboot_recipes.py."""

    def test_check(self) -> None:
        """installer.c, recipes.go and the USB recovery patch carry every template byte for byte."""
        wanted = python_recipes()
        for where, have in (("installer.c", c_recipes()), ("recipes.go", go_recipes()),
                            ("uboot-2026.07-usb-recovery.patch", uboot_recipes())):
            with self.subTest(where=where):
                self.assertTrue(wanted == have, report(where, wanted, have))


if __name__ == "__main__":
    unittest.main()
