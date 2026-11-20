# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""bt.sh pushes the speaker's volume to the phone's bluealsa PCM, once, and never echoes it."""
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
BT = (ROOT / "os/overlay/opt/soundtap/bt.sh").read_text()
FUNC = re.search(r"^\tpush_speaker_volume\(\) \{\n.*?^\t\}\n", BT, re.S | re.M)


def push(speaker, btvol, wget_ok=True):
    """Run the function with `speaker` as GET /api/volume; return (bluealsa-cli calls, btvol after)."""
    with tempfile.TemporaryDirectory() as d:
        bin_dir = Path(d)
        body = f"echo '{{\"volume\":{speaker}}}'" if wget_ok else "exit 1"
        (bin_dir / "wget").write_text(f"#!/bin/sh\n{body}\n")
        (bin_dir / "bluealsa-cli").write_text(f'#!/bin/sh\necho "$*" >>{d}/calls\n')
        for f in ("wget", "bluealsa-cli"):
            (bin_dir / f).chmod(0o755)
        script = FUNC.group(0) + f'pcm=/pcm/x\nbtvol={btvol}\npush_speaker_volume\necho "$btvol"\n'
        env = {**os.environ, "PATH": f"{d}:{os.environ['PATH']}"}
        out = subprocess.run(["sh", "-ec", script], capture_output=True, text=True, check=True, env=env)
        calls = (bin_dir / "calls").read_text().splitlines() if (bin_dir / "calls").exists() else []
        return calls, out.stdout.strip()


class SpeakerVolumeToPhone(unittest.TestCase):
    def test_a_speaker_change_is_written_to_the_pcm_and_remembered(self):
        self.assertEqual(push(35, 30), (["volume /pcm/x 44"], "35"))

    def test_an_unchanged_volume_writes_nothing(self):
        self.assertEqual(push(30, 30), ([], "30"))

    def test_the_speaker_level_wins_the_first_sync_of_a_connection(self):
        self.assertEqual(push(35, ""), (["volume /pcm/x 44"], "35"))

    def test_a_phone_level_does_not_count_before_the_first_sync(self):
        self.assertIn('if [ -n "$btvol" ] && [ "$want" != "$btvol" ]; then', BT)

    def test_a_failed_read_writes_nothing(self):
        self.assertEqual(push(35, 30, wget_ok=False), ([], "30"))

    def test_what_is_written_reads_back_as_the_same_speaker_volume(self):
        # the loop's phone-to-speaker mapping, so the property change is not posted back
        for v in range(101):
            raw = (v * 127 + 50) // 100
            self.assertEqual((raw * 100 + 63) // 127, v, v)

    def test_the_poll_loop_calls_it_after_finding_the_pcm(self):
        self.assertLess(BT.index("pcm=$(bluealsa-cli list-pcms"), BT.index("\t\t\tpush_speaker_volume\n"))


if __name__ == "__main__":
    unittest.main()
