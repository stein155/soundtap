# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The README and the compatibility matrix call every model the release checklist covers Supported.

Supported is a model's place in the release (owner decision 2026-11-20), not a count of
checklist passes: a release is published only after each of these models has passed its
column of the checklist (RELEASING.md), so an open row blocks a release, not the model. Open
rows stay in docs/hardware/hardware-test.md.
"""
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[3]
README_ROWS = {"ST10": "SoundTouch 10", "ST20": "SoundTouch 20 Series I",
               "WLA": "SoundTouch Wireless Link adapter", "ST300": "SoundTouch 300"}


def cells(line: str) -> list[str]:
    """Return the stripped cells of a Markdown table row."""
    return [c.strip() for c in line.strip().strip("|").split("|")]


def release_models() -> list[str]:
    """Return the models the release checklist has a column for."""
    lines = (ROOT / "docs/hardware/hardware-test.md").read_text().splitlines()
    return cells(next(row for row in lines if row.startswith("| Path | ")))[1:]


class ModelStatus(unittest.TestCase):
    """Every release model is called Supported."""

    def test_the_checklist_covers_the_four_release_models(self) -> None:
        """The release checklist covers the four models."""
        self.assertEqual(sorted(release_models()), sorted(README_ROWS))

    def test_readme(self) -> None:
        """The README calls every release model Supported and none In test."""
        readme = (ROOT / "README.md").read_text()
        for model in release_models():
            row = re.search(r"^\| \**" + re.escape(README_ROWS[model]) + r"\** \| ([^|]+)\|", readme, re.MULTILINE)
            self.assertIsNotNone(row, model)
            self.assertIn("Supported", row.group(1), model)
        self.assertNotIn("In test", readme)

    def test_compatibility_matrix(self) -> None:
        """The compatibility matrix's overall status is Supported for all four models."""
        text = (ROOT / "docs/owners/compatibility.md").read_text()
        line = next(row for row in text.splitlines() if row.startswith("| Overall status"))
        self.assertEqual(cells(line)[1:], ["Supported"] * 4)


if __name__ == "__main__":
    unittest.main()
