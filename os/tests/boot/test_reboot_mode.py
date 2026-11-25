# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The reboot mode is written once, early, and warm only where U-Boot counts boots (ADR 0010).

Init used to write cold first and warm several steps later, so a panic in between rebooted
cold and cleared the boot count in SCRATCH2.
"""
import re
import tempfile
import unittest

from boot.init_script import INIT, InitScript

NAND = "console=ttyS3 soundtap.nandroot=1 init=/init"
RAM = "console=ttyGS0 init=/init"


def reboot_mode(cmdline: str, scratch2: str | None = None, seeded: str = "unset") -> str:
    """Run set_reboot_mode with this command line and SCRATCH2; return what it wrote."""
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        (init.proc / "cmdline").write_text(cmdline + "\n")
        mode = init.sys / "kernel/reboot/mode"
        mode.parent.mkdir(parents=True)
        mode.write_text(seeded + "\n")
        if scratch2 is not None:
            init.stub("devmem", f"echo {scratch2}")
        else:
            init.stub("devmem", "exit 1")  # a build without devmem
        init.run("set_reboot_mode")
        return mode.read_text().strip()


class RebootMode(unittest.TestCase):
    """set_reboot_mode and where init calls it."""

    def test_counting_nand_boot_reboots_warm(self) -> None:
        """A NAND boot that U-Boot counts reboots warm."""
        for scratch2 in ("0x010001B0", "0x030001b0", "0x080001B0"):
            with self.subTest(scratch2=scratch2):
                self.assertEqual(reboot_mode(NAND, scratch2), "warm")

    def test_everything_else_reboots_cold(self) -> None:
        """A confirmed boot, a build without devmem, a RAM boot and other magic reboot cold."""
        cases = {
            "confirmed (SCRATCH2 cleared)": (NAND, "0x00000000"),
            "no devmem": (NAND, None),
            "RAM boot with a count": (RAM, "0x010001B0"),
            "other magic": (NAND, "0x0100BEEF"),
        }
        for name, (cmdline, scratch2) in cases.items():
            with self.subTest(name):
                self.assertEqual(reboot_mode(cmdline, scratch2, seeded="warm"), "cold")

    def test_set_once_before_anything_else_runs(self) -> None:
        """Only set_reboot_mode writes the mode, and mount_filesystems calls it right after the mounts.

        It runs as soon as /proc, /sys and /dev are mounted: nothing between those and it can
        panic first.

        Read as text: mount_filesystems mounts the host's own /proc and /sys through
        /bin/busybox, so it cannot run outside a speaker, and "no other writer" is an absence.
        """
        text = INIT.read_text()
        fn = re.search(r"^set_reboot_mode\(\) \{\n.*?^\}\n", text, re.DOTALL | re.MULTILINE).group(0)
        writers = [line for line in text.replace(fn, "").splitlines()
                   if "reboot/mode" in line and not line.lstrip().startswith("#")]
        self.assertEqual(writers, [])
        mounts = re.search(r"^mount_filesystems\(\) \{\n(.*?)^\}\n", text, re.DOTALL | re.MULTILINE).group(1)
        steps = [line for line in mounts.splitlines() if line and not line.lstrip().startswith("#")]
        self.assertEqual(steps.index("set_reboot_mode"), 4, steps[:6])  # after proc, sys, dev, pstore


if __name__ == "__main__":
    unittest.main()
