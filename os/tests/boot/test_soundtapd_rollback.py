# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""soundtapd_loop in init.d rolls a failing app override back to the built-in soundtapd.

The daemon is a stub; sleep, cut (the uptime clock), wget (the liveness probe) and the file
commands of the restore are stubbed too. The stub daemon ends the run by killing the loop
once the built-in binary has been moved into place.
"""
import tempfile
import unittest

from boot.init_script import InitScript

RESTORE = "mv /run/audio-bin/app/soundtapd.new /run/audio-bin/app/soundtapd"
COPY = "cp /opt/soundtap/soundtapd /run/audio-bin/app/soundtapd.new"


def run_loop(daemon_body: str, uptime_step: int, *, cp_fails_once: bool = False) -> tuple[list[str], str, list[str]]:
    """Run soundtapd_loop with an override installed; return (calls, log, override files left)."""
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        override = init.nv / "soundtap/soundtapd.override"
        override.parent.mkdir()
        for ext in (".gz", ".base", ".trial"):
            override.with_name(override.name + ext).touch()
        init.stub("sleep")
        # The probe misses only while the daemon is up and the built-in binary is not in yet.
        # The stubbed sleep makes the watchdog instant: without this it could kill a daemon
        # before it started (a run not counted) or the last one before it ended the loop.
        init.stub("wget", f'! grep -qx "{RESTORE}" "{init.calls_file}" || exit 0\n'
                          f'! kill -0 "$(cat "{d}/daemon.pid" 2>/dev/null)" 2>/dev/null || exit 1')
        init.stub("cut", f'n=$(($(cat "{d}/uptime" 2>/dev/null || echo 0) + {uptime_step}))\n'
                         f'echo $n >"{d}/uptime"\necho $n')
        init.stub("cp", f'[ -e "{d}/cp-failed" ] || {{ : >"{d}/cp-failed"; exit 1; }}' if cp_fails_once else "")
        init.stub("chmod")
        init.stub("mv")
        init.stub("devmem", "echo 0x00000000")  # a confirmed boot: no crash-loop reboot
        init.stub("reboot")
        init.stub("soundtapd", f'! grep -qx "{RESTORE}" "{init.calls_file}" || {{ kill $PPID; exit 0; }}\n'
                               f'echo $$ >"{d}/daemon.pid"\n{daemon_body}')
        init.run(f'APP_DIR=/run/audio-bin/app SOUNDTAPD_BIN="{init.bin}/soundtapd" SOUNDTAPD_OVERRIDE="{override}"\n'
                 f'WATCHDOG_DISARM="{init.run_dir}/booted" CRASH_REBOOT=1 BOOT_TRACE="{d}/boot-trace"\n'
                 'soundtapd_loop', check=False)
        left = sorted(p.name for p in override.parent.iterdir())
        return init.calls(), init.log.read_text(), left


def restart_delays(run_lengths: list[int]) -> list[int]:
    """Run soundtapd_loop over runs of the given lengths; return the logged restart delays."""
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        init.stub("sleep")
        init.stub("wget")  # answers: a liveness kill must not end a run before it counts itself
        # cut is read once at the start and once at the end of a run: the end adds this run's length.
        lengths = " ".join(str(n) for n in run_lengths)
        init.stub("cut", f'n=$(cat "{d}/uptime" 2>/dev/null || echo 0)\n'
                         f'if [ -e "{d}/running" ]; then rm "{d}/running"; i=$(cat "{d}/runs")\n'
                         f'  n=$((n + $(echo {lengths} | awk -v i=$i "{{print \\$i}}"))); else : >"{d}/running"; fi\n'
                         f'echo $n >"{d}/uptime"\necho $n')
        init.stub("soundtapd", f'i=$(($(cat "{d}/runs" 2>/dev/null || echo 0) + 1)); echo $i >"{d}/runs"\n'
                               f'[ $i -le {len(run_lengths)} ] || kill $PPID\nexit 1')
        init.run(f'APP_DIR=/run/audio-bin/app SOUNDTAPD_BIN="{init.bin}/soundtapd" SOUNDTAPD_OVERRIDE="{d}/none"\n'
                 f'WATCHDOG_DISARM="{init.run_dir}/booted" CRASH_REBOOT= BOOT_TRACE="{d}/boot-trace"\n'
                 'soundtapd_loop', check=False)
        return [int(line.split("restarting in ")[1].rstrip("s"))
                for line in init.log.read_text().splitlines() if "restarting in " in line]


def run_trial(*, usable: bool) -> tuple[list[str], str, list[str]]:
    """Run soundtapd_loop on an override on trial whose /api/info answers; return (calls, log, override files left).

    Only when usable do its web page and update routes answer too.
    """
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        override = init.nv / "soundtap/soundtapd.override"
        override.parent.mkdir()
        for ext in (".gz", ".base", ".trial"):
            override.with_name(override.name + ext).touch()
        trial = override.with_name(override.name + ".trial")
        init.stub("sleep")
        page = ('case "$*" in *api/info*) exit 0 ;; *--post-data*) '
                'echo "wget: server returned error: HTTP/1.1 400 Bad Request" >&2; exit 1 ;; '
                '*) echo "<!doctype html><html>" ;; esac') if usable else \
            'case "$*" in *api/info*) exit 0 ;; esac\nexit 1'
        init.stub("wget", page)
        init.stub("cut", "echo 1000")
        init.stub("cp")
        init.stub("chmod")
        init.stub("mv")
        init.stub("devmem", "echo 0x00000000")
        init.stub("reboot")
        # The first run ends the loop once its trial is over, either way; a second run (the
        # built-in copy after a rollback) ends it at once.
        init.stub("soundtapd", f'[ ! -e "{d}/ran" ] || {{ kill $PPID; exit 0; }}\n: >"{d}/ran"\n'
                               f'while [ -e "{trial}" ]; do /bin/sleep 0.05; done\nkill $PPID')
        init.run(f'APP_DIR=/run/audio-bin/app SOUNDTAPD_BIN="{init.bin}/soundtapd" SOUNDTAPD_OVERRIDE="{override}"\n'
                 f'WATCHDOG_DISARM="{init.run_dir}/booted" CRASH_REBOOT= BOOT_TRACE="{d}/boot-trace"\n'
                 'soundtapd_loop', check=False)
        left = sorted(p.name for p in override.parent.iterdir())
        return init.calls(), init.log.read_text(), left


class SoundtapdRollback(unittest.TestCase):
    """soundtapd_loop's restarts and override rollback."""

    def test_override_answering_only_api_info_rolls_back(self) -> None:
        """An override whose web page and update routes do not answer is rolled back."""
        calls, log, left = run_trial(usable=False)
        self.assertIn("soundtapd override dropped: its web page or update routes do not answer", log)
        self.assertEqual(calls.count(RESTORE), 1)
        self.assertEqual(left, [])

    def test_usable_override_is_kept(self) -> None:
        """A usable override is kept and leaves its trial marker behind."""
        calls, log, left = run_trial(usable=True)
        self.assertNotIn("override dropped", log)
        self.assertEqual(calls.count(RESTORE), 0)
        self.assertEqual(left, ["soundtapd.override.base", "soundtapd.override.gz"])

    def test_restarts_back_off_and_never_give_up(self) -> None:
        """Short runs double the wait from 2 s to at most 30 s; a run over 60 s starts at 2 s again."""
        self.assertEqual(restart_delays([1, 1, 1, 1, 1, 1, 1, 61, 1, 60, 1]),
                         [2, 4, 8, 16, 30, 30, 30, 2, 4, 8, 16])

    def test_hung_override_rolls_back(self) -> None:
        """A hung override is rolled back.

        Each run lives 200 s, past the 120 s window, and is killed by the liveness check:
        before, the count reset on every kill and the override ran forever.
        """
        calls, log, left = run_loop("exec /bin/sleep 600", 200)
        self.assertGreaterEqual(log.count("no answer on /api/info 5 times"), 3)
        self.assertIn("soundtapd override dropped: it failed 3 times in a row", log)
        self.assertEqual(calls.count(RESTORE), 1)
        self.assertEqual(calls.count("soundtapd -home /mnt/nv/soundtap"), 4)
        self.assertEqual(left, [])

    def test_failed_restore_is_retried(self) -> None:
        """A failed restore of the built-in binary is retried.

        The first copy of the built-in binary fails: the override and its markers
        stay, so the next quick exit tries again and that copy goes in.
        """
        calls, log, left = run_loop("exit 1", 10, cp_fails_once=True)
        self.assertIn("restoring the firmware's soundtapd failed, retrying", log)
        self.assertEqual(calls.count(COPY), 2)
        self.assertEqual(calls.count(RESTORE), 1)
        self.assertEqual(calls.count("soundtapd -home /mnt/nv/soundtap"), 5)
        self.assertNotIn("reboot -f", calls)
        self.assertEqual(left, [])


if __name__ == "__main__":
    unittest.main()
