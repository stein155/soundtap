# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "support"))
import check_wla_uboot_nand as check

RECIPE = b"fdt addr ${loadaddr}; " + check.WAY_BACK[0] + b"bootm ${kloadaddr} - ${loadaddr}"


class BoseBootPath(unittest.TestCase):
    """nboot.e may live only in the USB stick's way back to Bose (ADR 0130)."""

    def test_the_way_back_recipes_are_allowed(self):
        image = b"\0" + RECIPE + b"\0" + check.WAY_BACK[1] + b"\0"
        self.assertEqual(check.bose_boot_path(image, 'CONFIG_BOOTCOMMAND="true"'), "")

    def test_any_other_nboot_e_is_refused(self):
        image = RECIPE + b"\0nand_boot=nboot.e 0x80007fc0 0 0x280000\0"
        self.assertEqual(check.bose_boot_path(image, ""), "nboot.e")

    def test_a_default_bootcmd_that_runs_bose_recipes_is_refused(self):
        self.assertEqual(check.bose_boot_path(b"bootcmd=run nand_boot;\0", ""), "bootcmd=run ")
        self.assertEqual(check.bose_boot_path(RECIPE, 'CONFIG_BOOTCOMMAND="nboot.e 0"'),
                         "nboot.e in CONFIG_BOOTCOMMAND")


if __name__ == "__main__":
    unittest.main()
