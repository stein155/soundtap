# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""bt.sh reports a streaming phone only while Bluetooth is the selected source, as stock does."""
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
BT = (ROOT / "os/overlay/opt/soundtap/bt.sh").read_text()
# The transition block of the poll loop: from listing the PCMs to the streaming edge.
BLOCK = re.search(r"^\t\tpcms=\$\(bluealsa-cli list-pcms.*?^\t\t\tstreaming=\$s\n.*?^\t\tfi\n", BT, re.S | re.M)


def passes(states, selected=True):
    """One poll pass per entry of `states`: None = no phone, False = connected and idle,
    True = streaming. Return the bt-source.sh calls made, in order."""
    with tempfile.TemporaryDirectory() as d:
        root = Path(d)
        (root / "bt-source.sh").write_text(f'#!/bin/sh\necho "$1" >>{root}/calls\n')
        (root / "bt-source.sh").chmod(0o755)
        (root / "bluealsa-cli").write_text(
            """#!/bin/sh
case "$1" in
list-pcms) [ "$STATE" = none ] || echo /org/bluealsa/hci0/dev_X/a2dpsnk/source ;;
info) [ "$STATE" = streaming ] && echo "Running: true" || echo "Running: false" ;;
esac
"""
        )
        (root / "bluealsa-cli").chmod(0o755)
        # The block posts the connect chime to 127.0.0.1:80. A host with something on :80
        # that hangs up made wget retry 20 times, 145 s per test on the build host.
        (root / "wget").write_text("#!/bin/sh\nexit 0\n")
        (root / "wget").chmod(0o755)
        block = BLOCK.group(0).replace("/opt/soundtap/bt-source.sh", f"{root}/bt-source.sh")
        env = {**os.environ, "PATH": f"{root}:{os.environ['PATH']}", "BT_SELECTED": str(root / "selected")}
        if selected:
            (root / "selected").touch()
        script = "prev=0 streaming=0 meta= btvol=\n"
        for st in states:
            name = "none" if st is None else "streaming" if st else "idle"
            script += f"STATE={name}\nexport STATE\n{block}"
        subprocess.run(["sh", "-ec", script], check=True, env=env, capture_output=True)
        calls = root / "calls"
        return calls.read_text().split() if calls.exists() else []


class SourceOnStream(unittest.TestCase):
    def test_block_found(self):
        self.assertIsNotNone(BLOCK)

    def test_a_phone_that_only_connects_does_not_claim_the_source(self):
        self.assertEqual(passes([None, False, False]), [])

    def test_streaming_claims_it_once(self):
        self.assertEqual(passes([False, True, True]), ["on"])

    def test_a_pause_keeps_the_source_and_a_resume_claims_again(self):
        self.assertEqual(passes([True, False, True]), ["on", "pause", "on"])

    def test_a_phone_that_stops_streaming_reports_a_pause_once(self):
        self.assertEqual(passes([True, False, False]), ["on", "pause"])

    def test_disconnect_pauses_it_and_the_phone_resumes_it(self):
        # The source waits for the phone instead of being released.
        self.assertEqual(passes([True, None, False, True]), ["on", "pause", "on"])

    def test_disconnect_of_an_idle_phone_pauses_too(self):
        self.assertEqual(passes([False, None]), ["pause"])

    def test_a_lost_phone_never_stops_the_source(self):
        self.assertNotIn("bt-source.sh off", BT)

    def test_a_phone_streaming_under_another_source_is_ignored(self):
        self.assertEqual(passes([False, True, True], selected=False), [])

    def test_connect_itself_never_posts_on(self):
        self.assertNotRegex(BT, r"\[ \"\$n\" = 1 \] && /opt/soundtap/bt-source\.sh on")


if __name__ == "__main__":
    unittest.main()
