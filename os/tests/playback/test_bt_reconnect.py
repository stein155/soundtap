# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Selecting Bluetooth makes bt-ctl.sh call the trusted phones, newest first, bounded."""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CTL = ROOT / "os/overlay/opt/soundtap/bt-ctl.sh"
MAC = "AA:BB:CC:DD:EE:%02X"


def reconnect(devices, connects_after=None, verb="reconnect", connected=""):
    """Run `bt-ctl.sh reconnect`. devices: [(n, mtime, trusted)]. Return the addresses connected to.

    The stub bluetoothctl reports a connected device once `connects_after` connect calls were made.
    """
    with tempfile.TemporaryDirectory() as d:
        root = Path(d)
        for n, mtime, trusted in devices:
            info = root / "state" / "00:11:22:33:44:55" / (MAC % n) / "info"
            info.parent.mkdir(parents=True)
            info.write_text("[General]\n" + ("Trusted=true\n" if trusted else "Trusted=false\n"))
            os.utime(info, (mtime, mtime))
        bin_dir = root / "bin"
        bin_dir.mkdir()
        calls = root / "calls"
        stub = f"""#!/bin/sh
case "$*" in
connect*) echo "$2" >>{calls} ;;
remove*) echo "rm-$2" >>{calls} ;;
"info "*) [ "$2" = "{connected}" ] && echo "	Connected: yes" ;;
"devices Connected")
	n=$(wc -l <{calls} 2>/dev/null || echo 0)
	[ -n "{connects_after}" ] && [ "$n" -ge "{connects_after}" ] && echo "Device AA:BB:CC:DD:EE:99 phone" ;;
esac
"""
        (bin_dir / "bluetoothctl").write_text(stub)
        (bin_dir / "bluetoothctl").chmod(0o755)
        env = {**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}", "BT_STATE": str(root / "state")}
        rc = subprocess.run(["sh", str(CTL), verb], env=env, capture_output=True, timeout=30).returncode
        got = calls.read_text().split() if calls.exists() else []
        # 3 only when phones were called and none came; everything else succeeds.
        came = connects_after is not None and len(got) >= connects_after
        want = 3 if verb == "reconnect" and got and not came else 0
        if rc != want:
            raise AssertionError(f"exit {rc}, want {want}")
        return got


class Reconnect(unittest.TestCase):
    def test_newest_trusted_first_and_untrusted_skipped(self):
        got = reconnect([(1, 100, True), (2, 300, True), (3, 200, False), (4, 200, True)])
        self.assertEqual(got, [MAC % 2, MAC % 4, MAC % 1])

    def test_stops_once_a_phone_is_connected(self):
        self.assertEqual(reconnect([(1, 100, True), (2, 300, True), (3, 200, True)], connects_after=1), [MAC % 2])

    def test_gives_up_after_five(self):
        self.assertEqual(len(reconnect([(n, n, True) for n in range(1, 9)])), 5)

    def test_nothing_paired_does_nothing(self):
        self.assertEqual(reconnect([]), [])


class Trim(unittest.TestCase):
    def test_forgets_the_oldest_beyond_eight(self):
        got = reconnect([(n, n, True) for n in range(1, 11)], verb="trim")
        self.assertEqual(got, ["rm-" + MAC % 2, "rm-" + MAC % 1])

    def test_keeps_a_connected_device(self):
        got = reconnect([(n, n, True) for n in range(1, 11)], verb="trim", connected=MAC % 1)
        self.assertEqual(got, ["rm-" + MAC % 2])

    def test_eight_or_fewer_untouched(self):
        self.assertEqual(reconnect([(n, n, True) for n in range(1, 9)], verb="trim"), [])


if __name__ == "__main__":
    unittest.main()
