# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""keep_clock starts the long-running ntpd once, even when two syncs finish together."""

import contextlib
import os
import pathlib
import signal
import subprocess
import tempfile
import unittest

from boot.init_script import InitScript

ROOT = pathlib.Path(__file__).resolve().parents[3]
CLOCK = ROOT / "os/overlay/opt/soundtap/clock.sh"


class KeepClockTest(unittest.TestCase):
    def run_keep(self, tmp, claimed):
        bin_dir = pathlib.Path(tmp, "bin")
        bin_dir.mkdir()
        starts = pathlib.Path(tmp, "starts")
        ntpd = bin_dir / "ntpd"
        ntpd.write_text(f"#!/bin/sh\necho started >>{starts}\n")
        ntpd.chmod(0o755)
        pid_file = pathlib.Path(tmp, "ntpd.pid")
        if claimed:
            pathlib.Path(f"{pid_file}.claim").mkdir()
        env = dict(os.environ, PATH=f"{bin_dir}:{os.environ['PATH']}", NTPD_PID=str(pid_file))
        subprocess.run(["sh", "-c", f". {CLOCK}; keep_clock; wait"], env=env, check=True)
        return starts.read_text().count("started") if starts.exists() else 0, pid_file

    def test_starts_ntpd_and_releases_the_claim(self):
        with tempfile.TemporaryDirectory() as tmp:
            n, pid_file = self.run_keep(tmp, claimed=False)
            self.assertEqual(n, 1)
            self.assertTrue(pid_file.exists())
            self.assertFalse(pathlib.Path(f"{pid_file}.claim").exists())

    def test_backs_off_while_another_caller_holds_the_claim(self):
        with tempfile.TemporaryDirectory() as tmp:
            n, pid_file = self.run_keep(tmp, claimed=True)
            self.assertEqual(n, 0)
            self.assertFalse(pid_file.exists())


NV_FIXTURE = ROOT / "app/testdata/nv-0.1.0/ntp-servers"
DEFAULT = ["time.cloudflare.com", "time.google.com"]


class NtpPeersTest(unittest.TestCase):
    """ntpd asks the lease's servers, then the owner's list or the default; never the
    NTP Pool's default zone, whose terms a distributed device may not use."""

    def peers(self, lease=None, own=None, vendor_zone=None):
        with tempfile.TemporaryDirectory() as tmp:
            dhcp = pathlib.Path(tmp, "ntp-servers")
            if lease is not None:
                dhcp.write_text(lease + "\n")
            servers = pathlib.Path(tmp, "nv-ntp-servers")
            if own is not None:
                servers.write_text(own)
            env = dict(os.environ, DHCP_NTP=str(dhcp), NTP_SERVERS=str(servers))
            env.pop("NTP_VENDOR_ZONE", None)
            if vendor_zone is not None:
                env["NTP_VENDOR_ZONE"] = vendor_zone
            args = subprocess.run(["sh", "-c", f". {CLOCK}; ntp_peers"], env=env, check=True,
                                  capture_output=True, text=True).stdout.split()
        self.assertEqual(args[::2], ["-p"] * (len(args) // 2), args)
        return args[1::2]

    def test_lease_servers_come_before_the_default(self):
        self.assertEqual(self.peers(lease="192.168.1.1 10.0.0.5"), ["192.168.1.1", "10.0.0.5", *DEFAULT])

    def test_without_a_lease_server_only_the_default(self):
        self.assertEqual(self.peers(), DEFAULT)

    def test_the_default_never_uses_the_pools_default_zone(self):
        for name in self.peers() + self.peers(vendor_zone="soundtap.pool.ntp.org"):
            self.assertNotRegex(name, r"^([0-3]\.)?pool\.ntp\.org$")

    def test_a_vendor_zone_comes_first_in_the_default(self):
        self.assertEqual(self.peers(vendor_zone="soundtap.pool.ntp.org"),
                         ["0.soundtap.pool.ntp.org", "1.soundtap.pool.ntp.org", "2.soundtap.pool.ntp.org", *DEFAULT])

    def test_the_owners_list_replaces_the_default_after_the_lease(self):
        self.assertEqual(self.peers(lease="192.168.1.1", own=NV_FIXTURE.read_text()),
                         ["192.168.1.1", "ntp.home.arpa", "192.168.1.2"])

    def test_an_owners_list_without_a_name_keeps_the_default(self):
        self.assertEqual(self.peers(own="# nothing yet\n\n"), DEFAULT)

    def test_words_ntpd_would_take_for_options_are_dropped(self):
        got = self.peers(lease="192.168.1.1 -S", own="-S /tmp/x ntp.example $(reboot)\n")
        self.assertEqual(got, ["192.168.1.1", "ntp.example"])


class ClockRetryTest(unittest.TestCase):
    """A failed sync starts one retry loop, however often Wi-Fi rejoins meanwhile."""

    def test_rejoins_do_not_stack_retry_loops(self):
        with tempfile.TemporaryDirectory() as tmp:
            calls = pathlib.Path(tmp, "calls")
            script = (
                f". {CLOCK}\n"
                f"sync_clock_and_save() {{ echo try >>{calls}; return 1; }}\n"
                "for _ in 1 2 3 4 5; do sync_clock_in_background; done\n"
                "sleep 1\n"
            )
            env = dict(os.environ, CLOCK_RETRY_SECONDS="30", CLOCK_RETRY_PID=f"{tmp}/retry.pid")
            # Its own process group: the retry loops outlive the script and are ended here.
            proc = subprocess.Popen(["sh", "-c", script], env=env, start_new_session=True,
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            try:
                proc.wait(timeout=20)
                self.assertEqual(calls.read_text().count("try"), 1)
            finally:
                with contextlib.suppress(ProcessLookupError):
                    os.killpg(proc.pid, signal.SIGTERM)


class SyncClockStepTest(unittest.TestCase):
    """soundtapd runs `init.d/soundtap sync-clock` after a Wi-Fi join: one sync now, and the
    retry loop only when that fails, both in the log."""

    def run_step(self, synced: bool) -> str:
        with tempfile.TemporaryDirectory() as tmp:
            init = InitScript(tmp)
            done = init.run(
                f"sync_clock_and_save() {{ echo sync; {'true' if synced else 'false'}; }}\n"
                "sync_clock_in_background() { echo retry-loop; }\n"
                "run_subcommand sync-clock\n"
                "echo boot went on", check=False)
            self.assertEqual(done.returncode, 0, done.stderr)
            self.assertNotIn("boot went on", done.stdout)
            return init.log.read_text()

    def test_a_good_sync_starts_no_retry_loop(self):
        log = self.run_step(synced=True)
        self.assertIn("sync\n", log)
        self.assertIn("clock synchronized after the Wi-Fi join", log)
        self.assertNotIn("retry-loop", log)

    def test_a_failed_sync_hands_over_to_the_retry_loop(self):
        log = self.run_step(synced=False)
        self.assertIn("WARNING: clock synchronization degraded; retrying", log)
        self.assertIn("retry-loop", log)


if __name__ == "__main__":
    unittest.main()
