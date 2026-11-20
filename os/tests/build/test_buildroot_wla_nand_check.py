# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "support"))
import buildroot_wla_nand_check as check


class BuildrootWlaNandCheckTests(unittest.TestCase):
    def test_static_contract(self):
        self.assertEqual(check.main(), 0)


if __name__ == "__main__":
    unittest.main()
