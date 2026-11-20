# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Which librespot events make spotify-source.sh flush the queued audio."""
import os
import pathlib
import signal
import subprocess
import tempfile
import unittest
import contextlib
import time

HOOK = pathlib.Path(__file__).resolve().parents[3] / "os/overlay/opt/soundtap/spotify-source.sh"


class SpotifyHookFlush(unittest.TestCase):
    def setUp(self):
        self.tmp = pathlib.Path(tempfile.mkdtemp())
        (self.tmp / "bin").mkdir()
        wget = self.tmp / "bin/wget"
        # Record the JSON body of every POST the hook makes.
        wget.write_text('#!/bin/sh\nfor a; do case "$a" in --post-data=*) echo "${a#--post-data=}" >>"$LOG";; esac; done\n')
        wget.chmod(0o755)
        self.log = self.tmp / "posts"
        self.boundary = False
        self.groups = []
        self.addCleanup(self.kill_timers)

    def kill_timers(self):
        # The pause timer sleeps PAUSE_GRACE (an hour here) in the hook's process
        # group; left alone, every test run leaked one per pause.
        for pgid in self.groups:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(pgid, signal.SIGTERM)

    def run_events(self, *events):
        for event in events:
            event, _, sink = event.partition(":")
            env = dict(os.environ, PATH=f"{self.tmp / 'bin'}:{os.environ['PATH']}", LOG=str(self.log),
                       PLAYER_EVENT=event, NAME="", SINK_STATUS=sink, PAUSE_GRACE="3600", EOT_STAMP=str(self.tmp / "eot"),
                       FLUSHED_STAMP=str(self.tmp / "flushed"), PAUSE_STAMP=str(self.tmp / "paused"), BOUNDARY_STAMP=str(self.tmp / "boundary"),
                       SOUNDTAP_SPOTIFY_BOUNDARY="1" if self.boundary else "0")
            # The pause timer outlives the hook; keep it off our stdout.
            proc = subprocess.Popen(["sh", str(HOOK)], env=env, stdout=subprocess.DEVNULL,
                                    stderr=subprocess.DEVNULL, start_new_session=True)
            self.groups.append(proc.pid)
            self.assertEqual(proc.wait(timeout=30), 0)
        text = self.log.read_text() if self.log.exists() else ""
        return text.count('"event":"flush"')

    def test_says_where_the_track_is(self):
        env = dict(os.environ, PATH=f"{self.tmp / 'bin'}:{os.environ['PATH']}", LOG=str(self.log), NAME="",
                   PAUSE_GRACE="0", EOT_STAMP=str(self.tmp / "eot"), FLUSHED_STAMP=str(self.tmp / "flushed"),
                   PAUSE_STAMP=str(self.tmp / "paused"), BOUNDARY_STAMP=str(self.tmp / "boundary"),
                       SOUNDTAP_SPOTIFY_BOUNDARY="1" if self.boundary else "0")
        for extra in ({"PLAYER_EVENT": "track_changed", "DURATION_MS": "180000"},
                      {"PLAYER_EVENT": "position_correction", "POSITION_MS": "5000"},
                      {"PLAYER_EVENT": "position_correction", "POSITION_MS": "5;x"}):
            subprocess.run(["sh", str(HOOK)], env={**env, **extra}, check=True, timeout=30,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        posts = self.log.read_text()
        self.assertIn('{"source":"SPOTIFY","positionMs":0,"durationMs":180000}', posts)
        self.assertIn('{"source":"SPOTIFY","positionMs":5000}', posts)
        self.assertNotIn("5;x", posts)

    def test_stamps_default_to_the_directory_librespot_owns(self):
        # librespot's user can write only the pause stamp's directory.
        text = HOOK.read_text()
        for name in ("PAUSE_STAMP", "EOT_STAMP", "FLUSHED_STAMP"):
            self.assertIn(f"{name}=${{{name}:-/run/soundtap/spotify/", text)

    def test_skip_to_unloaded_track_flushes_once_at_loading(self):
        self.assertEqual(self.run_events("loading", "track_changed"), 1)

    def test_skip_to_preloaded_track_flushes_at_track_changed(self):
        self.assertEqual(self.run_events("track_changed"), 1)

    def test_natural_advance_keeps_the_tail(self):
        self.assertEqual(self.run_events("end_of_track", "loading", "track_changed"), 0)
        self.assertEqual(self.run_events("end_of_track", "track_changed"), 0)

    def test_skip_after_a_natural_advance_flushes_again(self):
        self.assertEqual(self.run_events("end_of_track", "track_changed", "loading", "track_changed"), 1)

    def test_seek_and_pause_flush(self):
        self.assertEqual(self.run_events("seeked", "paused"), 2)

    def test_pause_and_resume_are_reported(self):
        self.run_events("paused", "playing")
        events = [line for line in self.log.read_text().splitlines() if '"event":"p' in line]
        self.assertEqual(events, ['{"source":"SPOTIFY","event":"pause"}', '{"source":"SPOTIFY","event":"play"}'])

    def test_pause_keeps_the_source_and_only_a_stop_releases_it(self):
        # librespot 0.8 reports a pause as "sink closed".
        self.run_events("sink:closed", "sink:temporarily_closed")
        self.assertNotIn('"event":"stop"', self.log.read_text() if self.log.exists() else "")
        self.run_events("stopped")
        self.assertIn('{"source":"SPOTIFY","event":"stop"}', self.log.read_text())

    def test_closed_sink_is_the_skip_boundary(self):
        self.boundary = True
        self.run_events("play_request_id_changed", "sink:temporarily_closed")
        self.assertEqual(self.log.read_text().count('"event":"flush_wait"'), 1)
        self.run_events("loading", "track_changed", "sink:running", "playing")
        self.assertEqual(self.log.read_text().count('"event":"flush_wait"'), 1)
        self.assertNotIn('"event":"flush"', self.log.read_text())

    def test_natural_boundary_keeps_tail_then_next_skip_flushes(self):
        self.boundary = True
        self.run_events("end_of_track", "play_request_id_changed", "sink:temporarily_closed",
                        "track_changed", "sink:running")
        self.assertNotIn('"event":"flush', self.log.read_text())
        self.run_events("play_request_id_changed", "sink:temporarily_closed", "track_changed")
        self.assertEqual(self.log.read_text().count('"event":"flush_wait"'), 1)

    def test_initial_request_marker_does_not_survive_track_changed(self):
        self.boundary = True
        self.run_events("play_request_id_changed", "loading", "track_changed", "sink:running")
        self.assertFalse((self.tmp / "boundary").exists())
        self.assertNotIn('"event":"flush', self.log.read_text())

    def test_sink_waits_for_delayed_event_thread(self):
        self.boundary = True
        env = dict(os.environ, PATH=f"{self.tmp / 'bin'}:{os.environ['PATH']}", LOG=str(self.log),
                   PLAYER_EVENT="sink", SINK_STATUS="temporarily_closed", PAUSE_GRACE="3600",
                   EOT_STAMP=str(self.tmp / "eot"), FLUSHED_STAMP=str(self.tmp / "flushed"),
                   PAUSE_STAMP=str(self.tmp / "paused"), BOUNDARY_STAMP=str(self.tmp / "boundary"),
                   SOUNDTAP_SPOTIFY_BOUNDARY="1")
        proc = subprocess.Popen(["sh", str(HOOK)], env=env, start_new_session=True,
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.groups.append(proc.pid)
        time.sleep(0.05)
        self.assertIsNone(proc.poll(), "sink resumed before the event thread reached the boundary")
        self.run_events("end_of_track", "play_request_id_changed")
        self.assertEqual(proc.wait(timeout=5), 0)
        self.assertFalse(self.log.exists(), "late natural end was mistaken for a skip")

    def test_preloaded_skip_waits_for_ack_and_has_no_late_flush(self):
        self.boundary = True
        self.run_events("play_request_id_changed")
        # An HTTP call that does not acknowledge yet: the synchronous sink hook
        # must keep librespot from writing even a preloaded track's first block.
        gate = self.tmp / "ack"
        (self.tmp / "bin/wget").write_text(
            '#!/bin/sh\nfor a; do case "$a" in --post-data=*) '
            'echo "${a#--post-data=}" >>"$LOG";; esac; done\n'
            f'while [ ! -f "{gate}" ]; do sleep 0.01; done\n')
        env = dict(os.environ, PATH=f"{self.tmp / 'bin'}:{os.environ['PATH']}", LOG=str(self.log),
                   PLAYER_EVENT="sink", SINK_STATUS="temporarily_closed", PAUSE_GRACE="3600",
                   EOT_STAMP=str(self.tmp / "eot"), FLUSHED_STAMP=str(self.tmp / "flushed"),
                   PAUSE_STAMP=str(self.tmp / "paused"), BOUNDARY_STAMP=str(self.tmp / "boundary"),
                   SOUNDTAP_SPOTIFY_BOUNDARY="1")
        proc = subprocess.Popen(["sh", str(HOOK)], env=env, start_new_session=True,
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.groups.append(proc.pid)
        deadline = time.monotonic() + 3
        while not self.log.exists() and time.monotonic() < deadline:
            time.sleep(0.01)
        self.assertTrue(self.log.exists())
        self.assertIsNone(proc.poll(), "player resumed before HTTP acknowledgement")
        gate.touch()
        self.assertEqual(proc.wait(timeout=5), 0)
        self.run_events("track_changed", "sink:running", "playing")
        self.assertEqual(self.log.read_text().count('"event":"flush_wait"'), 1)
        self.assertNotIn('"event":"flush"', self.log.read_text())


if __name__ == "__main__":
    unittest.main()
