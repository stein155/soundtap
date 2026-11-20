# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Guard the 2026-09-13 WLA handshake facts against silent drift.

The board rounds that produced these cost hardware time, so the values live in a fixture and
this test pins the ones the installer design depends on. It reads files only.
"""
import unittest
from pathlib import Path

FIXTURE = Path(__file__).resolve().parents[1] / "fixtures" / "wla-stock-17008-handshake-20260913.txt"


def facts():
    out = {}
    for line in FIXTURE.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, _, value = line.partition("=")
        out[key] = value
    return out


class TestHandshakeFacts(unittest.TestCase):
    def test_installer_speaks_first(self):
        f = facts()
        self.assertEqual(f["installer_speaks_first"], "SoftwareUpdateStatus")
        self.assertEqual(f["stock_first_reply"], "SoftwareUpdateStart")
        self.assertEqual(f["silent_installer_failure_code"], "bad_url")

    def test_pull_framing(self):
        f = facts()
        self.assertEqual(f["data_request_first_sequence"], "2")
        self.assertEqual(f["data_request_bytes"], "16384")
        self.assertEqual(f["data_response_sequence_mirrors_request"], "true")

    def test_stock_declares_no_crc(self):
        # The installer, not stock, is responsible for validating package content.
        self.assertEqual(facts()["start_update_crc"], "0")

    def test_toolchain_boundary(self):
        f = facts()
        self.assertEqual(f["go_static_arm_module_runs_under_stock_kernel"], "false")
        self.assertEqual(f["c_static_arm_module_runs_under_stock_kernel"], "true")


if __name__ == "__main__":
    unittest.main()
