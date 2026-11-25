# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The init script's soc_mac reads the SoC's MAC fuses."""
import tempfile
import unittest

from boot.init_script import InitScript


class SocMacTest(unittest.TestCase):
    """The init script's soc_mac prints stock's deviceID from the SoC's mac_id0 fuses."""

    def run_soc_mac(self, hi: str, lo: str) -> str:
        """Run soc_mac with devmem answering hi and lo for the two fuse words; return its output."""
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            init.stub("devmem", f"case $1 in 0x44E10634) echo {hi};; 0x44E10630) echo {lo};; esac")
            return init.run("soc_mac").stdout

    def test_matches_stock_device_ids(self) -> None:
        """The printed ID matches stock's deviceID on a WLA and an ST10."""
        self.assertEqual(self.run_soc_mac("0xA1000002", "0x000030B2"), "020000A1B230")  # WLA
        self.assertEqual(self.run_soc_mac("0xC3000002", "0x000010D4"), "020000C3D410")  # ST10

    def test_blank_fuse_prints_nothing(self) -> None:
        """Blank fuses print nothing."""
        self.assertEqual(self.run_soc_mac("0x00000000", "0x00000000"), "")


if __name__ == "__main__":
    unittest.main()
