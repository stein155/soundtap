# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""`make stu` asks for all three models: a missing ST20 kernel stops it."""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "os/installer/build-universal-stu.sh"


class UniversalTarget(unittest.TestCase):
    def test_tri_target_refuses_a_missing_st20_kernel(self):
        env = dict(os.environ, SOUNDTAP_STU_TARGET="soundtap-tri",
                   SOUNDTAP_SPOTTY_KERNEL="/nonexistent/uImage-st20")
        done = subprocess.run(["sh", str(SCRIPT)], env=env, capture_output=True, text=True,
                              check=False)
        self.assertEqual(done.returncode, 2, done.stderr)
        self.assertIn("refusing a two-model package", done.stderr)

    def test_make_stu_asks_for_tri(self):
        """`make stu` runs the builder for all three models, with each model's boot payloads."""
        with tempfile.TemporaryDirectory() as tmp:
            seen = Path(tmp) / "seen"
            stub = Path(tmp) / "sh"
            stub.write_text(f'#!/bin/sh\n{{ echo "$*"; env; }} >"{seen}"\n')
            stub.chmod(0o755)
            env = dict(os.environ, PATH=f"{tmp}:{os.environ['PATH']}")
            subprocess.run(["make", "-s", "stu", "OUTPUT=/out"], cwd=ROOT, env=env,
                           check=True, capture_output=True)
            lines = seen.read_text().splitlines()
        self.assertEqual(lines[0], str(SCRIPT))
        self.assertIn("SOUNDTAP_STU_TARGET=soundtap-tri", lines)
        for name, path in (("RHINO_KERNEL", "uImage-st10"), ("SPOTTY_KERNEL", "uImage-st20"),
                           ("SPOTTY_MLO", "st20-spl/MLO"), ("BINKY_MLO", "wla-spl/MLO"),
                           ("BINKY_UBOOT", "wla-uboot/u-boot.img")):
            self.assertIn(f"SOUNDTAP_{name}=/out/images/{path}", lines)


if __name__ == "__main__":
    unittest.main()
