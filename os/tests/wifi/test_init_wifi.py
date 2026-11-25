# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Who brings up Wi-Fi at boot.

soundtapd's Wi-Fi manager (app/internal/wifi) joins the saved network or opens the setup AP;
the init script starts no Wi-Fi script of its own. It only tells soundtapd which mode the
radio is in: on an ST20 it first looks for the own BCO app's link, with which the BCO's
wlan0 is ours to join like an ST10's; without it stock's BCO app keeps Wi-Fi. The setup
AP's marker path is exported for soundtapd either way.
"""
import tempfile
import unittest

from boot.init_script import InitScript

SAVED = "SSID_B64=aG9tZQ==\nPSK_B64=c2VjcmV0\n"


def wifi(model: str, sys_net: tuple[str, ...] = (), conf: str | None = None) -> tuple[str, list[str], bool, list[str], str]:
    """Run the boot's network steps; return (mode, calls, marker, musb rebinds, log)."""
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        for name in sys_net:
            (init.sys / "class/net" / name).mkdir(parents=True)
        (init.sys / "bus/platform/drivers/musb-hdrc").mkdir(parents=True)
        if conf is not None:
            (init.nv / "wifi.conf").write_text(conf)
        init.stub("wifi.sh", where=init.opt)  # an old image's script must not be started
        init.stub("sleep")
        init.stub("devmem", "exit 1")
        done = init.run(f"MODEL={model} MAINLINE=1 BOOT_TRACE={d}/trace\n"
                        'netwatch() { echo netwatch >>"$LOG"; }\n'
                        "{ device_identity; select_wifi_mode; start_wifi; wait; } >/dev/null\n"
                        'echo "$SOUNDTAP_WIFI_MODE/$SOUNDTAP_AIRPLAY_MODE $SOUNDTAP_SETUP_AP_MARKER"')
        calls = [c for c in init.calls() if not c.startswith(("sleep", "devmem"))]
        musb = init.sys / "bus/platform/drivers/musb-hdrc"
        rebound = [f.name for f in sorted(musb.iterdir())]
        mode, marker = done.stdout.split()
        return (mode, calls, marker == str(init.run_dir / "setup-ap"), rebound,
                init.log.read_text() if init.log.exists() else "")


class InitWifi(unittest.TestCase):
    """The init script's Wi-Fi mode per model."""

    def test_soundtapd_owns_wifi_where_the_main_cpu_has_the_radio(self) -> None:
        """On the ST10, WLA and ST300 soundtapd brings up Wi-Fi and init starts no script."""
        for model in ("st10", "wla", "st300"):
            for conf in (SAVED, "SSID_B64=\n", None):
                with self.subTest(model=model, conf=conf):
                    mode, calls, marker, _, log = wifi(model, conf=conf)
                    self.assertEqual((mode, calls, marker), ("scm/scm-ap2", [], True))
                    self.assertIn("wifi: soundtapd brings it up", log)
                    self.assertIn("netwatch", log)

    def test_st20_with_own_bco_joins_like_an_st10(self) -> None:
        """An ST20 whose BCO runs our app leaves Wi-Fi to soundtapd, as on an ST10."""
        mode, calls, marker, rebound, log = wifi("st20", sys_net=("eth0", "bcolink0"), conf=SAVED)
        self.assertEqual((mode, calls, marker, rebound), ("scm/scm-ap2", [], True, []))
        self.assertIn("wifi: soundtapd brings it up", log)

    def test_st20_with_stock_bco_leaves_wifi_to_it(self) -> None:
        """An ST20 with stock's BCO app leaves Wi-Fi to the BCO."""
        mode, calls, _, _, log = wifi("st20", sys_net=("eth0",))
        self.assertEqual((mode, calls), ("bco/bco-ap1", []))
        self.assertIn("wifi skipped (st20 BCO owns WiFi)", log)

    def test_st20_missing_bco_link_rebinds_usb_once_and_leaves_wifi_to_the_bco(self) -> None:
        """An ST20 without the BCO link rebinds USB once, logs an error and leaves Wi-Fi to the BCO."""
        mode, calls, _, rebound, log = wifi("st20")
        self.assertEqual((mode, calls, rebound), ("bco/bco-ap1", [], ["bind", "unbind"]))
        self.assertIn("ERROR: st20 BCO link still missing", log)


if __name__ == "__main__":
    unittest.main()
