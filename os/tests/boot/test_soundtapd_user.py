# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""soundtapd runs as its own user (ADR 0066): what init hands it, and how it starts.

grant_soundtapd gives the user soundtapd its device nodes, the shared runtime directories and,
through migrate_nv_owner, the state on /mnt/nv that every earlier build left root's. The test
runs as an ordinary user, so chgrp, chmod and chown are recorded stubs; find is the real one,
which is what decides which files a boot hands over. SOUNDTAPD_USER stands in for the user:
the test's own name where the files are already that user's (every boot after the first), root
where none are (the first boot after the update).
"""
import os
import socket
import tempfile
import unittest
from pathlib import Path

from boot.init_script import InitScript


def owner_calls(init):
    return [c for c in init.calls() if c.split()[0] in ("chown", "chgrp", "chmod")]


class GrantSoundtapd(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.init = InitScript(self.dir.name)
        for cmd in ("chown", "chgrp", "chmod"):
            self.init.stub(cmd)

    def tearDown(self):
        self.dir.cleanup()

    def node(self, path):
        p = Path(path)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text("")
        return p

    def test_without_the_user_nothing_changes(self):
        self.init.stub("id", "exit 1")
        r = self.init.run("soundtapd_user\ngrant_soundtapd\necho \"user=[$SOUNDTAPD_USER]\"")
        self.assertIn("user=[]", r.stdout)
        self.assertEqual(owner_calls(self.init), [])
        self.assertIn("soundtapd runs as root", self.init.log.read_text())

    def test_devices_and_directories(self):
        init = self.init
        dev, sysfs = init.dev, init.sys
        for n in ("watchdog", "lirc0", "input/event0", "spidev1.0", "ttyS0", "snd/pcmC0D0p", "mtd0", "mtd8"):
            self.node(dev / n)
        (dev / "snd/by-path").mkdir()
        self.node(sysfs / "class/leds/st10:white/brightness")
        self.node(sysfs / "class/gpio/export")
        self.node(sysfs / "class/gpio/gpio72/value")
        self.node(sysfs / "class/gpio/gpio72/direction")
        self.node(sysfs / "module/firmware_class/parameters/path")
        os.mkfifo(init.run_dir / "snapcast.pcm")
        os.mkfifo(init.run_dir / "log.fifo")
        init.run("SOUNDTAPD_USER=soundtapd\ngrant_soundtapd")
        calls = owner_calls(init)
        for n in ("watchdog", "lirc0", "input/event0", "spidev1.0", "ttyS0", "snd/pcmC0D0p"):
            self.assertIn(f"chgrp soundtapd {dev / n}", calls)
            self.assertIn(f"chmod 0660 {dev / n}", calls)
        for n in ("mtd0", "mtd8"):  # NAND is read-only for soundtapd: writes go to soundtap-root
            self.assertIn(f"chmod 0640 {dev / n}", calls)
        self.assertFalse(any(str(dev / "snd/by-path") in c for c in calls), "a directory got a device's mode")
        self.assertIn(f"chmod 0664 {sysfs}/class/leds/st10:white/brightness", calls)
        self.assertIn(f"chmod 0220 {sysfs}/class/gpio/export", calls)
        self.assertIn(f"chown soundtapd:soundtapd {sysfs}/class/gpio/gpio72/value", calls)
        # Shared and sticky: soundtapd adds its own entries, root's stay root's.
        self.assertIn(f"chmod 1775 {init.var_run}", calls)
        self.assertIn(f"chmod 1775 {init.run_dir}", calls)
        self.assertIn(f"chmod 0750 {init.root_run}", calls)
        for f in ("ntp-servers", "lan-net.wlan0"):
            self.assertTrue((init.run_dir / f).is_file())
            self.assertIn(f"chmod 0664 {init.run_dir / f}", calls)
        self.assertIn(f"chmod 0664 {init.tmp}/resolv.conf", calls)
        self.assertIn(f"chown -R soundtapd:soundtapd {init.app}", calls)
        # The tap stays root's: soundtapd writes it through its group (protected_fifos=2).
        self.assertIn(f"chgrp soundtapd {init.run_dir}/snapcast.pcm", calls)
        self.assertIn(f"chmod 0664 {init.run_dir}/snapcast.pcm", calls)
        # udhcpc's script logs into the FIFO as soundtapd; it stays root's for the same reason.
        self.assertIn(f"chgrp soundtapd {init.run_dir}/log.fifo", calls)
        self.assertIn(f"chmod 0620 {init.run_dir}/log.fifo", calls)
        self.assertFalse(any(c.startswith("chown") and c.endswith("snapcast.pcm") for c in calls),
                         "the tap went to soundtapd: init's exec 6<> of it then fails and ends the boot")
        self.assertIn(f"chown spotify:audio {init.run_dir}/spotify", calls)
        self.assertIn(f"chmod 2770 {init.run_dir}/snapserver", calls)
        self.assertEqual((sysfs / "module/firmware_class/parameters/path").read_text(), f"{init.run_dir}/firmware\n")
        self.assertFalse(init.log.exists() and "ERROR" in init.log.read_text(), init.log.exists() and init.log.read_text())

    def test_a_link_in_place_of_a_shared_file_is_not_followed(self):
        init = self.init
        target = Path(self.dir.name) / "elsewhere"
        target.write_text("root's\n")
        (init.run_dir / "ntp-servers").symlink_to(target)
        init.run("SOUNDTAPD_USER=soundtapd\nshared_file \"$RUN_DIR/ntp-servers\"")
        self.assertFalse((init.run_dir / "ntp-servers").is_symlink())
        self.assertEqual(target.read_text(), "root's\n")
        self.assertNotIn(f"chgrp soundtapd {target}", owner_calls(init))


class MigrateNVOwner(unittest.TestCase):
    """The first boot after the update hands soundtapd its state; later boots write nothing."""

    def migrate(self, user):
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            for cmd in ("chown", "chgrp", "chmod"):
                init.stub(cmd)
            (init.run_dir / "persistent-storage").touch()
            home = init.nv / "soundtap"
            (home / "codec").mkdir(parents=True)
            for f in ("presets.json", "presets.json.settings", "codec/rhino_sm2_normal.cfg", "current-boot.log", "previous-boot.1.log"):
                (home / f).write_text("{}")
            for f in ("wifi.conf", "wifi.conf.known", "soundtap-clock"):
                (init.nv / f).write_text("x\n")
            init.run(f"SOUNDTAPD_USER={user}\nmigrate_nv_owner")
            log = init.log.read_text() if init.log.exists() else ""
            return [c.replace(d, "") for c in owner_calls(init)], log

    def test_first_boot_hands_everything_over(self):
        calls, log = self.migrate("root")  # none of the test's files is root's: an old build's
        chowned = " ".join(c for c in calls if c.startswith("chown -h root:root"))
        for f in ("soundtap", "soundtap/presets.json", "soundtap/presets.json.settings", "soundtap/codec/rhino_sm2_normal.cfg"):
            self.assertIn(f"/mnt/nv/{f}", chowned.split())
        self.assertIn("chown root:root /mnt/nv/wifi.conf", calls)
        self.assertIn("chown root:root /mnt/nv/wifi.conf.known", calls)
        self.assertFalse(any("soundtap-clock" in c for c in calls), "root's clock file handed over")
        self.assertFalse(any("boot.log" in c or "boot.1.log" in c for c in calls), "init's boot logs handed over")
        self.assertIn("chmod 1775 /mnt/nv", calls)
        self.assertNotIn("not all", log)  # the test machine has no spotify user: that tree is not checked here

    def test_later_boots_write_nothing_in_its_home(self):
        calls, _ = self.migrate(str(os.getuid()))  # every file already the user's (find takes a uid)
        self.assertFalse([c for c in calls if c.startswith("chown -h") and "/soundtap" in c], calls)

    def test_without_persistent_storage_nothing_is_touched(self):
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            for cmd in ("chown", "chgrp", "chmod"):
                init.stub(cmd)
            init.run("SOUNDTAPD_USER=soundtapd\nmigrate_nv_owner")
            self.assertEqual(owner_calls(init), [])


class StartsAsItsOwnUser(unittest.TestCase):
    def test_network_ready_is_soundtapds(self):
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            init.stub("chown")
            init.run(f'SOUNDTAPD_USER=soundtapd NETWORK_READY="{init.run_dir}/network-ready"\nmark_network_ready')
            self.assertTrue((init.run_dir / "network-ready").exists())
            self.assertIn(f"chown soundtapd {init.run_dir}/network-ready", init.calls())

    def test_network_ready_is_not_reopened_once_soundtapds(self):
        # protected_regular=2 refuses root's O_CREAT open of soundtapd's marker in the sticky
        # /run/soundtap, and a failed redirection on ":" ends the shell: init leaves it be.
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            init.stub("chown")
            marker = init.run_dir / "network-ready"
            marker.write_text("soundtapd's\n")
            init.run(f'SOUNDTAPD_USER=soundtapd NETWORK_READY="{marker}"\nmark_network_ready')
            self.assertEqual(marker.read_text(), "soundtapd's\n")

    def test_soundtapd_starts_through_the_helper(self):
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            helper = init.bin / "soundtap-root"
            # The helper's serve loop ends the test's background job; the socket is a real one.
            sock = socket.socket(socket.AF_UNIX)
            sock.bind(str(init.root_run / "sock"))
            try:
                init.stub("soundtap-root", 'case "$1" in serve) kill $PPID;; exec-soundtapd) shift; echo "as soundtapd: $*"; kill $PPID;; esac')
                init.stub("sleep")
                init.stub("wget")
                init.stub("cut", "echo 0")
                r = init.run(f'SOUNDTAPD_USER=soundtapd ROOT_HELPER="{helper}" SOUNDTAPD_BIN=/run/audio-bin/app/soundtapd\n'
                             f'SOUNDTAPD_OVERRIDE="{d}/none" WATCHDOG_DISARM="{init.run_dir}/booted" CRASH_REBOOT=\n'
                             'start_root_helper\necho "exec=[$SOUNDTAPD_EXEC]"\nsoundtapd_loop -no-audio', check=False)
            finally:
                sock.close()
            self.assertIn(f"exec=[{helper} exec-soundtapd]", r.stdout)
            self.assertIn("soundtap-root serve", init.calls())
            self.assertIn("soundtap-root exec-soundtapd /run/audio-bin/app/soundtapd -home /mnt/nv/soundtap -no-audio", init.calls())

    def test_without_the_user_soundtapd_starts_directly(self):
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            r = init.run(f'SOUNDTAPD_USER= ROOT_HELPER="{init.bin}/soundtap-root"\nstart_root_helper\necho "exec=[$SOUNDTAPD_EXEC]"')
            self.assertIn("exec=[]", r.stdout)


if __name__ == "__main__":
    unittest.main()
