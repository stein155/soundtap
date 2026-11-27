# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""bt-nowplaying.sh --push posts a body soundtapd can parse, whatever the phone sends as a title."""
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[3] / "os/overlay/opt/soundtap/bt-nowplaying.sh"


def push(line: str) -> str:
    """Run `bt-nowplaying.sh --push line` against a recording wget and return the body it posted."""
    with tempfile.TemporaryDirectory() as d:
        wget = Path(d) / "wget"
        wget.write_text('#!/bin/sh\nfor a; do case "$a" in --post-data=*) printf \'%s\' "${a#--post-data=}" >"$LOG";; esac; done\n')
        wget.chmod(0o755)
        log = Path(d) / "body"
        env = {**os.environ, "PATH": f"{d}:{os.environ['PATH']}", "LOG": str(log)}
        subprocess.run(["sh", str(SCRIPT), "--push", line], check=True, env=env, timeout=30)
        return log.read_bytes().decode()


class NowPlayingBody(unittest.TestCase):
    """The pushed body is valid JSON with the track fields as the phone sent them."""

    def test_quotes_and_backslashes_are_escaped(self) -> None:
        """A title with a quote and a backslash survives the round trip."""
        body = json.loads(push('playing\ta "12\\" mix"\tArtist\tAlbum'))
        self.assertEqual(body, {"source": "BLUETOOTH", "track": 'a "12\\" mix"', "artist": "Artist", "album": "Album"})

    def test_control_characters_are_dropped(self) -> None:
        """A raw control byte in a title is dropped instead of making the body invalid JSON."""
        body = json.loads(push("playing\tIn\x01 Your\x1f Eyes\x7f\tAl\x02esso\tAlbum"))
        self.assertEqual(body["track"], "In Your Eyes\x7f")
        self.assertEqual(body["artist"], "Alesso")


if __name__ == "__main__":
    unittest.main()
