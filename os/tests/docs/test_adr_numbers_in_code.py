# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every ADR number a tracked file cites names an ADR that exists.

A deleted ADR keeps its number and the others are not renumbered (docs/decisions/README.md,
principle 5), so a citation of a deleted one points at nothing: "ADR" and a number, or a
docs/decisions/ path with one, in a comment or a page, with no ADR file of that number.
History does not count: CHANGELOG.md names what a release did at the time.
"""
import re
import subprocess
import unittest
from collections.abc import Iterator
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CITE = re.compile(r"(?:\bADR[ -]?|decisions/)(\d{4})\b")
SKIP = ("app/vendor/", "CHANGELOG.md")


def cited(text: str) -> Iterator[tuple[int, str]]:
    """Yield (line number, ADR number) for every citation in text."""
    for n, line in enumerate(text.splitlines(), 1):
        for m in CITE.finditer(line):
            yield n, m.group(1)


class AdrNumbersInCodeTest(unittest.TestCase):
    """ADR citations in tracked files against the ADRs that exist."""

    def test_every_cited_adr_exists(self) -> None:
        """Every ADR a tracked file cites exists."""
        have = {p.name[:4] for p in (ROOT / "docs/decisions").glob("[0-9][0-9][0-9][0-9]-*.md")}
        files = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, capture_output=True,
                               check=True).stdout.decode().split("\0")
        missing = []
        for name in files:
            if not name or name.startswith(SKIP):
                continue
            try:
                text = (ROOT / name).read_text(encoding="utf-8")
            except (UnicodeDecodeError, IsADirectoryError, FileNotFoundError):
                continue
            missing += [f"{name}:{n}: ADR {adr}" for n, adr in cited(text) if adr not in have]
        self.assertEqual(missing, [], "citations of ADRs that do not exist")

    def test_the_pattern(self) -> None:
        """The pattern finds ADR and decisions/ citations and nothing else."""
        self.assertEqual([a for _, a in cited("see ADR 0010, ADR-0048 and decisions/0130-usb.md")],
                         ["0010", "0048", "0130"])
        self.assertEqual(list(cited("ADR 10, VADR 0010x, 20100")), [])


if __name__ == "__main__":
    unittest.main()
