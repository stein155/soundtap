# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""udhcpc keeps and reports its lease; bt.sh follows a rename on an event, not a poll."""

import os
import re
import unittest
from collections.abc import Iterator
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OVERLAY = ROOT / "os/overlay"
HOOK = OVERLAY / "usr/share/udhcpc/default.script.d/soundtap-log"


def code_lines(path: Path) -> Iterator[str]:
    """Yield the lines of path that are not comments, a Go call split over lines as one.

    A line that ends in an open argument list (a trailing comma or parenthesis) is joined
    with the lines that follow it, so a long exec.Command reads as one call.
    """
    pending = ""
    for line in path.read_text().splitlines():
        if line.lstrip().startswith(("#", "//")):
            continue
        pending = f"{pending} {line.strip()}" if pending else line
        if path.suffix == ".go" and pending.rstrip().endswith((",", "(")):
            continue
        yield pending
        pending = ""
    if pending:
        yield pending


class DhcpTest(unittest.TestCase):
    """How udhcpc is started and how its lease events are reported."""

    def test_every_udhcpc_stays_in_the_background_and_uses_the_default_script(self) -> None:
        """Every udhcpc call goes to the background and keeps Buildroot's default script.

        -q exits once bound, so the lease is never renewed; -s would bypass
        Buildroot's default.script, which sets address, route and DNS on renew.
        """
        sources = [p for p in OVERLAY.rglob("*") if p.is_file() and p.read_bytes().startswith(b"#!/bin/sh")]
        sources += [p for p in (ROOT / "app").rglob("*.go") if not p.name.endswith("_test.go")]
        calls = []
        for path in sources:
            calls.extend((path.relative_to(ROOT), line) for line in code_lines(path)
                         if re.search(r'exec\.Command(Context\(\w+, |\()"udhcpc",|(^|[;&|({])\s*udhcpc\s', line))
        self.assertGreaterEqual(len(calls), 3, calls)
        for path, line in calls:
            self.assertNotRegex(line, r'["\s]-[a-zA-Z]*q', f"{path}: {line}")
            self.assertNotRegex(line, r'["\s]-s["\s]', f"{path}: {line}")
            self.assertRegex(line, r'["\s]-b["\s)]', f"{path}: {line}")

    def test_busybox_keeps_the_buildroot_default_script(self) -> None:
        """BusyBox keeps udhcpc with Buildroot's default script path."""
        fragment = (ROOT / "os/board/rootfs/busybox.fragment").read_text()
        self.assertNotIn("CONFIG_UDHCPC_DEFAULT_SCRIPT", fragment)
        self.assertNotRegex(fragment, r"#\s*CONFIG_UDHCPC is not set")

    def test_lease_events_are_logged_by_a_default_script_hook(self) -> None:
        """An executable default.script hook logs every lease event and is installed and checked."""
        self.assertTrue(os.access(HOOK, os.X_OK), HOOK)
        hook = HOOK.read_text()
        self.assertTrue(hook.startswith("#!/bin/sh\n"))
        self.assertIn("[dhcp] ${1:-?} ${interface:-?} ${ip:-no address}", hook)
        self.assertIn("exit 0", hook)
        mk = (ROOT / "os/package/soundtap-runtime/soundtap-runtime.mk").read_text()
        self.assertIn("$(TARGET_DIR)/usr/share/udhcpc/default.script.d/soundtap-log", mk)
        target = (ROOT / "os/support/rootfs_check.py").read_text()
        self.assertIn('"usr/share/udhcpc/default.script"', target)
        self.assertIn('"usr/share/udhcpc/default.script.d/soundtap-log"', target)


if __name__ == "__main__":
    unittest.main()
