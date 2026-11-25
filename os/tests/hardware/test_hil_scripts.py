# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The bench scripts in tools/hil/ run against a fake speaker (tools/ci/fake_speaker.py).

They have to work on a production image, which has no SSH (ADR 0119): the boot is read from
/api/health, and every install and reboot waits for a key held on the speaker, which the
script asks for. These tests run the scripts as an owner would, with a person at the fake
speaker who holds the key when the script says so, and pulls the plug when it asks.
"""
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
HIL = ROOT / "tools/hil"
sys.path.insert(0, str(ROOT / "tools/ci"))
from fake_speaker import FakeSpeaker, serve  # noqa: E402

PROMPT = ">>> Hold a button on the speaker now"


class Run:
    """One script run, with a person at the speaker who reads its prompts."""

    def __init__(self, sp: FakeSpeaker, script: str, *args: str, env: dict[str, str] | None = None,
                 hold: bool = True, hold_after: float = 0.0) -> None:
        """Run `script` with `args` to the end, holding the key `hold_after` seconds after each prompt if `hold`."""
        self.sp = sp
        self.prompts, self.manual, self.err, self.out = 0, 0, [], []
        self.hold, self.hold_after = hold, hold_after
        self.proc = subprocess.Popen(["sh", str(HIL / script), *args], env=env,
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=subprocess.PIPE, text=True)
        out = threading.Thread(target=lambda: self.out.extend(self.proc.stdout))
        out.start()
        for line in self.proc.stderr:
            self.err.append(line)
            if line.startswith(PROMPT):
                self.prompts += 1
                if self.hold:
                    threading.Timer(self.hold_after, sp.press).start()
            elif line.startswith(">>> Cold boot"):
                self.manual += 1
                sp.power_cut()
                self.proc.stdin.write("\n")
                self.proc.stdin.flush()
        self.code = self.proc.wait(timeout=120)
        out.join()
        for pipe in (self.proc.stdin, self.proc.stdout, self.proc.stderr):
            pipe.close()
        self.stdout, self.stderr = "".join(self.out), "".join(self.err)


class HilScriptTest(unittest.TestCase):
    """The tools/hil scripts against a fake speaker on a production or alpha image."""

    def setUp(self) -> None:
        """Make a temporary bin with an ssh that finds no port 22, and a signed candidate STU."""
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.bin = self.dir / "bin"
        self.bin.mkdir()
        self.ssh_calls = self.dir / "ssh-calls"
        self.token = self.dir / "confirm-token"
        # A production image: nothing listens on port 22.
        self.stub_ssh("exit 255")
        self.stu = self.dir / "SoundTap-Core-0.2.0.stu"
        self.stu.write_text("0.2.0\n")
        Path(str(self.stu) + ".sig").write_text("c2lnbmF0dXJl\n")

    def stub_ssh(self, body: str) -> None:
        """Replace ssh with a stub that logs its command and then runs `body`."""
        ssh = self.bin / "ssh"
        ssh.write_text("#!/bin/sh\n"
                       f"for last; do :; done\nprintf '%s\\n' \"$last\" >>'{self.ssh_calls}'\n"
                       + body + "\n")
        ssh.chmod(0o755)

    def speaker(self, **kw: float | str) -> tuple[FakeSpeaker, str]:
        """Serve a fake speaker made with `kw`; return it and its address."""
        kw.setdefault("token_file", str(self.token))
        sp = FakeSpeaker(**kw)
        httpd = serve(sp)
        self.addCleanup(httpd.server_close)
        self.addCleanup(httpd.shutdown)
        return sp, f"127.0.0.1:{httpd.server_address[1]}"

    def env(self, **extra: str) -> dict[str, str]:
        """Return the scripts' environment with the stub ssh first on PATH and `extra` on top."""
        env = dict(os.environ, PATH=f"{self.bin}{os.pathsep}{os.environ['PATH']}",
                   SOUNDTAP_HIL_POLL="0.05", SOUNDTAP_HIL_TIMEOUT="20", SOUNDTAP_HIL_SSH="auto",
                   SOUNDTAP_HIL_SERIAL="")
        env.update(extra)
        return env

    def ssh_commands(self) -> list[str]:
        """Return the commands the stub ssh was asked to run."""
        return self.ssh_calls.read_text().splitlines() if self.ssh_calls.exists() else []

    def gated(self, sp: FakeSpeaker) -> list[tuple[str, str]]:
        """Return each gated POST the speaker took (volume aside) with how it was authorised."""
        return [(p, how) for m, p, how in sp.requests if m == "POST" and p != "/api/volume"]

    def test_install_candidate_on_a_production_image(self) -> None:
        """Without SSH the install and the reboot each wait for the hold, and the slot switches."""
        sp, addr = self.speaker()
        run = Run(sp, "install-candidate.sh", addr, str(self.stu), env=self.env())
        self.assertEqual(run.code, 0, run.stderr + run.stdout)
        self.assertIn("PASS", run.stdout)
        self.assertIn("after: PONG", run.stdout)
        self.assertIn('"os": "0.2.0"', run.stdout)
        self.assertIn("no ssh (production image)", run.stderr)
        # The install and the reboot each waited for the hold, after the script asked for it.
        self.assertEqual(run.prompts, 2)
        self.assertEqual(self.gated(sp), [("/api/firmware", "hold"), ("/api/reboot", "hold")])
        self.assertEqual((sp.running, sp.selected, sp.confirmed), ("PONG", "PONG", True))
        self.assertEqual(sp.volume, 12, "the volume is not back")
        # Only the probe for SSH went to port 22.
        self.assertEqual(self.ssh_commands(), ["true"])

    def test_install_without_the_hold_stops_before_anything_is_written(self) -> None:
        """An install nobody holds the key for fails with 403 and writes and reboots nothing."""
        sp, addr = self.speaker(hold_window=0.5)
        run = Run(sp, "install-candidate.sh", addr, str(self.stu), env=self.env(), hold=False)
        self.assertEqual(run.code, 1)
        self.assertIn("install: HTTP 403", run.stdout)
        self.assertEqual(run.prompts, 1)
        self.assertEqual((sp.selected, sp.builds["PONG"]), ("PING", "0.1.0"))
        self.assertEqual(sp.boots, [], "it rebooted")

    def test_install_candidate_on_an_alpha_image_uses_the_token(self) -> None:
        """With root SSH the script reads the confirm token and asks for no hold."""
        self.stub_ssh(f"case $last in true) exit 0 ;; *confirm-token*) cat '{self.token}' ;; "
                      "*) exit 1 ;; esac")
        sp, addr = self.speaker()
        run = Run(sp, "install-candidate.sh", addr, str(self.stu), env=self.env(), hold=False)
        self.assertEqual(run.code, 0, run.stderr + run.stdout)
        self.assertIn("root ssh (alpha image)", run.stderr)
        self.assertEqual(run.prompts, 0)
        self.assertEqual(self.gated(sp), [("/api/firmware", "token"), ("/api/reboot", "token")])
        self.assertEqual(sp.running, "PONG")

    def test_a_reboot_that_does_not_come_back_fails_with_the_console(self) -> None:
        """A speaker that does not come back fails the run and shows and logs the serial console."""
        console = self.dir / "uart"
        console.write_text("U-Boot SPL 2026.01\nTrying to boot from NAND\n")
        log = self.dir / "console.log"
        sp, addr = self.speaker()
        sp.never_back = True
        run = Run(sp, "install-candidate.sh", addr, str(self.stu),
                  env=self.env(SOUNDTAP_HIL_TIMEOUT="1", SOUNDTAP_HIL_SERIAL=str(console),
                               SOUNDTAP_HIL_CONSOLE_LOG=str(log)))
        self.assertEqual(run.code, 1)
        self.assertIn("FAIL: not back 1 s after the reboot", run.stderr)
        self.assertIn("Trying to boot from NAND", run.stderr, "no console lines with the failure")
        self.assertIn("U-Boot SPL 2026.01", log.read_text())
        self.assertNotIn("PASS", run.stdout)

    def test_install_app(self) -> None:
        """install-app.sh installs soundtapd after one hold and leaves the volume as it was."""
        sp, addr = self.speaker()
        app = self.dir / "soundtapd"
        app.write_text("0.2.1\n")
        Path(str(app) + ".sig").write_text("c2ln\n")
        sp.volume = 7
        run = Run(sp, "install-app.sh", addr, str(app), env=self.env())
        self.assertEqual(run.code, 0, run.stderr + run.stdout)
        self.assertIn('"app": "0.2.1"', run.stdout)
        self.assertEqual(run.prompts, 1)
        self.assertEqual(self.gated(sp), [("/api/soundtapd", "hold")])
        self.assertEqual(sp.volume, 7)

    def test_boot_soak(self) -> None:
        """boot-soak.sh reports the install, each warm reboot and the cold boot, then passes."""
        sp, addr = self.speaker()
        run = Run(sp, "boot-soak.sh", addr, str(self.stu), "1", "2", "1", env=self.env())
        self.assertEqual(run.code, 0, run.stderr + run.stdout)
        lines = run.stdout.splitlines()
        self.assertRegex(lines[0], r"^install 1: rebooted after \d+ s total \d+ s, after: PONG, ")
        self.assertRegex(lines[1], r"^warm reboot 1: up \d+s confirmed \d+s PONG previous=reboot$")
        self.assertRegex(lines[2], r"^warm reboot 2: up \d+s confirmed \d+s PONG previous=reboot$")
        self.assertRegex(lines[3], r"^cold boot 1: up \d+s confirmed \d+s PONG previous=power loss$")
        self.assertEqual(lines[4], "PASS")
        # Two holds for the install, one per warm reboot; the power cut was asked for.
        self.assertEqual((run.prompts, run.manual), (4, 1))
        self.assertEqual(sp.boots, ["PONG"] * 4)

    def test_rollback_on_a_production_image(self) -> None:
        """rollback-check.sh sees four unconfirmed boots fall back to the other slot, which stays."""
        sp, addr = self.speaker(confirm_after=2.0)
        run = Run(sp, "rollback-check.sh", addr, env=self.env())
        self.assertEqual(run.code, 0, run.stderr + run.stdout)
        self.assertIn("slot under test: PING", run.stdout)
        self.assertIn("fell back to PONG after 4 unconfirmed boots", run.stdout)
        self.assertIn("PASS: PING -> PONG, kept after a reboot", run.stdout)
        # Four boots of PING reset before their confirm, the fallback, and the reboot after.
        self.assertEqual(sp.boots, ["PING"] * 4 + ["PONG", "PONG"])
        self.assertEqual(sp.selected, "PONG")
        self.assertEqual(run.prompts, 6)

    def test_the_fake_keeps_an_install_through_three_power_cuts(self) -> None:
        """The fake keeps an unconfirmed slot through three power cuts and falls back after the fourth.

        ADR 0010, bootlimit 4: an unconfirmed selection keeps its count across power cuts,
        so three cuts before the confirm leave the fourth boot on the new slot; after four
        unconfirmed boots the fifth boots the old one.
        """
        sp = FakeSpeaker(confirm_after=60.0, down_for=0.0)

        def gated() -> dict[str, str]:
            return {"X-SoundTap-Device-Token": sp.token, "X-SoundTap-Signature": "c2ln",
                    "X-SoundTap-Confirm": "install"}

        self.assertEqual(sp.post("/api/firmware", "install=1", gated(), b"0.2.0")[0], 200)
        self.assertEqual(sp.post("/api/reboot", "", gated(), b'{"confirm":true}')[0], 200)
        sp.tick()
        for _ in range(3):
            sp.power_cut()
            sp.tick()
        self.assertEqual((sp.boots, sp.count), (["PONG"] * 4, 4))
        sp.power_cut()
        sp.tick()
        self.assertEqual(sp.boots[-1], "PING")

    def test_rollback_fails_when_a_boot_confirms_before_its_reboot(self) -> None:
        """The check fails when the key is held too late and the boot confirms while the reboot waits."""
        sp, addr = self.speaker(confirm_after=1.0, reboot_delay=1.0)
        run = Run(sp, "rollback-check.sh", addr, env=self.env(), hold_after=1.5)
        self.assertEqual(run.code, 1, run.stdout)
        self.assertIn("FAIL: boot 1 on PING confirmed before it rebooted", run.stderr)


if __name__ == "__main__":
    unittest.main()
