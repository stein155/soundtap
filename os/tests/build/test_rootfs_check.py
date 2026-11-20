# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import importlib.util
import os
import subprocess
import struct
import tempfile
import time
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("rootfs_check", ROOT / "os/support/rootfs_check.py")
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)


def write(path, data=b"x", executable=False):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    path.chmod(0o755 if executable else 0o644)


def fixture(root):
    root = Path(root)
    write(root / "bin/busybox", executable=True)
    (root / "sbin").mkdir()
    elf = bytearray(20)
    elf[:6] = b"\x7fELF\x01\x01"
    struct.pack_into("<H", elf, 18, 40)
    for name in check.EXECUTABLES:
        write(root / name, elf if name in ("opt/soundtap/soundtapd", "usr/bin/ffmpeg") else b"#!/bin/sh\n", True)
    write(root / "etc/init.d/gadget-console",
          b"#!/bin/sh\nmount -t proc proc /proc\ncat /proc/cmdline\n", True)
    for name in check.BUSYBOX_APPLETS:
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        os.symlink("/bin/busybox", path)
    write(root / "usr/sbin/wpa_passphrase", b"#!/bin/sh\n", True)
    write(root / "usr/sbin/dropbear", executable=True)
    write(root / "opt/soundtap/ssh.sh", b"#!/bin/sh\ndropbear -B -r key -p 22\n", True)
    write(root / "etc/shadow", b"root::0:0:99999:7:::\n")
    (root / "mnt/nv").mkdir(parents=True)
    write(root / "etc/soundtap-build-epoch", b"1787443200\n")
    write(root / "etc/inittab", b"ttyS3::respawn:/sbin/getty -L ttyS3 115200 vt100\n")
    write(root / "etc/init.d/soundtap",
          b"#!/bin/sh\n/bin/busybox mount -t devtmpfs dev /dev\n2>/dev/null\n/opt/soundtap/ssh.sh\n"
           b"NAND\\.Persistent-Storage\nNAND\\.Persistant-Storage\n"
            b'ubiattach -m "$PERSIST_MTD" -O 2048 -d 1 /dev/ubi_ctrl\n'
           b"read -r build_time </etc/soundtap-build-epoch\n"
          b"SNAPCLIENT_SOUNDCARD=shelby\nSNAPCLIENT_SOUNDCARD=plughw:0,0\n"
          b"SNAPCLIENT_SAMPLEFORMAT='48000:16:*'\nSNAPCLIENT_SAMPLEFORMAT='46875:16:*'\n"
          b'AUDIO_DEV="alsa:$SNAPCLIENT_SOUNDCARD"\nAUDIO_TAP=/run/soundtap/snapcast.pcm\n'
          b'SOUNDTAP_MULTIROOM="${SOUNDTAP_MULTIROOM:-0}"\n'
          b'[ -f /mnt/nv/soundtap/multiroom-enabled ] && SOUNDTAP_MULTIROOM=1\n'
          b'unset AUDIO_PHYSICAL_RATE\ntrue >"/mnt/nv/soundtap/.writable.$$" 2>/dev/null\n'
          b"persistent storage unavailable or read-only; using volatile /mnt/nv\n"
          b"snapserver disabled: settings directory is not writable\n"
          b"SOUNDTAP_SNAPSERVER_CONF=/run/soundtap/snapserver.conf\n"
          b"start_services() {\nstart_root_helper\nsoundtapd_loop ${SOUNDTAPD_ARGS:-} &\n"
          b'${SOUNDTAPD_EXEC:-} "$SOUNDTAPD_BIN" -home /mnt/nv/soundtap "$@" >>"$LOG" 2>&1 &\n'
          b'SOUNDTAPD_EXEC="$ROOT_HELPER exec-soundtapd"\n'
          b'mkdir -p "$ROOT_RUN_DIR" && chgrp "$SOUNDTAPD_USER" "$ROOT_RUN_DIR" && chmod 0750 "$ROOT_RUN_DIR"\n'
          b"}\nmain() {\n\tgrant_soundtapd\n\tstart_services\n}\n"
          b"snapclient --player file:filename=stdout --mixer none\n"
          b"snapclient --player file:filename=/dev/null --mixer none\n"
          b'SNAPCLIENT_SOUNDCARD=shelby\nSNAPCLIENT_SOUNDCARD=plughw:0,0\n'
          b"SNAPCLIENT_SAMPLEFORMAT='48000:16:*'\nSOUNDTAP_SNAPCLIENT_ID=\"soundtap-$snap_mac\"\n"
          b"snapclient --player alsa --soundcard \"$SNAPCLIENT_SOUNDCARD\" --mixer none "
          b"--hostID \"$SOUNDTAP_SNAPCLIENT_ID\" tcp://127.0.0.1:1704\n", True)
    # soundtapd's own user and its helper (ADR 0066), as mkusers and soundtapd.mk leave them.
    write(root / "etc/passwd", b"root:x:0:0::/root:/bin/sh\nsoundtapd:x:1006:1006::/home/soundtapd:/bin/false\n")
    write(root / "etc/group", b"root:x:0:\naudio:x:29:airplay,soundtapd\nsoundtapd:x:1006:\n")
    write(root / "usr/bin/soundtap-root", elf, True)
    write(root / "etc/dbus-1/system.d/soundtapd.conf", b'<busconfig><policy user="soundtapd"></policy></busconfig>\n')
    write(root / "etc/shairport-sync.conf",
          b'general = { service_type = "airplay2"; };\n'
          b'alsa = { output_rate = 48000; output_format = "S16_LE"; output_channels = 2; '
          b'period_size = 1024; buffer_size = 8192; disable_synchronization = "yes"; };\n'
          b'sessioncontrol = { wait_for_completion = "yes"; };\n')
    write(root / "etc/asound.conf.bus",
          b'pcm.busfile { type file slave.pcm "null" file "/run/soundtap/source.pcm" format "raw" }\n'
          b'pcm.!default { type plug slave { pcm "busfile" format S16_LE rate 48000 channels 2 } }\n')
    write(root / "etc/snapserver.conf",
          b'[server]\ndatadir = /run/soundtap/snapserver\nmdns_enabled = false\n[http]\nenabled = false\n'
          b'[tcp-control]\nenabled = true\nbind_to_address = 127.0.0.1\nport = 1705\n'
          b'[tcp-streaming]\nbind_to_address = 0.0.0.0\nport = 1704\n[stream]\n'
          b'source = pipe:///run/soundtap/snapcast.pcm?name=SoundTap&sampleformat=46875:16:2&codec=pcm&chunk_ms=20\n'
          b'buffer = 1000\n')
    return root


class RootfsCheckTest(unittest.TestCase):
    def test_env_fallback_file_is_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            check.validate_no_env_fallback(tmp)
            os.makedirs(os.path.join(tmp, "etc"))
            open(os.path.join(tmp, "etc/u-boot-initial-env"), "w").close()
            with self.assertRaises(SystemExit):
                check.validate_no_env_fallback(tmp)

    def test_st20_rear_usb_is_routed_to_the_scm(self):
        # Stock microbswitch drives microbsel high with a host cable in; low, usb0 sees no VBUS.
        dts = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-st20.dts").read_text()
        hog = dts[dts.index("microbsel-hog {"):]
        self.assertIn("output-high;", hog[:hog.index("};")])

    def test_st20_bco_gets_its_clock(self):
        # Without clkout1 (xdma_event_intr0, MODE3) the BCO never enumerates from a ROM boot.
        dts = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-st20.dts").read_text()
        group = dts[dts.index("power_control_pins:"):]
        self.assertIn("AM33XX_PADCONF(0x9b0, 0x00, MUX_MODE3)", group[:group.index("};")])
        # Linux muxes it seconds after power-on; the SPL has to do it right away.
        spl = (ROOT / "os/bootloader/uboot/uboot-2026.07-spl-board.patch").read_text()
        # A warm reset keeps the BCO powered but stops its clock; the SPL restarts it.
        restart = spl[spl.index("static void spl_bco_restart(void)"):]
        restart = restart[:restart.index("\n+}")]
        self.assertIn("+\twritel(0x3, 0x44e109b0);", restart)
        self.assertLess(restart.index("SETDATAOUT: BCO off"), restart.index("CLEARDATAOUT: BCO on"))
        self.assertIn("+\t\tspl_bco_restart();", spl[spl.index("+\tspl_clocks();\n \ttimer_init();"):])

    def test_spl_watchdog_runs_off_the_rc_oscillator(self):
        # CLK_32KHZ comes from the PER DPLL; a stall with that DPLL down must still reset.
        spl = (ROOT / "os/bootloader/uboot/uboot-2026.07-spl-board.patch").read_text()
        start = spl[spl.index("static void spl_watchdog_start(void)"):]
        self.assertLess(start.index("writel(0x0, 0x44e00538);"), start.index("WSPR start sequence"))

    def test_clock_sync_waits_for_route_retries_and_saves(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            state = tmp / "state"
            state.mkdir()
            write(tmp / "ip", b'''#!/bin/sh
n=$(cat "$STATE/ip" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" >"$STATE/ip"
[ "$n" -ge 2 ] && echo "default via 192.0.2.1 dev wlan0"
''', True)
            write(tmp / "timeout", b'#!/bin/sh\nshift\nexec "$@"\n', True)
            write(tmp / "ntpd", b'''#!/bin/sh
case " $* " in *" -q "*) ;; *) echo "$*" >"$STATE/daemon"; exit 0 ;; esac
n=$(cat "$STATE/ntpd" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" >"$STATE/ntpd"
[ "$n" -lt 2 ] || touch "$STATE/synced"
''', True)
            write(tmp / "date", b'''#!/bin/sh
[ "$1" != +%Y ] || { [ -e "$STATE/synced" ] && echo 2026 || echo 1970; }
''', True)
            write(tmp / "sleep", b'#!/bin/sh\nexit 0\n', True)
            write(tmp / "save-clock", b'#!/bin/sh\nprintf "%s\\n" "$1" >"$STATE/save"\n', True)
            env = os.environ | {
                "PATH": f"{tmp}:{os.environ['PATH']}",
                "STATE": str(state),
                "CLOCK_SAVE_ACTION": str(tmp / "save-clock"),
                "NTPD_PID": str(tmp / "ntpd.pid"),
                "CLOCK_SYNCED": str(state / "clock-synced"),
                "DHCP_NTP": str(state / "no-lease"),
                "NTP_SERVERS": str(state / "no-own-list"),
            }
            helper = ROOT / "os/overlay/opt/soundtap/clock.sh"
            subprocess.run(
                ["sh", "-c", '. "$1"; sync_clock_and_save', "sh", str(helper)],
                check=True, env=env,
            )
            self.assertEqual((state / "ntpd").read_text().strip(), "2")
            self.assertEqual((state / "save").read_text().strip(), "save-clock")
            # The standby clock and the alarm wait for this marker.
            self.assertTrue((state / "clock-synced").exists())
            # After the one-shot sync a long-running ntpd keeps the clock.
            # The redirection creates the file before echo writes it: wait for content,
            # not existence, or a loaded runner reads it empty.
            daemon = ""
            for _ in range(100):
                if (state / "daemon").exists():
                    daemon = (state / "daemon").read_text()
                    if daemon.endswith("\n"):
                        break
                time.sleep(0.1)
            self.assertIn("-n -p time.cloudflare.com", daemon)

    def test_crash_loop_reboots_only_while_u_boot_counts(self):
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        loop = init.split("soundtapd_loop() {", 1)[1].split("\n}\n", 1)[0]
        self.assertIn('[ "$fails" -ge 5 ] && [ -n "$CRASH_REBOOT" ]', loop)
        self.assertIn("reboot -f", loop)
        # Set where the count is below the point where both slots have had their tries,
        # so that a speaker with two bad slots stays up to be looked at.
        armed = init.split('if [ "$count" -le 9 ]; then', 1)[1].split("else", 1)[0]
        self.assertIn("CRASH_REBOOT=1", armed)

    def test_clock_logic_lives_in_clock_sh(self):
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        self.assertIn(". /opt/soundtap/clock.sh", init)
        self.assertNotIn("ntpd -n -q", init)

    def test_rejects_an_image_that_still_carries_wifi_sh(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "opt/soundtap/wifi.sh", b"#!/bin/sh\n", True)
            with self.assertRaisesRegex(SystemExit, "soundtapd owns Wi-Fi"):
                check.validate(root)

    def test_clock_retries_after_dhcp_until_ntp_succeeds(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            state = tmp / "state"
            state.mkdir()
            write(tmp / "ip", b'#!/bin/sh\necho "default via 192.0.2.1 dev wlan0"\n', True)
            write(tmp / "timeout", b'#!/bin/sh\nshift\nexec "$@"\n', True)
            write(tmp / "ntpd", b'''#!/bin/sh
case " $* " in *" -q "*) ;; *) exit 0 ;; esac
n=$(cat "$STATE/ntpd" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" >"$STATE/ntpd"
[ "$n" -lt 4 ] || touch "$STATE/synced"
[ "$n" -ge 4 ]
''', True)
            write(tmp / "date", b'''#!/bin/sh
[ "$1" != +%Y ] || { [ -e "$STATE/synced" ] && echo 2026 || echo 1970; }
''', True)
            write(tmp / "sleep", b'#!/bin/sh\nexit 0\n', True)
            write(tmp / "save-clock", b'#!/bin/sh\ntouch "$STATE/save"\n', True)
            env = os.environ | {
                "PATH": f"{tmp}:{os.environ['PATH']}",
                "STATE": str(state),
                "CLOCK_SAVE_ACTION": str(tmp / "save-clock"),
                "NTPD_PID": str(tmp / "ntpd.pid"),
            }
            helper = ROOT / "os/overlay/opt/soundtap/clock.sh"
            subprocess.run(
                ["sh", "-c", '. "$1"; sync_clock_in_background; while [ ! -e "$STATE/save" ]; do sleep 1; done', "sh", str(helper)],
                check=True, env=env, timeout=5,
            )
            self.assertEqual((state / "ntpd").read_text().strip(), "4")

    def test_rejects_image_without_mnt_nv(self):
        with tempfile.TemporaryDirectory() as tmp:
            fixture(tmp)
            (Path(tmp) / "mnt/nv").rmdir()
            with self.assertRaisesRegex(SystemExit, "mnt/nv"):
                check.validate(tmp)

    def test_gadget_console_mounts_proc_before_reading_cmdline(self):
        script = (ROOT / "os/overlay/etc/init.d/gadget-console").read_text()
        self.assertLess(script.index("mount -t proc"), script.index("/proc/cmdline"))

    def test_rejects_gadget_console_reading_cmdline_first(self):
        with tempfile.TemporaryDirectory() as tmp:
            write(Path(tmp) / "etc/init.d/gadget-console",
                  b"#!/bin/sh\ncat /proc/cmdline\nmount -t proc proc /proc\n", True)
            with self.assertRaisesRegex(SystemExit, "before mounting proc"):
                check.validate_gadget_console(tmp)

    def test_rejects_gadget_console_without_rndis(self):
        script = (ROOT / "os/overlay/etc/init.d/gadget-console").read_text()
        for required in ('functions/rndis.usb0', 'ln -sf "$G/functions/rndis.usb0"',
                         'ip addr add 192.168.7.2/24 dev usb0', 'ip link set usb0 up'):
            self.assertIn(required, script)

    def test_spotify_new_pause_resets_grace_period(self):
        script = ROOT / "os/overlay/opt/soundtap/spotify-source.sh"
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            calls = tmp / "calls"
            write(tmp / "wget", b'#!/bin/sh\nprintf "%s\\n" "$*" >>"$CALLS"\n', True)
            env = os.environ | {
                "PATH": f"{tmp}:{os.environ['PATH']}",
                "CALLS": str(calls),
                # 4 s grace, not 1 s. The assertions below need to land strictly
                # between the first pause expiry and the second, and at 1 s the margins were
                # 0.15 s and 0.25 s, which flakes whenever this box is busy building. At 4 s
                # every margin is a full second. Costs ~7 s of wall clock for determinism.
                "PAUSE_GRACE": "4",
                "PAUSE_STAMP": str(tmp / "spotify-paused"),
                "PLAYER_EVENT": "sink",
                "SINK_STATUS": "temporarily_closed",
            }
            subprocess.run(["sh", script], check=True, env=env)   # first pause expires at t=4
            subprocess.run(["sleep", "2"], check=True)
            subprocess.run(["sh", script], check=True, env=env)   # second pause expires at t=6
            subprocess.run(["sleep", "3"], check=True)            # t=5: after the first, before the second
            self.assertFalse(calls.exists(), "first pause ended newer pause early")
            subprocess.run(["sleep", "2"], check=True)            # t=7: after the second
            self.assertEqual(len(calls.read_text().splitlines()), 1)

    def test_source_build_includes_snapcast_packages_and_overlay(self):
        runtime = (ROOT / "os/package/soundtap-runtime/Config.in").read_text()
        self.assertIn("select BR2_PACKAGE_SOUNDTAP_SNAPCAST", runtime)
        snapcast = (ROOT / "os/package/soundtap-snapcast/soundtap-snapcast.mk").read_text()
        self.assertIn("rm -rf $(TARGET_DIR)/usr/share/snapserver/snapweb", snapcast)
        config = (ROOT / "os/overlay/etc/snapserver.conf").read_text()
        self.assertIn("datadir = /run/soundtap/snapserver", config)
        self.assertIn("mdns_enabled = false", config)  # its Avahi poll woke it 20 times a second
        self.assertIn("[http]\nenabled = false", config)
        self.assertIn("codec=pcm&chunk_ms=20", config)
        self.assertIn("buffer = 1000", config)

    def test_source_build_includes_bluetooth_runtime(self):
        runtime = (ROOT / "os/package/soundtap-runtime/Config.in").read_text()
        self.assertIn("select BR2_PACKAGE_BLUEZ5_UTILS", runtime)
        self.assertIn("select BR2_PACKAGE_BLUEZ_ALSA", runtime)
        bt = (ROOT / "os/overlay/opt/soundtap/bt.sh").read_text()
        self.assertIn("${BT_UARTS:-/dev/ttyO2}", bt)
        self.assertIn('[ "${MODEL:-st10}" = st10 ]', bt)
        self.assertNotIn("/dev/ttyO1", bt)
        self.assertNotIn("/dev/ttyS1", bt)

    def test_wla_hardware_defaults_are_fail_closed(self):
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        self.assertIn("binky) PROC_MODEL=wla", init)
        self.assertIn('stock_modules="snd-binky wakeup-source"', init)
        self.assertIn('elif [ "${MODEL:-st10}" = "wla" ]; then', init)

    def test_the_init_script_starts_soundtapd_as_its_own_user(self):
        # The real script, not the fixture's lines: soundtap-root first, soundtapd through it.
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "etc/init.d/soundtap").write_bytes((ROOT / "os/overlay/etc/init.d/soundtap").read_bytes())
            check.validate_soundtapd_user(root)

    def test_the_accounts_come_from_the_filesystems_own_tree(self):
        # output/target has no users from users.txt; only the copy mkfs packs does
        # (images/rootfs-tree). image-check passes that tree as accounts.
        with tempfile.TemporaryDirectory() as tmp, tempfile.TemporaryDirectory() as fs:
            root = fixture(tmp)
            for name in ("etc/passwd", "etc/group"):
                (Path(fs) / name).parent.mkdir(parents=True, exist_ok=True)
                (Path(fs) / name).write_bytes((root / name).read_bytes())
            (root / "etc/passwd").write_text("root:x:0:0::/root:/bin/sh\n")
            with self.assertRaises(SystemExit):
                check.validate_soundtapd_user(root)
            check.validate_soundtapd_user(root, accounts=fs)

    def test_soundtapd_never_runs_as_root(self):
        cases = {
            "no soundtapd user": lambda r: (r / "etc/passwd").write_text("root:x:0:0::/root:/bin/sh\n"),
            "apart from root": lambda r: (r / "etc/passwd").write_text("soundtapd:x:0:0::/:/bin/false\n"),
            "group audio": lambda r: (r / "etc/group").write_text("audio:x:29:\nsoundtapd:x:1006:\n"),
            "soundtap-root is not": lambda r: (r / "usr/bin/soundtap-root").write_bytes(b"#!/bin/sh\n"),
            "D-Bus policy": lambda r: (r / "etc/dbus-1/system.d/soundtapd.conf").unlink(),
            "other than through": lambda r: (r / "etc/init.d/soundtap").write_bytes(
                (r / "etc/init.d/soundtap").read_bytes() + b'"$SOUNDTAPD_BIN" -home /mnt/nv/soundtap &\n'),
            "not started before": lambda r: (r / "etc/init.d/soundtap").write_bytes(
                (r / "etc/init.d/soundtap").read_bytes().replace(b"start_root_helper\nsoundtapd_loop", b"soundtapd_loop")),
            "0750": lambda r: (r / "etc/init.d/soundtap").write_bytes(
                (r / "etc/init.d/soundtap").read_bytes().replace(b"chmod 0750", b"chmod 0755")),
            "before it starts": lambda r: (r / "etc/init.d/soundtap").write_bytes(
                (r / "etc/init.d/soundtap").read_bytes().replace(b"\tgrant_soundtapd\n", b"")),
        }
        for message, damage in cases.items():
            with self.subTest(message), tempfile.TemporaryDirectory() as tmp:
                root = fixture(tmp)
                damage(root)
                with self.assertRaisesRegex(SystemExit, message):
                    check.validate(root)

    def test_valid_contract(self):
        with tempfile.TemporaryDirectory() as tmp:
            check.validate(fixture(tmp))

    def test_rejects_a_special_builtin_writability_probe(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            init = root / "etc/init.d/soundtap"
            init.write_bytes(init.read_bytes().replace(
                b'true >"/mnt/nv/soundtap/.writable.$$"',
                b': >"/mnt/nv/soundtap/.writable.$$"'))
            with self.assertRaisesRegex(SystemExit, "probes /mnt/nv with"):
                check.validate(root)

    def test_follows_absolute_and_relative_links_inside_the_root(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            os.remove(root / "usr/sbin/udhcpd")
            os.symlink("../../bin/busybox", root / "usr/sbin/udhcpd")
            check.validate(root)
            os.remove(root / "usr/sbin/udhcpd")
            os.symlink("/bin/other", root / "usr/sbin/udhcpd")
            with self.assertRaises(SystemExit):
                check.validate(root)

    def test_rejects_kernel_modules(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "lib/modules/6.12/foo.ko")
            with self.assertRaises(SystemExit):
                check.validate(root)

    def test_accepts_the_st300_cpsw_modules(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "lib/modules/6.12/kernel/drivers/net/ethernet/ti/ti_cpsw_new.ko")
            write(root / "lib/modules/6.12/kernel/drivers/phy/ti/phy-gmii-sel.ko")
            check.validate(root)

    def test_rejects_a_rootfs_without_ssh(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "usr/sbin/dropbear").unlink()
            with self.assertRaisesRegex(SystemExit, "no dropbear"):
                check.validate(root)

    # A blank password only lets anyone in if dropbear is told to accept one; the
    # two have to be configured together or the speaker is quietly unreachable.
    def test_rejects_ssh_that_refuses_the_blank_password(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            ssh = root / "opt/soundtap/ssh.sh"
            ssh.write_text(ssh.read_text().replace("dropbear -B", "dropbear"))
            with self.assertRaisesRegex(SystemExit, "blank-password"):
                check.validate(root)

    def test_rejects_a_locked_root_account(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "etc/shadow", b"root:!:19000:0:99999:7:::\n")
            with self.assertRaisesRegex(SystemExit, "blank"):
                check.validate(root)

    def test_production_rejects_alpha_ssh(self):
        with tempfile.TemporaryDirectory() as tmp, self.assertRaisesRegex(SystemExit, "production rootfs contains dropbear"):
            check.validate_ssh_policy(fixture(tmp), profile="production")

    def production(self, tmp):
        root = fixture(tmp)
        (root / "usr/sbin/dropbear").unlink()
        write(root / "etc/shadow", b"root:!:19000:0:99999:7:::\n")
        (root / "opt/soundtap/ssh.sh").unlink()
        write(root / "etc/soundtap/profile", b"production\n")
        write(root / "usr/sbin/nft", b"#!/bin/sh\n")
        os.chmod(root / "usr/sbin/nft", 0o755)
        write(root / "etc/soundtap/firewall.nft", (ROOT / "os/board/rootfs/firewall.nft").read_bytes())
        write(root / "etc/inittab", b"::sysinit:/usr/sbin/nft -f /etc/soundtap/firewall.nft\n"
              b"::sysinit:/etc/init.d/gadget-console\n::sysinit:/etc/init.d/soundtap\n")
        return root

    def test_production_refuses_a_package_that_may_not_be_redistributed(self):
        config = (ROOT / "os/configs/soundtap_defconfig").read_text()
        config += (ROOT / "os/configs/production.fragment").read_text()
        check.validate_redistributable(config)
        with tempfile.TemporaryDirectory() as tmp:
            write(Path(tmp) / "os/package/closed-thing/closed-thing.mk",
                  b"CLOSED_THING_VERSION = 1\nCLOSED_THING_REDISTRIBUTE = NO\n")
            check.validate_redistributable(config, root=Path(tmp))
            with self.assertRaisesRegex(SystemExit, "closed-thing"):
                check.validate_redistributable(config + "BR2_PACKAGE_CLOSED_THING=y\n", root=Path(tmp))

    def test_production_accepts_no_ssh_and_locked_root(self):
        with tempfile.TemporaryDirectory() as tmp:
            check.validate_ssh_policy(self.production(tmp), profile="production")

    # Not the server alone: nothing SSH, nor a script or filter rule that would bring it back (ADR 0119).
    def test_production_rejects_any_ssh_leftover(self):
        leftovers = {
            "usr/bin/dbclient": b"", "usr/bin/dropbearkey": b"", "root/.ssh/authorized_keys": b"",
            "etc/avahi/services/ssh.service": b"",
            "opt/soundtap/keys.sh": b"#!/bin/sh\ndropbearkey -t ed25519 -f /mnt/nv/k\n",
        }
        for path, body in leftovers.items():
            with self.subTest(path), tempfile.TemporaryDirectory() as tmp:
                root = self.production(tmp)
                write(root / path, body)
                with self.assertRaisesRegex(SystemExit, "production"):
                    check.validate_ssh_policy(root, profile="production")
        with tempfile.TemporaryDirectory() as tmp:
            root = self.production(tmp)
            rules = root / "etc/soundtap/firewall.nft"
            rules.write_text(rules.read_text().replace("{ 80, 7000", "{ 22, 80, 7000"))
            with self.assertRaisesRegex(SystemExit, "opens port 22"):
                check.validate_ssh_policy(root, profile="production")

    # The input filter has to be in place before gadget-console brings up usb0 (ADR 0066).
    def test_production_loads_the_input_filter_first(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = self.production(tmp)
            write(root / "etc/inittab", b"::sysinit:/etc/init.d/gadget-console\n"
                  b"::sysinit:/usr/sbin/nft -f /etc/soundtap/firewall.nft\n")
            with self.assertRaisesRegex(SystemExit, "input filter first"):
                check.validate_ssh_policy(root, profile="production")
            (root / "etc/soundtap/firewall.nft").unlink()
            with self.assertRaisesRegex(SystemExit, "default-deny"):
                check.validate_ssh_policy(root, profile="production")

    def test_production_rejects_the_gadget_shell(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "usr/sbin/dropbear").unlink()
            (root / "opt/soundtap/ssh.sh").unlink()
            write(root / "etc/shadow", b"root:!:19000:0:99999:7:::\n")
            write(root / "etc/soundtap/profile", b"production\n")
            write(root / "etc/inittab", b"ttyGS0::respawn:/bin/sh\n")
            with self.assertRaisesRegex(SystemExit, "gadget a root shell"):
                check.validate_ssh_policy(root, profile="production")

    def test_rejects_provisioned_wifi_in_production(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "etc/soundtap-wifi.conf", b"SSID_B64=dGVzdA==\nPSK_B64=dGVzdA==\n")
            with self.assertRaisesRegex(SystemExit, "build-time Wi-Fi profile"):
                check.validate(root)


    def test_snapserver_falls_back_from_read_only_storage_and_bounds_restarts(self):
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        self.assertIn('ubiattach -m "$PERSIST_MTD" -O 2048 -d 1 /dev/ubi_ctrl', init)
        self.assertNotIn("ubiattach /dev/ubi_ctrl -m 10", init)
        self.assertIn("NAND\\.Persistent-Storage", init)
        self.assertIn("NAND\\.Persistant-Storage", init)
        # `true`, not `:`. A failed redirection on a special built-in exits a
        # non-interactive shell, so `: >` on a read-only /mnt/nv killed init
        # rather than taking the fallback branch this test is about.
        self.assertIn('true >"/mnt/nv/soundtap/.writable.$$"', init)
        self.assertNotIn(': >"/mnt/nv/soundtap/.writable.$$"', init)
        self.assertIn("persistent storage unavailable or read-only; using volatile /mnt/nv", init)
        self.assertIn(": >/run/soundtap/persistent-storage", init)
        self.assertIn('[ -e "$RUN_DIR/persistent-storage" ] || exit 0', init)
        self.assertIn("snapserver disabled: settings directory is not writable", init)
        self.assertIn("read -r build_time </etc/soundtap-build-epoch", init)
        # soundtapd restarts snapserver (ADR 0121); the init script only hands it over.
        self.assertIn("SOUNDTAP_SNAPSERVER_CONF=/run/soundtap/snapserver.conf", init)
        self.assertNotIn("supervise snapserver", init)

    def test_rejects_airplay_without_forced_ap2(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "etc/shairport-sync.conf").write_text('general = { service_type = "auto"; };\n')
            with self.assertRaisesRegex(SystemExit, "AirPlay configuration contract"):
                check.validate(root)

    def test_rejects_unbounded_airplay_fifo_buffer(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            config = root / "etc/shairport-sync.conf"
            config.write_text(config.read_text().replace("buffer_size = 8192;", ""))
            with self.assertRaisesRegex(SystemExit, "buffer_size = 8192"):
                check.validate(root)


if __name__ == "__main__":
    unittest.main()
