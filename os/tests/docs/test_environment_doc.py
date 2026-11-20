# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every environment variable soundtapd reads is listed in docs/contributors/environment.md."""
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[3]
READ = re.compile(r'(?:Getenv|LookupEnv|envDefault)\("([A-Z][A-Z0-9_]*)"')
EXPORT = re.compile(r"^\s*export\s+([A-Z][A-Z0-9_ ]*)$", re.M)
# Exported for the init script's other children, not for soundtapd.
NOT_FOR_SOUNDTAPD = {"PATH", "SNAPCLIENT_SAMPLEFORMAT"}


def read_by_go() -> set:
    names = set()
    for path in (ROOT / "app").rglob("*.go"):
        # Vendored modules read variables of their own (godbus's DBUS_*_BUS_ADDRESS) in
        # functions soundtapd does not call: it passes the bus address itself.
        if path.name.endswith("_test.go") or "vendor" in path.relative_to(ROOT / "app").parts[:1]:
            continue
        names |= set(READ.findall(path.read_text(errors="replace")))
    return names - {"PATH", "HOME"}


def exported_by_init() -> set:
    script = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
    return {name for m in EXPORT.findall(script) for name in m.split()} - NOT_FOR_SOUNDTAPD


def listed() -> set:
    doc = (ROOT / "docs/contributors/environment.md").read_text()
    return set(re.findall(r"^\| `([A-Z][A-Z0-9_]*)` \|", doc, re.M))


class EnvironmentDoc(unittest.TestCase):
    def test_every_variable_is_documented(self):
        self.assertEqual(sorted(read_by_go() - listed()), [], "add these to docs/contributors/environment.md")

    def test_init_exports_only_documented_names(self):
        # A misspelt export in the init script is silently ignored by soundtapd, which
        # then runs on its default; the doc is the list both sides must agree on.
        self.assertEqual(sorted(exported_by_init() - listed()), [], "typo, or add to docs/contributors/environment.md")

    def test_the_export_check_sees_the_script(self):
        self.assertIn("MODEL", exported_by_init())


if __name__ == "__main__":
    unittest.main()
