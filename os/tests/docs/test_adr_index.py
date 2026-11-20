# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The ADR index lists every decision under the title its own file gives it."""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
ADRS = ROOT / "docs/decisions"
ROW = re.compile(r"^\| \[(\d{4})\]\((\d{4}-[^)]+\.md)\) \| (.+?) \| [^|]+ \|\s*$")
H1 = re.compile(r"^# (\d{4})\. (.+?)\s*$")


def index_errors(index, titles):
    """index is the README's text; titles maps a file name to its H1 line."""
    errors, listed = [], set()
    for line in index.splitlines():
        m = ROW.match(line)
        if not m:
            continue
        number, name, title = m.groups()
        listed.add(name)
        h1 = H1.match(titles.get(name, ""))
        if not h1:
            errors.append(f"{number}: {name} has no '# {number}. Title' heading")
        elif h1.group(1) != number or not name.startswith(number):
            errors.append(f"{number}: {name} is numbered {h1.group(1)}")
        elif h1.group(2) != title:
            errors.append(f"{number}: index says {title!r}, the file {h1.group(2)!r}")
    errors += [f"{name} is not in the index" for name in sorted(set(titles) - listed)]
    return errors


class AdrIndexTest(unittest.TestCase):
    def test_index_titles_are_the_files_titles(self):
        titles = {p.name: p.read_text().splitlines()[0] for p in ADRS.glob("[0-9][0-9][0-9][0-9]-*.md")}
        self.assertEqual(index_errors((ADRS / "README.md").read_text(), titles), [])

    def test_mismatch_and_missing_entries_are_reported(self):
        index = "| [0001](0001-a.md) | One daemon | accepted |\n| [0002](0002-b.md) | Old | accepted |\n"
        titles = {"0001-a.md": "# 0001. One daemon", "0002-b.md": "# 0002. New",
                  "0003-c.md": "# 0003. Unlisted"}
        self.assertEqual(index_errors(index, titles),
                         ["0002: index says 'Old', the file 'New'", "0003-c.md is not in the index"])


if __name__ == "__main__":
    unittest.main()
