# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The WLA's audio-unavailable branch must leave a reachable box, not a dark one."""
import subprocess
import tempfile
import unittest

from boot.init_script import InitScript
from boot.test_init_starts_bt import services


class FailClosedTest(unittest.TestCase):
    """The init script's WLA branch without a sound card."""

    def test_no_pcm_still_starts_soundtapd_without_audio(self) -> None:
        """Without a PCM device the boot still starts soundtapd, with -no-audio.

        No /dev/snd/pcmC0D0p in the test: the branch a board without a sound card takes.
        Run under the script's own `set -u`, so a variable the tail reads unset ends it here.
        """
        setup = ("MODEL=wla MAINLINE=1 NOCODEC=1 SOUNDTAP_AIRPLAY_MODE=scm-ap2; unset AUDIO_MIX\n"
                 "set_audio_defaults; select_audio_output")
        self.assertEqual(services(setup), ["avahi-daemon -D", "bt.sh", "dbus-daemon --system --fork",
                                           "soundtapd_loop -no-audio"])

    def test_an_unset_variable_really_would_end_the_boot(self) -> None:
        """An unset variable under `set -u` ends the shell, which is why the check above matters."""
        for shell in ("dash", "sh"):
            with self.subTest(shell=shell):
                out = subprocess.run([shell, "-c", 'set -u; X="alsa:$NOPE"; echo reached'],
                                     check=False, capture_output=True, text=True)
                self.assertNotIn("reached", out.stdout)

    def test_unknown_arguments_are_refused(self) -> None:
        """An unknown subcommand exits 2 before anything runs.

        `restart` once fell through into a second full boot and supervisor;
        main runs this before it mounts anything.
        """
        with tempfile.TemporaryDirectory() as d:
            done = InitScript(d).run("run_subcommand restart; echo reached", check=False)
        self.assertEqual((done.returncode, done.stdout), (2, ""))


if __name__ == "__main__":
    unittest.main(verbosity=1)
