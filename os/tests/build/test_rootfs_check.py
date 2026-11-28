# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Tests for rootfs_check.py, the contract every root filesystem image meets, and for the boot scripts it checks."""

import importlib.util
import os
import struct
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("rootfs_check", ROOT / "os/support/rootfs_check.py")
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)


def write(path: Path, data: bytes | bytearray = b"x", *, executable: bool = False) -> None:
    """Write data to path, creating its directory, with mode 0755 or 0644."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    path.chmod(0o755 if executable else 0o644)


def fixture(root: str | Path) -> Path:
    """Fill root with a minimal alpha root filesystem that passes the check, and return it."""
    root = Path(root)
    write(root / "bin/busybox", executable=True)
    (root / "sbin").mkdir()
    elf = bytearray(20)
    elf[:6] = b"\x7fELF\x01\x01"
    struct.pack_into("<H", elf, 18, 40)
    for name in check.EXECUTABLES:
        write(root / name, elf if name in ("opt/soundtap/soundtapd", "usr/bin/ffmpeg") else b"#!/bin/sh\n", executable=True)
    # Snapcast built with FLAC names libFLAC in its dynamic section (ADR 0013).
    for name in ("usr/bin/snapserver", "usr/bin/snapclient"):
        write(root / name, bytes(elf) + b"\0libFLAC.so.12\0", executable=True)
    write(root / "usr/lib/libFLAC.so.12", elf)
    write(root / "etc/init.d/gadget-console",
          b"#!/bin/sh\nmount -t proc proc /proc\ncat /proc/cmdline\n", executable=True)
    for name in check.BUSYBOX_APPLETS:
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.symlink_to("/bin/busybox")
    write(root / "usr/sbin/wpa_passphrase", b"#!/bin/sh\n", executable=True)
    write(root / "usr/sbin/dropbear", executable=True)
    write(root / "opt/soundtap/ssh.sh", b"#!/bin/sh\ndropbear -B -r key -p 22\n", executable=True)
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
          b'snapclient --player alsa --soundcard "$SNAPCLIENT_SOUNDCARD" --mixer none '
          b'--hostID "$SOUNDTAP_SNAPCLIENT_ID" tcp://127.0.0.1:1704\n', executable=True)
    # soundtapd's own user and its helper (ADR 0066), as mkusers and soundtapd.mk leave them.
    write(root / "etc/passwd", b"root:x:0:0::/root:/bin/sh\nsoundtapd:x:1006:1006::/home/soundtapd:/bin/false\n")
    write(root / "etc/group", b"root:x:0:\naudio:x:29:airplay,soundtapd\nsoundtapd:x:1006:\n")
    write(root / "usr/bin/soundtap-root", elf, executable=True)
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
          b"[server]\ndatadir = /run/soundtap/snapserver\nmdns_enabled = false\n[http]\nenabled = false\n"
          b"[tcp-control]\nenabled = true\nbind_to_address = 127.0.0.1\nport = 1705\n"
          b"[tcp-streaming]\nbind_to_address = 0.0.0.0\nport = 1704\n[stream]\n"
          b"source = pipe:///run/soundtap/snapcast.pcm?name=SoundTap&sampleformat=46875:16:2&codec=flac:1&chunk_ms=20\n"
          b"buffer = 1000\n")
    return root


class RootfsCheckTest(unittest.TestCase):
    """rootfs_check.py accepts a good image and refuses each broken rule."""

    def test_env_fallback_file_is_refused(self) -> None:
        """An image carrying a U-Boot environment fallback file is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            check.validate_no_env_fallback(tmp)
            Path(tmp, "etc").mkdir(parents=True)
            Path(tmp, "etc/u-boot-initial-env").open("w").close()
            with self.assertRaises(SystemExit):
                check.validate_no_env_fallback(tmp)

    def test_st20_rear_usb_is_routed_to_the_scm(self) -> None:
        """The ST20's rear USB is routed to the SCM.

        Stock microbswitch drives microbsel high with a host cable in; low, usb0 sees no VBUS.
        """
        dts = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-st20.dts").read_text()
        hog = dts[dts.index("microbsel-hog {"):]
        self.assertIn("output-high;", hog[:hog.index("};")])

    def test_st20_bco_gets_its_clock(self) -> None:
        """The ST20's BCO gets its clock from the device tree and, at once, from the SPL.

        Without clkout1 (xdma_event_intr0, MODE3) the BCO never enumerates from a ROM boot.
        """
        dts = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-st20.dts").read_text()
        group = dts[dts.index("power_control_pins:"):]
        self.assertIn("AM33XX_PADCONF(0x9b0, 0x00, MUX_MODE3)", group[:group.index("};")])
        # Linux muxes it seconds after power-on; the SPL has to do it right away.
        spl = (ROOT / "os/bootloader/uboot/uboot-2026.07-spl-board.patch").read_text()
        # A warm reset keeps the BCO powered but stops its clock; the SPL restarts it.
        off = spl[spl.index("static void spl_bco_off(void)"):]
        off = off[:off.index("\n+}")]
        self.assertIn("+\twritel(0x3, 0x44e109b0);", off)
        self.assertIn("SETDATAOUT: BCO off", off)
        self.assertNotIn("BCO on", off)
        restart = spl[spl.index("static void spl_bco_restart(void)"):]
        restart = restart[:restart.index("\n+}")]
        self.assertIn("+\tspl_bco_off();", restart)
        # An SPL without the USB ask still waits out the second before switching it on.
        alone = restart[restart.index("+#if !defined(CONFIG_SPL_USB_ETHER)"):restart.index("+#endif")]
        self.assertLess(alone.index("+\tmdelay(1000);"), alone.index("CLEARDATAOUT: BCO on"))
        self.assertIn("+\t\tspl_bco_restart();", spl[spl.index("+\tspl_clocks();\n \ttimer_init();"):])

    def test_st20_bco_second_overlaps_the_usb_ask(self) -> None:
        """The ST20's SPL keeps the BCO off at least 1 s, alongside the USB ask (ADR 0131).

        The supply goes off in s_init and back on after the micro-B port's ask, before NAND or a
        USB image is booted, and never before 1 s from a timestamp taken after the off.
        """
        spl = (ROOT / "os/bootloader/uboot/uboot-2026.07-spl-board.patch").read_text()
        on = spl[spl.index("+void spl_bco_on(ulong since)"):]
        on = on[:on.index("\n+}")]
        self.assertLess(on.index("+\twhile (get_timer(since) < 1000)"), on.index("CLEARDATAOUT: BCO on"))
        self.assertIn("+#if defined(CONFIG_SPL_USB_ETHER)\n+/*", spl[:spl.index("+void spl_bco_on(ulong since)")][-400:])
        ask = spl[spl.index("+\tif (spl_board_is_st20()) {\n+\t\tstruct spl_boot_device bootdev"):]
        ask = ask[:ask.index("+\t}\n")]
        self.assertLess(ask.index("+\t\tulong bco_off = get_timer(0);"),
                        ask.index("+\t\tret = spl_net_load_image_usb(&spl_image, &bootdev);"))
        self.assertLess(ask.index("+\t\tret = spl_net_load_image_usb(&spl_image, &bootdev);"),
                        ask.index("+\t\tspl_bco_on(bco_off);"))
        # The USB connect timeout is U-Boot's own; the SPL does not shorten it.
        self.assertNotIn("cdc_connect_timeout", spl)

    def test_spl_watchdog_runs_off_the_rc_oscillator(self) -> None:
        """The SPL's watchdog runs off the RC oscillator.

        CLK_32KHZ comes from the PER DPLL; a stall with that DPLL down must still reset.
        """
        spl = (ROOT / "os/bootloader/uboot/uboot-2026.07-spl-board.patch").read_text()
        start = spl[spl.index("static void spl_watchdog_start(void)"):]
        self.assertLess(start.index("writel(0x0, 0x44e00538);"), start.index("WSPR start sequence"))

    def test_clock_sync_waits_for_route_retries_and_saves(self) -> None:
        """The clock sync waits for a route, retries NTP, saves the clock and leaves ntpd running."""
        with tempfile.TemporaryDirectory() as tmp_name:
            tmp = Path(tmp_name)
            state = tmp / "state"
            state.mkdir()
            write(tmp / "ip", b"""#!/bin/sh
n=$(cat "$STATE/ip" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" >"$STATE/ip"
[ "$n" -ge 2 ] && echo "default via 192.0.2.1 dev wlan0"
""", executable=True)
            write(tmp / "timeout", b'#!/bin/sh\nshift\nexec "$@"\n', executable=True)
            write(tmp / "ntpd", b"""#!/bin/sh
case " $* " in *" -q "*) ;; *) echo "$*" >"$STATE/daemon"; exit 0 ;; esac
n=$(cat "$STATE/ntpd" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" >"$STATE/ntpd"
[ "$n" -lt 2 ] || touch "$STATE/synced"
""", executable=True)
            write(tmp / "date", b"""#!/bin/sh
[ "$1" != +%Y ] || { [ -e "$STATE/synced" ] && echo 2026 || echo 1970; }
""", executable=True)
            write(tmp / "sleep", b"#!/bin/sh\nexit 0\n", executable=True)
            write(tmp / "save-clock", b'#!/bin/sh\nprintf "%s\\n" "$1" >"$STATE/save"\n', executable=True)
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

    def test_crash_loop_reboots_only_while_u_boot_counts(self) -> None:
        """A soundtapd crash loop reboots only while U-Boot's boot count can still fall back."""
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        loop = init.split("soundtapd_loop() {", 1)[1].split("\n}\n", 1)[0]
        self.assertIn('[ "$fails" -ge 5 ] && [ -n "$CRASH_REBOOT" ]', loop)
        self.assertIn("reboot -f", loop)
        # Set where the count is below the point where both slots have had their tries,
        # so that a speaker with two bad slots stays up to be looked at.
        armed = init.split('if [ "$count" -le 9 ]; then', 1)[1].split("else", 1)[0]
        self.assertIn("CRASH_REBOOT=1", armed)

    def test_clock_logic_lives_in_clock_sh(self) -> None:
        """The init script takes its clock logic from clock.sh."""
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        self.assertIn(". /opt/soundtap/clock.sh", init)
        self.assertNotIn("ntpd -n -q", init)

    def test_rejects_an_image_that_still_carries_wifi_sh(self) -> None:
        """An image that still carries wifi.sh is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "opt/soundtap/wifi.sh", b"#!/bin/sh\n", executable=True)
            with self.assertRaisesRegex(SystemExit, "soundtapd owns Wi-Fi"):
                check.validate(root)

    def test_clock_retries_after_dhcp_until_ntp_succeeds(self) -> None:
        """The background clock sync retries NTP until it succeeds."""
        with tempfile.TemporaryDirectory() as tmp_name:
            tmp = Path(tmp_name)
            state = tmp / "state"
            state.mkdir()
            write(tmp / "ip", b'#!/bin/sh\necho "default via 192.0.2.1 dev wlan0"\n', executable=True)
            write(tmp / "timeout", b'#!/bin/sh\nshift\nexec "$@"\n', executable=True)
            write(tmp / "ntpd", b"""#!/bin/sh
case " $* " in *" -q "*) ;; *) exit 0 ;; esac
n=$(cat "$STATE/ntpd" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" >"$STATE/ntpd"
[ "$n" -lt 4 ] || touch "$STATE/synced"
[ "$n" -ge 4 ]
""", executable=True)
            write(tmp / "date", b"""#!/bin/sh
[ "$1" != +%Y ] || { [ -e "$STATE/synced" ] && echo 2026 || echo 1970; }
""", executable=True)
            write(tmp / "sleep", b"#!/bin/sh\nexit 0\n", executable=True)
            write(tmp / "save-clock", b'#!/bin/sh\ntouch "$STATE/save"\n', executable=True)
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

    def test_rejects_image_without_mnt_nv(self) -> None:
        """An image without /mnt/nv is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            fixture(tmp)
            (Path(tmp) / "mnt/nv").rmdir()
            with self.assertRaisesRegex(SystemExit, "mnt/nv"):
                check.validate(tmp)

    def test_rejects_snapcast_built_without_flac(self) -> None:
        """Snapcast built without FLAC is refused.

        The group stream is FLAC (ADR 0013): an old follower's snapclient exits at its header.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "usr/bin/snapclient", b"\x7fELF no flac here", executable=True)
            with self.assertRaisesRegex(SystemExit, "snapclient is not linked with libFLAC"):
                check.validate(root)

    def test_rejects_image_without_libflac(self) -> None:
        """An image without libFLAC is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "usr/lib/libFLAC.so.12").unlink()
            with self.assertRaisesRegex(SystemExit, "libFLAC missing"):
                check.validate(root)

    def test_gadget_console_mounts_proc_before_reading_cmdline(self) -> None:
        """The shipped gadget-console mounts proc before it reads the command line."""
        script = (ROOT / "os/overlay/etc/init.d/gadget-console").read_text()
        self.assertLess(script.index("mount -t proc"), script.index("/proc/cmdline"))

    def test_rejects_gadget_console_reading_cmdline_first(self) -> None:
        """A gadget-console that reads the command line before mounting proc is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            write(Path(tmp) / "etc/init.d/gadget-console",
                  b"#!/bin/sh\ncat /proc/cmdline\nmount -t proc proc /proc\n", executable=True)
            with self.assertRaisesRegex(SystemExit, "before mounting proc"):
                check.validate_gadget_console(tmp)

    def test_rejects_gadget_console_without_rndis(self) -> None:
        """The shipped gadget-console brings up RNDIS on usb0 with its address."""
        script = (ROOT / "os/overlay/etc/init.d/gadget-console").read_text()
        for required in ("functions/rndis.usb0", 'ln -sf "$G/functions/rndis.usb0"',
                         "ip addr add 192.168.7.2/24 dev usb0", "ip link set usb0 up"):
            self.assertIn(required, script)

    def test_spotify_new_pause_resets_grace_period(self) -> None:
        """A new Spotify pause restarts the grace period before the source is released."""
        script = ROOT / "os/overlay/opt/soundtap/spotify-source.sh"
        with tempfile.TemporaryDirectory() as tmp_name:
            tmp = Path(tmp_name)
            calls = tmp / "calls"
            write(tmp / "wget", b'#!/bin/sh\nprintf "%s\\n" "$*" >>"$CALLS"\n', executable=True)
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

    def test_source_build_includes_snapcast_packages_and_overlay(self) -> None:
        """The build includes Snapcast with FLAC, without snapweb, and its quiet configuration."""
        runtime = (ROOT / "os/package/soundtap-runtime/Config.in").read_text()
        self.assertIn("select BR2_PACKAGE_SOUNDTAP_SNAPCAST", runtime)
        snapcast = (ROOT / "os/package/soundtap-snapcast/soundtap-snapcast.mk").read_text()
        self.assertIn("rm -rf $(TARGET_DIR)/usr/share/snapserver/snapweb", snapcast)
        config = (ROOT / "os/overlay/etc/snapserver.conf").read_text()
        self.assertIn("datadir = /run/soundtap/snapserver", config)
        self.assertIn("mdns_enabled = false", config)  # its Avahi poll woke it 20 times a second
        self.assertIn("[http]\nenabled = false", config)
        self.assertIn("codec=flac:1&chunk_ms=20", config)
        self.assertIn("buffer = 1000", config)
        # The group stream is FLAC, encoded and decoded through libFLAC (ADR 0013).
        self.assertIn("-DBUILD_WITH_FLAC=ON", snapcast)
        self.assertIn("\tflac \\", snapcast)
        self.assertIn("select BR2_PACKAGE_FLAC",
                      (ROOT / "os/package/soundtap-snapcast/Config.in").read_text())

    def test_init_announces_the_bus_rate_and_keeps_the_flac_codec(self) -> None:
        """The init script announces the bus rate and keeps the FLAC codec.

        start_snapserver rewrites the rate in the shipped configuration; run its sed on it.
        """
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        line = next(text_line for text_line in init.splitlines() if 'sed "s|sampleformat=' in text_line)
        expr = line.split('"')[1].replace("$snap_rate", "48000")
        out = subprocess.run(["sed", expr, str(ROOT / "os/overlay/etc/snapserver.conf")],
                             check=True, capture_output=True, text=True).stdout
        self.assertIn("?name=SoundTap&sampleformat=48000:16:2&codec=flac:1&chunk_ms=20\n", out)
        self.assertNotIn("46875", out)

    def test_source_build_includes_bluetooth_runtime(self) -> None:
        """The build includes the Bluetooth runtime on the ST10's UART only."""
        runtime = (ROOT / "os/package/soundtap-runtime/Config.in").read_text()
        self.assertIn("select BR2_PACKAGE_BLUEZ5_UTILS", runtime)
        self.assertIn("select BR2_PACKAGE_BLUEZ_ALSA", runtime)
        bt = (ROOT / "os/overlay/opt/soundtap/bt.sh").read_text()
        self.assertIn("${BT_UARTS:-/dev/ttyO2}", bt)
        self.assertIn('[ "${MODEL:-st10}" = st10 ]', bt)
        self.assertNotIn("/dev/ttyO1", bt)
        self.assertNotIn("/dev/ttyS1", bt)

    def test_wla_hardware_defaults_are_fail_closed(self) -> None:
        """The WLA's hardware defaults fail closed in the init script."""
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        self.assertIn("binky) PROC_MODEL=wla", init)
        self.assertIn('stock_modules="snd-binky wakeup-source"', init)
        self.assertIn('elif [ "${MODEL:-st10}" = "wla" ]; then', init)

    def test_the_init_script_starts_soundtapd_as_its_own_user(self) -> None:
        """The init script starts soundtapd as its own user.

        The real script, not the fixture's lines: soundtap-root first, soundtapd through it.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "etc/init.d/soundtap").write_bytes((ROOT / "os/overlay/etc/init.d/soundtap").read_bytes())
            check.validate_soundtapd_user(root)

    def test_the_accounts_come_from_the_filesystems_own_tree(self) -> None:
        """The accounts come from the filesystem's own tree.

        output/target has no users from users.txt; only the copy mkfs packs does
        (images/rootfs-tree). image-check passes that tree as accounts.
        """
        with tempfile.TemporaryDirectory() as tmp, tempfile.TemporaryDirectory() as fs:
            root = fixture(tmp)
            for name in ("etc/passwd", "etc/group"):
                (Path(fs) / name).parent.mkdir(parents=True, exist_ok=True)
                (Path(fs) / name).write_bytes((root / name).read_bytes())
            (root / "etc/passwd").write_text("root:x:0:0::/root:/bin/sh\n")
            with self.assertRaises(SystemExit):
                check.validate_soundtapd_user(root)
            check.validate_soundtapd_user(root, accounts=fs)

    def test_soundtapd_never_runs_as_root(self) -> None:
        """Each way soundtapd could end up running as root is refused."""
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

    def test_valid_contract(self) -> None:
        """The fixture image passes the check."""
        with tempfile.TemporaryDirectory() as tmp:
            check.validate(fixture(tmp))

    def test_rejects_a_special_builtin_writability_probe(self) -> None:
        """A writability probe through a special built-in is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            init = root / "etc/init.d/soundtap"
            init.write_bytes(init.read_bytes().replace(
                b'true >"/mnt/nv/soundtap/.writable.$$"',
                b': >"/mnt/nv/soundtap/.writable.$$"'))
            with self.assertRaisesRegex(SystemExit, "probes /mnt/nv with"):
                check.validate(root)

    def test_follows_absolute_and_relative_links_inside_the_root(self) -> None:
        """Absolute and relative links are followed inside the root."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "usr/sbin/udhcpd").unlink()
            (root / "usr/sbin/udhcpd").symlink_to("../../bin/busybox")
            check.validate(root)
            (root / "usr/sbin/udhcpd").unlink()
            (root / "usr/sbin/udhcpd").symlink_to("/bin/other")
            with self.assertRaises(SystemExit):
                check.validate(root)

    def test_rejects_kernel_modules(self) -> None:
        """An image carrying a kernel module is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "lib/modules/6.12/foo.ko")
            with self.assertRaises(SystemExit):
                check.validate(root)

    def test_accepts_the_st300_cpsw_modules(self) -> None:
        """The ST300's CPSW modules are accepted."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "lib/modules/6.12/kernel/drivers/net/ethernet/ti/ti_cpsw_new.ko")
            write(root / "lib/modules/6.12/kernel/drivers/phy/ti/phy-gmii-sel.ko")
            check.validate(root)

    def test_rejects_a_rootfs_without_ssh(self) -> None:
        """An alpha image without dropbear is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "usr/sbin/dropbear").unlink()
            with self.assertRaisesRegex(SystemExit, "no dropbear"):
                check.validate(root)

    def test_rejects_ssh_that_refuses_the_blank_password(self) -> None:
        """SSH that refuses the blank password is refused.

        A blank password only lets anyone in if dropbear is told to accept one; the
        two have to be configured together or the speaker is quietly unreachable.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            ssh = root / "opt/soundtap/ssh.sh"
            ssh.write_text(ssh.read_text().replace("dropbear -B", "dropbear"))
            with self.assertRaisesRegex(SystemExit, "blank-password"):
                check.validate(root)

    def test_rejects_a_locked_root_account(self) -> None:
        """An alpha image with a locked root account is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "etc/shadow", b"root:!:19000:0:99999:7:::\n")
            with self.assertRaisesRegex(SystemExit, "blank"):
                check.validate(root)

    def test_production_rejects_alpha_ssh(self) -> None:
        """A production image with alpha's SSH is refused."""
        with tempfile.TemporaryDirectory() as tmp, self.assertRaisesRegex(SystemExit, "production rootfs contains dropbear"):
            check.validate_ssh_policy(fixture(tmp), profile="production")

    def production(self, tmp: str) -> Path:
        """Fill tmp with a production root filesystem that passes the SSH policy, and return it."""
        root = fixture(tmp)
        (root / "usr/sbin/dropbear").unlink()
        write(root / "etc/shadow", b"root:!:19000:0:99999:7:::\n")
        (root / "opt/soundtap/ssh.sh").unlink()
        write(root / "etc/soundtap/profile", b"production\n")
        write(root / "usr/sbin/nft", b"#!/bin/sh\n", executable=True)
        write(root / "etc/soundtap/firewall.nft", (ROOT / "os/board/rootfs/firewall.nft").read_bytes())
        write(root / "etc/inittab", b"::sysinit:/usr/sbin/nft -f /etc/soundtap/firewall.nft\n"
              b"::sysinit:/etc/init.d/gadget-console\n::sysinit:/etc/init.d/soundtap\n")
        return root

    def test_production_refuses_a_package_that_may_not_be_redistributed(self) -> None:
        """Production refuses a package that may not be redistributed."""
        config = (ROOT / "os/configs/soundtap_defconfig").read_text()
        config += (ROOT / "os/configs/production.fragment").read_text()
        check.validate_redistributable(config)
        with tempfile.TemporaryDirectory() as tmp:
            write(Path(tmp) / "os/package/closed-thing/closed-thing.mk",
                  b"CLOSED_THING_VERSION = 1\nCLOSED_THING_REDISTRIBUTE = NO\n")
            check.validate_redistributable(config, root=Path(tmp))
            with self.assertRaisesRegex(SystemExit, "closed-thing"):
                check.validate_redistributable(config + "BR2_PACKAGE_CLOSED_THING=y\n", root=Path(tmp))

    def test_production_accepts_no_ssh_and_locked_root(self) -> None:
        """Production accepts an image without SSH and with root locked."""
        with tempfile.TemporaryDirectory() as tmp:
            check.validate_ssh_policy(self.production(tmp), profile="production")

    def test_production_rejects_any_ssh_leftover(self) -> None:
        """Production refuses any SSH leftover.

        Not the server alone: nothing SSH, nor a script or filter rule that would bring it back (ADR 0119).
        """
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

    def test_production_loads_the_input_filter_first(self) -> None:
        """Production loads the default-deny input filter first.

        The input filter has to be in place before gadget-console brings up usb0 (ADR 0066).
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = self.production(tmp)
            write(root / "etc/inittab", b"::sysinit:/etc/init.d/gadget-console\n"
                  b"::sysinit:/usr/sbin/nft -f /etc/soundtap/firewall.nft\n")
            with self.assertRaisesRegex(SystemExit, "input filter first"):
                check.validate_ssh_policy(root, profile="production")
            (root / "etc/soundtap/firewall.nft").unlink()
            with self.assertRaisesRegex(SystemExit, "default-deny"):
                check.validate_ssh_policy(root, profile="production")

    def test_production_filter_covers_ipv6(self) -> None:
        """An ip table instead of the inet one leaves IPv6 unfiltered and is refused (ADR 0134)."""
        with tempfile.TemporaryDirectory() as tmp:
            root = self.production(tmp)
            check.validate_ssh_policy(root, profile="production")
            rules = root / "etc/soundtap/firewall.nft"
            rules.write_text(rules.read_text().replace("table inet filter", "table ip filter"))
            with self.assertRaisesRegex(SystemExit, "does not cover IPv6"):
                check.validate_ssh_policy(root, profile="production")

    def test_production_rejects_the_gadget_shell(self) -> None:
        """Production refuses a root shell on the USB gadget console."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "usr/sbin/dropbear").unlink()
            (root / "opt/soundtap/ssh.sh").unlink()
            write(root / "etc/shadow", b"root:!:19000:0:99999:7:::\n")
            write(root / "etc/soundtap/profile", b"production\n")
            write(root / "etc/inittab", b"ttyGS0::respawn:/bin/sh\n")
            with self.assertRaisesRegex(SystemExit, "gadget a root shell"):
                check.validate_ssh_policy(root, profile="production")

    def test_rejects_provisioned_wifi_in_production(self) -> None:
        """An image carrying a build-time Wi-Fi profile is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            write(root / "etc/soundtap-wifi.conf", b"SSID_B64=dGVzdA==\nPSK_B64=dGVzdA==\n")
            with self.assertRaisesRegex(SystemExit, "build-time Wi-Fi profile"):
                check.validate(root)


    def test_snapserver_falls_back_from_read_only_storage_and_bounds_restarts(self) -> None:
        """The init script falls back from read-only storage, disables snapserver then, and leaves restarts to soundtapd."""
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

    def test_rejects_airplay_without_forced_ap2(self) -> None:
        """AirPlay without AirPlay 2 forced is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            (root / "etc/shairport-sync.conf").write_text('general = { service_type = "auto"; };\n')
            with self.assertRaisesRegex(SystemExit, "AirPlay configuration contract"):
                check.validate(root)

    def test_rejects_unbounded_airplay_fifo_buffer(self) -> None:
        """AirPlay without its bounded buffer is refused."""
        with tempfile.TemporaryDirectory() as tmp:
            root = fixture(tmp)
            config = root / "etc/shairport-sync.conf"
            config.write_text(config.read_text().replace("buffer_size = 8192;", ""))
            with self.assertRaisesRegex(SystemExit, "buffer_size = 8192"):
                check.validate(root)


if __name__ == "__main__":
    unittest.main()
