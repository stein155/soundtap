# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Small supply-chain invariants.

Every alpine image the repo pulls is pinned by digest, so a moved tag cannot change what the
recovery tools run. Numbered patches in one directory never share a number: Buildroot applies
them in name order, and a duplicate makes that order depend on the rest of the file name.
"""

import fnmatch
import os
import re
import unittest
from collections import Counter
from collections.abc import Iterator
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
# Untracked build, download and upstream trees (.gitignore, CI's buildroot checkout); the test container has no git.
# A directory with its own .git is another checkout (a worktree or clone kept inside this one).
SKIP = {ROOT / d for d in (".git", "buildroot", ".cache", "dl", "output", "artifacts", "dist", "os/rootfs", "os/bootloader/build")}


def files(*patterns: str) -> Iterator[Path]:
    """Yield the repository files whose name matches one of patterns, skipping untracked trees."""
    for top, dirs, names in os.walk(ROOT):
        dirs[:] = [d for d in dirs if d != "node_modules" and Path(top, d) not in SKIP
                   and not Path(top, d, ".git").exists()]
        for name in names:
            if any(fnmatch.fnmatch(name, p) for p in patterns):
                yield Path(top, name)


class SupplyChainPins(unittest.TestCase):
    """Images and patches cannot change behind the repository's back."""

    def test_alpine_images_are_digest_pinned(self) -> None:
        """Every alpine image reference carries an @sha256 digest."""
        for path in files("*.sh", "*.yml", "*.yaml", "*Dockerfile*"):
            for n, line in enumerate(path.read_text(errors="replace").splitlines(), 1):
                for ref in re.findall(r"\balpine:[\w.-]+(?:@sha256:[0-9a-f]{64})?", line):
                    self.assertIn("@sha256:", ref, f"{path.relative_to(ROOT)}:{n}: {ref}")

    def test_patch_numbers_are_unique_per_directory(self) -> None:
        """No two patches in one directory share a number."""
        numbers = Counter()
        for path in files("*.patch"):
            if m := re.match(r"(\d{4})-", path.name):
                numbers[(str(path.parent.relative_to(ROOT)), m[1])] += 1
        self.assertEqual([k for k, c in numbers.items() if c > 1], [])


if __name__ == "__main__":
    unittest.main()
