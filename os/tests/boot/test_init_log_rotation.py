# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Boot logs survive an unclean reboot and rotate over three boots.

Runs the init script's save-log / rotate-logs subcommands, its 15-minute save loop and its
network watch against a temp dir.
"""
import tempfile
import unittest
from pathlib import Path

from boot.init_script import InitScript


class LogRotation(unittest.TestCase):
    """save-log, rotate-logs and the clock save loop."""

    def run_cmd(self, tmp: str, cmd: str, *, marker: bool = True, df: str | None = None) -> Path:
        """Run an init subcommand in the temp root tmp; return the persistent soundtap directory."""
        init = InitScript(tmp)
        nv = init.nv / "soundtap"
        nv.mkdir(exist_ok=True)
        if marker:
            (init.run_dir / "persistent-storage").touch()
        init.stub("sync")
        if df is not None:
            init.stub("df", df)
        init.run(f"run_subcommand {cmd}")
        return nv

    def ram_log(self, tmp: str, text: str) -> None:
        """Write text as this boot's log in RAM."""
        InitScript(tmp).log.write_text(text)

    def test_three_boots_kept_oldest_dropped(self) -> None:
        """Three boots' logs are kept and the oldest is dropped."""
        with tempfile.TemporaryDirectory() as tmp:
            for boot in "abcd":
                self.ram_log(tmp, boot * 10)
                self.run_cmd(tmp, "save-log")
                nv = self.run_cmd(tmp, "rotate-logs")
            names = sorted(p.name for p in nv.iterdir())
            self.assertEqual(names, ["previous-boot.1.log", "previous-boot.2.log", "previous-boot.log"])
            self.assertEqual((nv / "previous-boot.log").read_text(), "d" * 10)
            self.assertEqual((nv / "previous-boot.2.log").read_text(), "b" * 10)

    def test_tail_is_bounded(self) -> None:
        """The saved tail is bounded to 32 KiB."""
        with tempfile.TemporaryDirectory() as tmp:
            self.ram_log(tmp, "x" * 100000)
            nv = self.run_cmd(tmp, "save-log")
            self.assertEqual((nv / "current-boot.log").stat().st_size, 32768)

    def test_long_volume_name_still_saves(self) -> None:
        """A long UBI volume name still saves; BusyBox df without -P puts it on its own line."""
        fake = ('case "$1" in *P*) printf "F 1024-blocks Used Available Capacity Mounted\\n'
                'ubi1:persistent_volume 32224 2168 28372 7%% /mnt/nv\\n";; *) printf "F 1K-blocks Used '
                'Available Use%% Mounted\\nubi1:persistent_volume\\n 32224 2168 28372 7%% /mnt/nv\\n";; esac')
        with tempfile.TemporaryDirectory() as tmp:
            self.ram_log(tmp, "x")
            nv = self.run_cmd(tmp, "save-log", df=fake)
            self.assertEqual((nv / "current-boot.log").read_text(), "x")

    def test_no_persistent_storage_writes_nothing(self) -> None:
        """Without persistent storage nothing is written."""
        with tempfile.TemporaryDirectory() as tmp:
            self.ram_log(tmp, "x")
            nv = self.run_cmd(tmp, "save-log", marker=False)
            self.assertEqual(list(nv.iterdir()), [])

    def test_rotate_without_snapshot_keeps_older_logs(self) -> None:
        """A rotation without a snapshot of this boot keeps the older logs."""
        with tempfile.TemporaryDirectory() as tmp:
            nv = Path(tmp) / "mnt/nv/soundtap"
            nv.mkdir(parents=True)
            (nv / "previous-boot.log").write_text("old")
            self.run_cmd(tmp, "rotate-logs")
            self.assertEqual((nv / "previous-boot.log").read_text(), "old")

    def test_snapshot_rides_the_clock_save_loop(self) -> None:
        """Every 15 minutes the clock is saved, then the log.

        So a watchdog reset or a panic leaves the tail of this boot behind.
        """
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            init.stub("init.d-soundtap")
            ticks = Path(tmp) / "ticks"
            # Two 15-minute ticks, then sleep fails and the loop ends.
            init.stub("sleep", f'echo >>"{ticks}"; [ "$(wc -l <"{ticks}")" -le 2 ]')
            init.run("PERSISTENT_CLOCK=1\nstart_clock_saver\nwait")
            self.assertEqual(init.calls(), ["sleep 900", "init.d-soundtap save-clock", "init.d-soundtap save-log"] * 2
                             + ["sleep 900"])

    def test_a_failed_clock_save_still_saves_the_log(self) -> None:
        """A failed clock save is logged and the log is still saved."""
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            init.stub("init.d-soundtap", '[ "$1" != save-clock ]')
            init.stub("sleep", f'[ ! -e "{tmp}/ticked" ] && : >"{tmp}/ticked"')
            init.run("PERSISTENT_CLOCK=1\nstart_clock_saver\nwait")
            self.assertIn("init.d-soundtap save-log", init.calls())
            self.assertIn("WARNING: persistent clock save failed", init.log.read_text())

    def test_no_persistent_clock_starts_no_loop(self) -> None:
        """Without a persistent clock no save loop starts."""
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            init.stub("init.d-soundtap")
            init.stub("sleep", "exit 1")
            init.run("PERSISTENT_CLOCK=0\nstart_clock_saver\nwait")
            self.assertEqual(init.calls(), [])


class NetworkDiagnostics(unittest.TestCase):
    """netwatch's record of a boot without a network."""

    def test_reads_the_log_file_not_the_fifo(self) -> None:
        """With no default route after three minutes, netwatch keeps the end of the log file.

        $LOG is a FIFO under syslogd: tail on it would never end and would steal lines.
        """
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            init.stub("ip", 'case "$*" in "-4 route show default") ;; *) echo "ip $*";; esac')
            init.stub("sleep")
            init.stub("date", "echo DATE")
            log_file = Path(tmp) / "soundtap.log"
            log_file.write_text("".join(f"line {n}\n" for n in range(1, 201)))
            netfail = Path(tmp) / "netfail.log"
            init.run(f'rm -f "$LOG"; mkfifo "$LOG"; LOG_FILE="{log_file}" NETFAIL_LOG="{netfail}"\n'
                     f'BOOT_TRACE="{tmp}/trace"\nnetwatch\nwait')
            kept = netfail.read_text().splitlines()
            trace = Path(tmp, "trace").read_text()
        self.assertIn("ip addr", kept)
        self.assertEqual(kept[-150:], [f"line {n}" for n in range(51, 201)])
        self.assertIn("no default route after 180s", trace)

    def test_a_default_route_is_marked_and_nothing_kept(self) -> None:
        """A default route is noted in the boot trace and no failure log is kept."""
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            init.stub("ip", 'echo "default via 192.0.2.1 dev wlan0"')
            init.stub("sleep")
            netfail = Path(tmp) / "netfail.log"
            init.run(f'NETFAIL_LOG="{netfail}" LOG_FILE="{tmp}/none" BOOT_TRACE="{tmp}/trace"\n'
                     "netwatch\nwait")
            self.assertFalse(netfail.exists())
            self.assertIn("network up after 0s: default via 192.0.2.1 dev wlan0",
                          Path(tmp, "trace").read_text())


if __name__ == "__main__":
    unittest.main()
