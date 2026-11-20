# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""A speaker script may not hide a new error with 2>/dev/null unless a comment says why.

A `2>/dev/null` counts as explained when a comment ends the same line or the line above is
a comment. The rest are counted per file, and the count may only go down: an unexplained
one that is added fails this test, and one that is removed or explained lowers BASELINE.

A lint, so a text check: what it holds is how the scripts are written, not what they do.
"""
import subprocess
import unittest
from pathlib import Path

# "the line above is a comment" also accepts an unrelated comment; reviewing the
# remaining baseline lines one by one is the next step.

ROOT = Path(__file__).resolve().parents[3]
TREES = ("os/overlay", "os/board/rootfs/rootfs-overlay")
# Unexplained 2>/dev/null per file on 2026-11-07. Lower these; never raise them.
BASELINE = {
}


def unexplained(lines):
    count = 0
    for i, line in enumerate(lines):
        if "2>/dev/null" not in line or line.lstrip().startswith("#"):
            continue
        if "#" in line.split("2>/dev/null", 1)[1]:
            continue
        if i and lines[i - 1].lstrip().startswith("#"):
            continue
        count += 1
    return count


def scripts():
    names = subprocess.check_output(["git", "-C", str(ROOT), "ls-files", *TREES], text=True)
    for name in names.split():
        lines = (ROOT / name).read_text(errors="replace").splitlines()
        if name.endswith(".sh") or (lines and lines[0].startswith("#!") and "sh" in lines[0]):
            yield name, lines


class DevNullRatchet(unittest.TestCase):
    def test_no_new_unexplained_devnull(self):
        grown = {name: (n, BASELINE.get(name, 0)) for name, lines in scripts()
                 if (n := unexplained(lines)) > BASELINE.get(name, 0)}
        self.assertEqual(grown, {}, "explain each new 2>/dev/null in a comment, or let the error show")

    def test_counting(self):
        self.assertEqual(unexplained(["x 2>/dev/null", "y 2>/dev/null # absent on the WLA",
                                      "# may not exist yet", "z 2>/dev/null", "# 2>/dev/null"]), 1)


if __name__ == "__main__":
    unittest.main()
