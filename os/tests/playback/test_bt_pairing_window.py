# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The pairing window lives in soundtapd (internal/bluez); BlueZ must not run a
window of its own, and bt.sh must not grow an agent again."""
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
BT = (ROOT / "os/overlay/opt/soundtap/bt.sh").read_text()
CONF = (ROOT / "os/overlay/etc/bluetooth/main.conf").read_text()


class PairingWindow(unittest.TestCase):
    def test_bluez_timeouts_stay_off(self):
        # BlueZ's own DiscoverableTimeout leaves the controller answering inquiries while
        # it reports Discoverable: no; the agent closes the window with a real write.
        self.assertIn("DiscoverableTimeout = 0", CONF)
        self.assertIn("PairableTimeout = 0", CONF)

    def test_bt_sh_runs_no_agent(self):
        code = "\n".join(line for line in BT.splitlines() if not line.lstrip().startswith("#"))
        for word in ("--agent", "default-agent", "Discoverable", "Pairable", "system-alias"):
            self.assertNotIn(word, code)


if __name__ == "__main__":
    unittest.main()
