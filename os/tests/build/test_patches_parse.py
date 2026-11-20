# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every patch the image applies parses: hunk headers match their line counts.

Buildroot applies these with GNU patch, which only reports a corrupt hunk once the package
builds, after an hour of image build; a host test only compiled the code it cut out of the
patch, so a wrong count passed CI (the U-Boot board patch, 2026-11-20). git apply --numstat
reads the patch alone and needs no source tree.
"""
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


class PatchesParse(unittest.TestCase):
    def test_every_patch_parses(self):
        patches = sorted((ROOT / "os").rglob("*.patch"))
        self.assertTrue(patches, "no patches found under os/")
        bad = []
        for p in patches:
            r = subprocess.run(["git", "apply", "--numstat", str(p)], cwd=p.parent, capture_output=True, text=True, timeout=30)
            if r.returncode != 0:
                bad.append(f"{p.relative_to(ROOT)}: {r.stderr.strip()}")
        self.assertEqual(bad, [])


if __name__ == "__main__":
    unittest.main()
