#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The ssh NAND backup reads, verifies the speaker's own table, and never writes.

The script runs against a fake ssh so the whole path is exercised offline: the
partition-table check, the per-partition size check, the OOB arithmetic and the
refusal when the speaker disagrees with the table.
"""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/recovery/nand-backup-ssh.sh"

SIZES = [0x20000, 0x20000, 0x20000, 0x20000, 0x1E0000, 0x20000, 0x500000, 0x500000,
         0x6000000, 0x6000000, 0x2600000, 0xB80000, 0x40000]
NAMES = ["SPL", "SPL.backup1", "SPL.backup2", "SPL.backup3", "U-Boot", "U-Boot-Env",
         "Kernel-Ping", "Kernel-Pong", "File-System-Ping", "File-System-Pong",
         "Persistent-Storage", "Update-Staging", "Manufacturing-Data"]


def fake_ssh(tmp: Path, sizes) -> Path:
    """An ssh that answers /proc/mtd and serves zeroed dumps of the right length."""
    table = "dev:    size   erasesize  name\n" + "".join(
        f'mtd{i}: {size:08x} 00020000 "{name}"\n'
        for i, (size, name) in enumerate(zip(sizes, NAMES)))
    table_file = tmp / "proc-mtd"
    table_file.write_text(table)
    sizes_file = tmp / "sizes"
    sizes_file.write_text(" ".join(str(s) for s in sizes))
    path = tmp / "fake-ssh"
    path.write_text(f"""#!/bin/bash
shift                       # drop the target
echo "$*" >>{tmp / "remote"}
if [ "$1" = cat ]; then cat {table_file}; exit 0; fi
n=${{*}}; n=${{n##*/dev/mtd}}
size=$(cut -d' ' -f $((n + 1)) {sizes_file})
head -c $((size / 2048 * 2112)) /dev/zero
""")
    path.chmod(0o755)
    return path


def bsd_wc(tmp: Path) -> Path:
    """A wc that pads its count the way macOS's BSD wc does, and a stat that
    rejects GNU's -c the way BSD stat does."""
    bindir = tmp / "bsd-bin"
    bindir.mkdir(exist_ok=True)
    wc = bindir / "wc"
    wc.write_text('#!/bin/sh\nprintf "%8d\\n" "$(/usr/bin/env -i PATH=/usr/bin:/bin wc "$@")"\n')
    wc.chmod(0o755)
    stat = bindir / "stat"
    stat.write_text('#!/bin/sh\ncase "$1" in -c*) echo "stat: illegal option -- c" >&2; exit 1;; esac\n'
                    'exec /usr/bin/env -i PATH=/usr/bin:/bin stat "$@"\n')
    stat.chmod(0o755)
    return bindir


def run(tmp: Path, sizes, path_prefix=None):
    out = tmp / "backup"
    env = dict(os.environ, SSH=str(fake_ssh(tmp, sizes)))
    if path_prefix:
        env["PATH"] = f"{path_prefix}:{env['PATH']}"
    return subprocess.run(["bash", str(SCRIPT), "speaker", str(out)],
                          env=env, capture_output=True, text=True), out


def remote(tmp: Path):
    """Every command the script sent to the speaker, in order."""
    path = tmp / "remote"
    return path.read_text().splitlines() if path.exists() else []


class NandBackupSsh(unittest.TestCase):
    def test_backup_reads_every_partition_and_writes_nothing(self):
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            done, out = run(tmp, SIZES)
            self.assertEqual(done.returncode, 0, done.stderr)
            # Read-only: the speaker is asked for its table and one OOB dump per partition.
            self.assertEqual(remote(tmp), ["cat /proc/mtd"] + [
                f"nanddump -o --bb=padbad /dev/mtd{i}" for i in range(len(SIZES))])
            for i, (size, name) in enumerate(zip(SIZES, NAMES)):
                got = (out / f"mtd{i}-{name}.oob").stat().st_size
                self.assertEqual(got, size // 2048 * 2112, f"mtd{i}")
            self.assertEqual((out / "SHA256SUMS").read_text().count("\n"), len(SIZES) + 1)

    def test_running_again_reads_nothing_and_a_changed_dump_is_refused(self):
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            _, out = run(tmp, SIZES)
            (tmp / "remote").unlink()
            # Running again is a no-op rather than a re-read: a broken transfer costs one
            # partition, not the whole backup.
            again, _ = run(tmp, SIZES)
            self.assertEqual(again.returncode, 0)
            self.assertIn("already have", again.stdout)
            self.assertEqual(remote(tmp), ["cat /proc/mtd"])
            # A dump changed after the fact (same length) is refused, not re-certified.
            spl = out / "mtd0-SPL.oob"
            data = bytearray(spl.read_bytes())
            data[100] ^= 0xFF
            spl.write_bytes(bytes(data))
            sums = (out / "SHA256SUMS").read_text()
            tampered, _ = run(tmp, SIZES)
            self.assertEqual(tampered.returncode, 1, tampered.stdout)
            self.assertIn("no longer matches", tampered.stderr)
            self.assertEqual((out / "SHA256SUMS").read_text(), sums, "the original checksums must survive")

    def test_a_speaker_whose_table_differs_is_not_dumped(self):
        # A speaker whose table differs is the discovery, not something to dump around.
        with tempfile.TemporaryDirectory() as d:
            wrong = list(SIZES)
            wrong[8] = 0x4000000
            done, out = run(Path(d), wrong)
            self.assertEqual(done.returncode, 1, done.stdout)
            self.assertIn("speaker says size", done.stderr)
            self.assertEqual(list(out.glob("*.oob")), [], "nothing may be dumped against a wrong table")
            self.assertEqual(remote(Path(d)), ["cat /proc/mtd"])

    def test_bsd_wc_and_stat(self):
        # BSD wc pads "13" to "      13"; the count check must not read that as a mismatch.
        # BSD stat has no -c; sizes must not depend on it. Twice, for the resume path.
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            for _ in range(2):
                done, _ = run(tmp, SIZES, bsd_wc(tmp))
                self.assertEqual(done.returncode, 0, done.stderr)

    def test_a_partition_fewer_is_refused(self):
        # A speaker with a partition fewer is still refused, padded wc or not.
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            short = fake_ssh(tmp, SIZES[:-1])
            for prefix in (None, bsd_wc(tmp)):
                env = dict(os.environ, SSH=str(short))
                if prefix:
                    env["PATH"] = f"{prefix}:{env['PATH']}"
                done = subprocess.run(["bash", str(SCRIPT), "speaker", str(tmp / "backup")],
                                      env=env, capture_output=True, text=True)
                self.assertEqual(done.returncode, 1, done.stderr)
                self.assertIn("refusing to guess", done.stderr)


if __name__ == "__main__":
    unittest.main()
