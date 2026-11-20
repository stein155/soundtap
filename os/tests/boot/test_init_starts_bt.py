# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every model runs the same tail of the init script.

Model differences are hooks inside the shared steps: one soundtapd launch for every model,
and bt.sh with it.
"""
import tempfile
import unittest

from boot.init_script import InitScript


def services(setup: str) -> list:
    """Runs setup, then the shared tail; returns what it started, sorted."""
    with tempfile.TemporaryDirectory() as d:
        init = InitScript(d)
        init.stub("bt.sh", where=init.opt)
        for name in ("dbus-daemon", "avahi-daemon", "sleep"):
            init.stub(name)
        # The daemons themselves are stand-ins: this checks what the tail starts, not them.
        init.run(f'rec() {{ echo "$*" >>"{init.calls_file}"; }}\n'
                 f"BOOT_TRACE={d}/trace\n"
                 "sync_clock_in_background() { :; }; start_snapserver() { rec start_snapserver; return 1; }\n"
                 "snapclient_id() { echo id; }\n"
                 'soundtapd_loop() { rec soundtapd_loop "$@"; }\n'
                 f"{{ {setup}\nstart_services; wait; }} >/dev/null 2>&1")
        return sorted(c.strip() for c in init.calls() if not c.startswith("sleep"))


class InitSharedTail(unittest.TestCase):
    def test_every_model_starts_bt_and_soundtapd(self):
        for model, airplay in (("st10", "scm-ap2"), ("st20", "bco-ap1"), ("st20", "scm-ap2"), ("wla", "scm-ap2")):
            with self.subTest(model=model, airplay=airplay):
                want = ["avahi-daemon -D", "bt.sh", "dbus-daemon --system --fork", "soundtapd_loop", "start_snapserver"]
                setup = f"MODEL={model} SOUNDTAP_AIRPLAY_MODE={airplay} NOCODEC=0; set_audio_defaults"
                self.assertEqual(services(setup), want)


if __name__ == "__main__":
    unittest.main()
