# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Model detection and the NAND flag, run against the command lines the boards really boot with.

A flag or variant the init script does not read the way the boot recipes write it loses
persistent storage on every NAND boot (ADR 0040), and a speaker whose model reads wrong
must stop before it drives the wrong hardware.
"""
import re
import tempfile
import unittest

from boot.init_script import INIT, InitScript

ROOT = INIT.parents[4]
DTS = ROOT / "os/board/common/dts/ti/omap"


def recipe_cmdline() -> str:
    """The bootargs the installer's U-Boot recipe sets for an ST10 slot."""
    go = (ROOT / "app/internal/flash/recipes.go").read_text()
    block = go[go.index('"soundtap_args=setenv bootargs '):go.index('"stock_args=')]
    args = "".join(re.findall(r'"([^"]*)"', block)).removeprefix("soundtap_args=setenv bootargs ")
    return args.replace("${rmtd}", "8").replace("%s", "rhino", 1).replace("%s", "sm2", 1)


def boot(cmdline: str, variant=None, persisted=None):
    """Runs the boot's identity steps; returns (exit code, "NANDROOT MAINLINE MODEL NOCODEC WATCHDOG", log)."""
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        (init.proc / "cmdline").write_text(cmdline + "\n")
        if variant is not None:
            (init.proc / "variant").write_text(variant + "\n")
        if persisted is not None:
            (init.nv / "model").write_text(persisted + "\n")
        init.stub("uname", "echo 6.12.0")
        init.stub("mount", "exit 1")  # no tmpfs: the audio binaries stay where they are
        done = init.run("{ detect_kernel; read_boot_watchdog; detect_model; } >/dev/null\n"
                        'echo "$NANDROOT $MAINLINE $MODEL $NOCODEC ${WATCHDOG_SECONDS:--}"', check=False)
        log = init.log.read_text() if init.log.exists() else ""
        return done.returncode, done.stdout.strip(), log


class DetectModel(unittest.TestCase):
    def test_each_board_boots_as_its_model_from_nand(self):
        boards = {"st10": "st10 0", "st20": "st20 0", "st20-iii": "st10 0", "wla": "wla 1", "st300": "st300 1"}
        for board, model_nocodec in boards.items():
            with self.subTest(board=board):
                dts = (DTS / f"am335x-soundtap-{board}.dts").read_text()
                cmdline = re.search(r'bootargs = "([^"]*)"', dts).group(1)
                # The watchdog flag is passed but never armed on NAND: a reboot lands on the same slot.
                self.assertEqual(boot(cmdline)[:2], (0, f"1 1 {model_nocodec} -"))

    def test_installer_recipe_boots_from_nand(self):
        self.assertEqual(boot(recipe_cmdline())[:2], (0, "1 1 st10 0 -"))

    def test_proc_variant_alone_is_enough(self):
        self.assertEqual(boot("root=ubi0:rootfs", variant="binky")[:2], (0, "0 1 wla 1 -"))

    def test_persisted_model_never_overrides_the_hardware(self):
        code, out, log = boot("variant=binky soundtap.nandroot=1", persisted="st10")
        self.assertEqual((code, out), (0, "1 1 wla 1 -"))
        self.assertIn("ignored persisted model conflicting with hardware: st10", log)

    def test_unknown_variant_stops_the_boot(self):
        code, out, log = boot("variant=nonsense soundtap.nandroot=1")
        self.assertEqual((code, out), (1, ""))
        self.assertIn("refusing hardware and network bring-up", log)

    def test_conflicting_identity_stops_the_boot(self):
        code, _, log = boot("variant=rhino", variant="spotty")
        self.assertEqual(code, 1)
        self.assertIn("conflicting immutable hardware identity: proc=st20 cmdline=st10", log)

    def test_no_identity_stops_the_boot(self):
        code, _, log = boot("root=ubi0:rootfs")
        self.assertEqual(code, 1)
        self.assertIn("immutable hardware identity unavailable", log)

    def test_ram_boot_arms_the_watchdog(self):
        self.assertEqual(boot("variant=rhino soundtap.watchdog=90")[:2], (0, "0 1 st10 0 90"))


if __name__ == "__main__":
    unittest.main()
