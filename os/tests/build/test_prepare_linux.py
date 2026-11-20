# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""prepare-linux.sh must stop, not record a partial digest, when an input cannot be hashed."""

import os
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "os/board/rootfs/prepare-linux.sh"
INPUTS = [
    "os/configs/soundtap_defconfig",
    "os/board/image/linux.fragment",
    "os/board/common/patches/linux/a.patch",
    "os/board/rootfs/linux-patches/b.patch",
    "os/drivers/soundtap-st10-audio.c",
    "os/external.mk",
    "os/board/common/dts/c.dts",
    "os/drivers/aic3256-loader.c",
    "os/drivers/aic3256-bitbang.c",
    "os/package/soundtap-aic3256-loader/Config.in",
    "os/package/soundtap-aic3256-loader/soundtap-aic3256-loader.hash",
    "os/package/soundtap-aic3256-loader/soundtap-aic3256-loader.mk",
]
STAMPS = [
    ".soundtap-linux-inputs.sha256",
    ".soundtap-linux-dts-inputs.sha256",
    ".soundtap-aic3256-loader-inputs.sha256",
]


class PrepareLinuxTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = pathlib.Path(self.tmp.name, "repo")
        self.out = pathlib.Path(self.tmp.name, "out")
        self.out.mkdir()
        for name in INPUTS:
            path = self.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("BR2_arm=y\n" if name.endswith("defconfig") else name + "\n")

    def tearDown(self):
        self.tmp.cleanup()

    def record(self, env=None):
        return subprocess.run(
            ["sh", str(SCRIPT), str(self.repo), str(self.out), "record"],
            env=env, capture_output=True, text=True,
        )

    def stamps(self):
        return [(self.out / s).read_text() if (self.out / s).exists() else None for s in STAMPS]

    def test_record_writes_three_digests(self):
        self.assertEqual(self.record().returncode, 0)
        self.assertTrue(all(s and len(s.strip()) == 64 for s in self.stamps()))

    def test_missing_input_fails_and_keeps_stamps(self):
        self.assertEqual(self.record().returncode, 0)
        before = self.stamps()
        (self.repo / INPUTS[-1]).unlink()
        self.assertNotEqual(self.record().returncode, 0)
        self.assertEqual(self.stamps(), before)

    def test_failing_hash_command_fails(self):
        shim = pathlib.Path(self.tmp.name, "bin")
        shim.mkdir()
        fake = shim / "sha256sum"
        fake.write_text('#!/bin/sh\ncase "$*" in *aic3256-loader.c*) exit 1 ;; esac\n'
                        'exec /usr/bin/env -u PATH PATH=/usr/bin:/bin sha256sum "$@"\n')
        fake.chmod(0o755)
        env = dict(os.environ, PATH=f"{shim}:{os.environ['PATH']}")
        self.assertNotEqual(self.record(env).returncode, 0)
        self.assertEqual(self.stamps(), [None, None, None])


if __name__ == "__main__":
    unittest.main()
