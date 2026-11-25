# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every test file sits in a directory the test map's table lists."""
import re
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
MAP = ROOT / "docs/contributors/testing/README.md"
TEST_FILE = re.compile(r"(^|/)(test_[^/]+\.(py|sh|c)|[^/]+_test\.(go|c|sh)|[^/]+-test\.sh|[^/]+\.test\.tsx?)$")


class TestMapTest(unittest.TestCase):
    """The test map against the tracked test files."""

    def test_every_test_file_is_in_a_listed_directory(self) -> None:
        """Every test file is under a directory the table lists; the prose below it does not count."""
        rows = "".join(line for line in MAP.read_text().splitlines() if line.startswith("| "))
        listed = set(re.findall(r"`([\w./-]+/)`", rows))
        files = subprocess.run(["git", "ls-files"], cwd=ROOT, capture_output=True, text=True,
                               check=True).stdout.splitlines()
        unlisted = [f for f in files if TEST_FILE.search(f)
                    and not any(f.startswith(d) for d in listed)]
        self.assertEqual(unlisted, [], "add these to a row of docs/contributors/testing/README.md, "
                         "or move them into a listed directory")


if __name__ == "__main__":
    unittest.main()
