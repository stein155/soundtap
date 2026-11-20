# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Unconfirmed-boot recovery follows the bootloader's count, not the module name."""
import re
import tempfile
import unittest

from boot.init_script import INIT, InitScript

BOOTSELECT = (INIT.parents[4] / "app/internal/flash/bootselect.go").read_text()
LIMIT = int(re.search(r"const BootLimit = (\d+)", BOOTSELECT).group(1))


def scratch2(count: int) -> str:
    """SCRATCH2 holding U-Boot's count as Linux reads it: big-endian, so byte-swapped."""
    return f"0x{count:02X}0001B0"


def run(scratch2: str, cmdline: str = "soundtap.nandroot=1", model: str = "") -> str:
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        (init.proc / "cmdline").write_text(cmdline + "\n")
        mode = init.sys / "kernel/reboot/mode"
        mode.parent.mkdir(parents=True)
        mode.write_text("cold\n")
        init.stub("devmem", f"echo {scratch2}")
        out = init.run(f"NANDROOT=1 WATCHDOG_SECONDS= WATCHDOG_NETWORK=1 CRASH_REBOOT= MODEL={model}\n"
                       "unconfirmed_boot_guard >/dev/null\n"
                       'echo "$CRASH_REBOOT/$WATCHDOG_SECONDS/$WATCHDOG_NETWORK"').stdout.strip()
        return f"{out} {mode.read_text().strip()}"


def crash_loop(scratch2: str) -> list:
    """A soundtapd that dies at once, over and over, before it ever confirms the boot."""
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        init.stub("devmem", f"echo {scratch2}")
        init.stub("soundtapd", "exit 1")
        init.stub("reboot", "kill $PPID")  # ends the subshell the loop runs in
        for name in ("sleep", "sync"):
            init.stub(name)
        init.stub("wget", "exit 1")
        init.run(f"NANDROOT=1 WATCHDOG_SECONDS= WATCHDOG_NETWORK=1 CRASH_REBOOT=\n"
                 f"BOOT_TRACE={d}/trace SOUNDTAPD_BIN={init.bin}/soundtapd SOUNDTAPD_OVERRIDE={d}/none\n"
                 f"WATCHDOG_DISARM={d}/booted\n"
                 "unconfirmed_boot_guard >/dev/null\n"
                 "(soundtapd_loop >/dev/null)", check=False)
        return [c for c in init.calls() if c.startswith(("soundtapd", "reboot"))]


class UnconfirmedBootGuard(unittest.TestCase):
    def test_the_module_does_not_change_the_verdict(self):
        """Every board under our U-Boot counts boots the same way; the module type or model
        it boots as must not decide whether recovery is armed."""
        for count in ("0x010001B0", "0x00000000"):
            verdicts = {run(count, f"soundtap.nandroot=1 variant={variant} module_type={module}", model)
                        for variant, module, model in (("rhino", "sm2", "st10"), ("spotty", "scm", "st20"),
                                                       ("binky", "sm2", "wla"), ("ginger", "sm2", "st300"))}
            self.assertEqual(len(verdicts), 1, verdicts)

    # The reboot mode is set_reboot_mode's alone (test_reboot_mode.py): the guard leaves the
    # one the test seeds, cold, as it is.
    def test_counting_bootloader_arms_recovery(self):
        self.assertEqual(run("0x010001B0"), "1/300/0 cold")

    def test_confirmed_or_non_counting_boot_stays_unarmed(self):
        self.assertEqual(run("0x00000000"), "//1 cold")

    def test_both_slots_tried_stays_up(self):
        # Counts 1 to bootlimit try the selected slot, bootlimit+1 to 2*bootlimit+1 the
        # fallback (ADR 0010): armed through the fallback's last try, not past it.
        self.assertEqual(run(scratch2(2 * LIMIT + 1)), "1/300/0 cold")
        self.assertEqual(run(scratch2(2 * LIMIT + 2)), "//1 cold")

    def test_crash_loop_on_counting_bootloader_reboots(self):
        # The ST20 path: daemon exits keep the hardware watchdog fed, so only this
        # reboot lets U-Boot count the slot out.
        self.assertEqual(crash_loop("0x010001B0"), ["soundtapd -home /mnt/nv/soundtap"] * 5 + ["reboot -f"])


if __name__ == "__main__":
    unittest.main()
