# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Each per-model heading of the hardware checklist's open list states how many rows are open."""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
HEADING = re.compile(r"^### (.+) \((\d+) open\)\s*$")


def count_errors(text: str) -> list[str]:
    """Return each heading whose open count differs from its open rows.

    A row is open when its status cell is `open`, alone or followed by a colon and a note.
    """
    errors, section = [], None

    def close() -> None:
        if section and section[1] != section[2]:
            errors.append(f"{section[0]}: heading says {section[1]} open, {section[2]} rows are")

    for line in text.splitlines():
        if line.startswith("#"):
            close()
            m = HEADING.match(line)
            section = [m.group(1), int(m.group(2)), 0] if m else None
            continue
        if section and line.startswith("|"):
            status = line.strip().strip("|").split("|")[-1].strip()
            if status == "open" or status.startswith("open:"):
                section[2] += 1
    close()
    return errors


class HardwareOpenCountsTest(unittest.TestCase):
    """The open counts in the hardware checklist's headings."""

    def test_headings_match_open_rows(self) -> None:
        """Each model's heading states the number of its open rows."""
        text = (ROOT / "docs/hardware/hardware-test.md").read_text()
        models = [m.group(1) for m in map(HEADING.match, text.splitlines()) if m]
        self.assertEqual(models, ["ST10", "ST20", "WLA", "ST300"])
        self.assertEqual(count_errors(text), [])

    def test_counts_open_rows_only(self) -> None:
        """Only rows whose status is open count, and a wrong count is reported."""
        text = """### ST10 (2 open)

| Prove on the speaker | Status |
|---|---|
| a | open |
| b | open: half passed 2026-11-15 |
| c | historical pass: 2026-11-15 |
| open in the name | 0.1.0 sha256:1a2b3c4d 2026-11-15 |

### WLA (2 open)

| Prove on the speaker | Status |
|---|---|
| a | open |
"""
        self.assertEqual(count_errors(text), ["WLA: heading says 2 open, 1 rows are"])


if __name__ == "__main__":
    unittest.main()
