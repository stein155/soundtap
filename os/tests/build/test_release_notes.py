#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "notes", Path(__file__).parents[3] / "tools/release/notes.py")
notes = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notes)


class ReleaseNotes(unittest.TestCase):
    def test_first_os_release(self):
        text = notes.render("os", "0.1.0", "SoundTap-Core-0.1.0.stu", "ab" * 32, "", [])
        self.assertIn("## SoundTap Core 0.1.0", text)
        self.assertIn("The first SoundTap Core release", text)
        self.assertIn("SoundTap-Core-0.1.0.stu", text)
        self.assertIn("ab" * 32, text)

    def test_an_alpha_says_it_is_one(self):
        for lane in ("os", "app"):
            alpha = notes.render(lane, "0.1.0-alpha.1", "a", "00", "", [], os_api="1")
            self.assertIn("**This is an alpha**", alpha)
            self.assertNotIn("alpha", notes.render(lane, "0.1.0", "a", "00", "", [], os_api="1"))

    def test_groups(self):
        subjects = ["Fix the preset start", "ci: cache ccache", "docs: README", "Add a sleep timer"]
        text = notes.render("app", "0.2.0", "a.bin", "00", "app-v0.1.0", subjects, os_api="2")
        self.assertIn("level 2", text)
        for head in ("**Changes**", "**Fixes**", "**Build and CI**", "**Documentation**"):
            self.assertIn(head, text)
        self.assertLess(text.index("**Changes**"), text.index("**Fixes**"))
        self.assertIn("Compared with app-v0.1.0.", text)

    def test_install_steps_name_the_signature(self):
        """A production speaker refuses an upload without its .sig."""
        for lane, asset in (("os", "SoundTap-Core-0.1.0.stu"), ("app", "SoundTap-app-0.1.0-armv7.bin")):
            text = notes.render(lane, "0.1.0", asset, "00", "", [], os_api="1")
            self.assertIn(f"**{asset}.sig**", text)
            self.assertIn("both files together", text)

    def test_the_preview_package_has_its_section_and_checksum(self):
        """ADR 0133: the preview package is an asset of its own, with its own signature."""
        preview = ("SoundTap-Core-0.1.0-preview.stu", "cd" * 32)
        text = notes.render("os", "0.1.0", "SoundTap-Core-0.1.0.stu", "ab" * 32, "", [],
                            preview=preview)
        self.assertIn("### Preview package", text)
        self.assertIn("**SoundTap-Core-0.1.0-preview.stu.sig**", text)
        self.assertIn(f"`{'cd' * 32}  SoundTap-Core-0.1.0-preview.stu`", text)
        self.assertNotIn("Preview package", notes.render("os", "0.1.0", "a", "00", "", []))


if __name__ == "__main__":
    unittest.main()
