# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import tempfile
import unittest

from boot.init_script import InitScript


class SocMacTest(unittest.TestCase):
    """The init script's soc_mac prints stock's deviceID from the SoC's mac_id0 fuses."""

    def run_soc_mac(self, hi, lo):
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            init.stub("devmem", f'case $1 in 0x44E10634) echo {hi};; 0x44E10630) echo {lo};; esac')
            return init.run("soc_mac").stdout

    def test_matches_stock_device_ids(self):
        self.assertEqual(self.run_soc_mac("0xA1000002", "0x000030B2"), "020000A1B230")  # WLA
        self.assertEqual(self.run_soc_mac("0xC3000002", "0x000010D4"), "020000C3D410")  # ST10

    def test_blank_fuse_prints_nothing(self):
        self.assertEqual(self.run_soc_mac("0x00000000", "0x00000000"), "")


if __name__ == "__main__":
    unittest.main()
