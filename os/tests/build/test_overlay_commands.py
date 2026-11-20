# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The speaker image ships BusyBox, not the usual desktop tools: overlay scripts must not call them."""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OVERLAY = ROOT / "os/overlay"

# Not in the image. curl (Bluetooth volume never reached soundtapd).
ABSENT = ("curl",)


class OverlayCommandsTest(unittest.TestCase):
    def test_scripts_call_only_tools_the_image_has(self):
        found = []
        for path in sorted(OVERLAY.rglob("*")):
            if not path.is_file():
                continue
            try:
                text = path.read_text()
            except UnicodeDecodeError:
                continue
            if not text.startswith("#!/bin/sh"):
                continue
            for n, line in enumerate(text.splitlines(), 1):
                code = line.split("#", 1)[0]
                for tool in ABSENT:
                    if re.search(rf"(^|[\s;|&(`$]){tool}(\s|$)", code):
                        found.append(f"{path.relative_to(ROOT)}:{n}: {tool}")
        self.assertEqual(found, [])


if __name__ == "__main__":
    unittest.main()
