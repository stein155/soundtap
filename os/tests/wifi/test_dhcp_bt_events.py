# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""udhcpc keeps and reports its lease; bt.sh follows a rename on an event, not a poll."""

import os
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OVERLAY = ROOT / "os/overlay"
HOOK = OVERLAY / "usr/share/udhcpc/default.script.d/soundtap-log"


def code_lines(path):
    for line in path.read_text().splitlines():
        if not line.lstrip().startswith(("#", "//")):
            yield line


class DhcpTest(unittest.TestCase):
    def test_every_udhcpc_stays_in_the_background_and_uses_the_default_script(self):
        sources = [p for p in OVERLAY.rglob("*") if p.is_file() and p.read_bytes().startswith(b"#!/bin/sh")]
        sources += [p for p in (ROOT / "app").rglob("*.go") if not p.name.endswith("_test.go")]
        calls = []
        for path in sources:
            for line in code_lines(path):
                if re.search(r'exec\.Command(Context\(\w+, |\()"udhcpc",|(^|[;&|({])\s*udhcpc\s', line):
                    calls.append((path.relative_to(ROOT), line))
        self.assertGreaterEqual(len(calls), 3, calls)
        for path, line in calls:
            # -q exits once bound, so the lease is never renewed; -s would bypass
            # Buildroot's default.script, which sets address, route and DNS on renew.
            self.assertNotRegex(line, r'["\s]-[a-zA-Z]*q', f"{path}: {line}")
            self.assertNotRegex(line, r'["\s]-s["\s]', f"{path}: {line}")
            self.assertRegex(line, r'["\s]-b["\s)]', f"{path}: {line}")

    def test_busybox_keeps_the_buildroot_default_script(self):
        fragment = (ROOT / "os/board/rootfs/busybox.fragment").read_text()
        self.assertNotIn("CONFIG_UDHCPC_DEFAULT_SCRIPT", fragment)
        self.assertNotRegex(fragment, r"#\s*CONFIG_UDHCPC is not set")

    def test_lease_events_are_logged_by_a_default_script_hook(self):
        self.assertTrue(os.access(HOOK, os.X_OK), HOOK)
        hook = HOOK.read_text()
        self.assertTrue(hook.startswith("#!/bin/sh\n"))
        self.assertIn('[dhcp] ${1:-?} ${interface:-?} ${ip:-no address}', hook)
        self.assertIn("exit 0", hook)
        mk = (ROOT / "os/package/soundtap-runtime/soundtap-runtime.mk").read_text()
        self.assertIn("$(TARGET_DIR)/usr/share/udhcpc/default.script.d/soundtap-log", mk)
        target = (ROOT / "os/support/rootfs_check.py").read_text()
        self.assertIn('"usr/share/udhcpc/default.script"', target)
        self.assertIn('"usr/share/udhcpc/default.script.d/soundtap-log"', target)


if __name__ == "__main__":
    unittest.main()
