# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Run the static WLA NAND contract check of the Buildroot tree."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "support"))
import buildroot_wla_nand_check as check


class BuildrootWlaNandCheckTests(unittest.TestCase):
    """The WLA NAND check passes on this tree."""

    def test_static_contract(self) -> None:
        """The Buildroot tree meets the WLA NAND contract."""
        self.assertEqual(check.main(), 0)


if __name__ == "__main__":
    unittest.main()
