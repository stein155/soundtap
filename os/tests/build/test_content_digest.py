# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import importlib.util
import os
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).parents[2] / "support" / "content_digest.py"
SPEC = importlib.util.spec_from_file_location("content_digest", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ContentDigestTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name) / "source"
        self.root.mkdir()
        (self.root / "a.txt").write_text("alpha")

    def tearDown(self):
        self.tempdir.cleanup()

    def digest(self, *exclusions):
        return MODULE.content_digest([self.root], exclusions)

    def test_digest_ignores_location_and_mtime(self):
        other = Path(self.tempdir.name) / "other"
        other.mkdir()
        (other / "a.txt").write_text("alpha")
        os.utime(other / "a.txt", (1, 1))
        self.assertEqual(self.digest(), MODULE.content_digest([other]))

    def test_content_addition_deletion_and_mode_change_invalidate(self):
        original = self.digest()
        (self.root / "a.txt").write_text("beta")
        changed = self.digest()
        self.assertNotEqual(original, changed)

        added = self.root / "new.txt"
        added.write_text("new")
        with_addition = self.digest()
        self.assertNotEqual(changed, with_addition)
        added.unlink()
        self.assertEqual(changed, self.digest())

        (self.root / "a.txt").chmod(0o755)
        self.assertNotEqual(changed, self.digest())

    def test_symlink_target_invalidate(self):
        link = self.root / "link"
        link.symlink_to("a.txt")
        original = self.digest()
        link.unlink()
        link.symlink_to("missing.txt")
        self.assertNotEqual(original, self.digest())

    def test_excluded_subtree_does_not_invalidate(self):
        generated = self.root / "generated"
        generated.mkdir()
        (generated / "asset.js").write_text("one")
        original = self.digest("generated")
        (generated / "asset.js").write_text("two")
        (generated / "new.js").write_text("new")
        self.assertEqual(original, self.digest("generated"))

    def test_stamp_updates_on_change_and_reversion(self):
        stamp = Path(self.tempdir.name) / "digest.stamp"
        original = self.digest()
        MODULE.update_stamp(stamp, original)
        os.utime(stamp, (1, 1))
        MODULE.update_stamp(stamp, original)
        self.assertEqual(stamp.stat().st_mtime_ns, 1_000_000_000)

        (self.root / "a.txt").write_text("beta")
        MODULE.update_stamp(stamp, self.digest())
        changed_mtime = stamp.stat().st_mtime_ns
        self.assertGreater(changed_mtime, 1_000_000_000)

        (self.root / "a.txt").write_text("alpha")
        MODULE.update_stamp(stamp, self.digest())
        self.assertEqual(stamp.read_text(), original + "\n")
        self.assertGreaterEqual(stamp.stat().st_mtime_ns, changed_mtime)

    def test_web_and_go_trees_have_independent_digests(self):
        frontend = self.root / "frontend"
        frontend.mkdir()
        (frontend / "app.tsx").write_text("app")
        generated = self.root / "internal" / "web" / "dist"
        generated.mkdir(parents=True)
        (generated / "index.html").write_text("generated")
        go_file = self.root / "main.go"
        go_file.write_text("package main")

        web_digest = MODULE.content_digest([frontend])
        go_digest = self.digest("frontend", "internal/web/dist")
        go_file.write_text("package main\n")
        self.assertEqual(web_digest, MODULE.content_digest([frontend]))
        self.assertNotEqual(go_digest, self.digest("frontend", "internal/web/dist"))


if __name__ == "__main__":
    unittest.main()
