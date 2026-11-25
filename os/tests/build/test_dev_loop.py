# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""`make dev`'s daemon part (tools/dev/dev.sh daemon) runs soundtapd on the simulated speaker.

It builds soundtapd, runs it with -dev on the simulated speaker and answers /api/version and
/api/health; Ctrl-C stops it cleanly, and it announced nothing on the LAN. Skipped without
Go 1.26 or newer on the host.
"""
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.request
from http import HTTPStatus
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/dev/dev.sh"
# The first build in an empty module and build cache downloads and compiles everything.
BUILD_TIMEOUT = 600
ANSWER_TIMEOUT = 10
STOP_TIMEOUT = 15


def host_go() -> bool:
    """Return whether the host's Go is one dev.sh builds with natively (1.26 or newer)."""
    if not shutil.which("go"):
        return False
    try:
        v = subprocess.run(["go", "env", "GOVERSION"], capture_output=True, text=True,
                           timeout=60, check=True).stdout
    except (OSError, subprocess.SubprocessError):
        return False
    m = re.match(r"go(\d+)\.(\d+)", v.strip())
    return bool(m) and (int(m[1]), int(m[2])) >= (1, 26)


def free_port() -> int:
    """Return a TCP port on 127.0.0.1 that is free right now."""
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def get(url: str) -> tuple[int, dict[str, object]]:
    """Fetch url and return its status and decoded JSON body."""
    with urllib.request.urlopen(url, timeout=2) as r:
        return r.status, json.loads(r.read())


@unittest.skipUnless(host_go(), "needs Go 1.26 or newer on the host")
@unittest.skipUnless(shutil.which("bash"), "needs bash")
class DevLoopDaemon(unittest.TestCase):
    """dev.sh daemon runs soundtapd -dev on the simulated speaker."""

    def test_daemon_answers_and_stops(self) -> None:
        """The daemon answers the API for the chosen model, stops cleanly on Ctrl-C and announces nothing."""
        port = free_port()
        base = f"http://127.0.0.1:{port}"
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp) / "dev-home"
            env = {k: v for k, v in os.environ.items() if k != "SOUNDTAP_FIRMWARE"}
            env.update(MODEL="st20", DEV_PORT=str(port), DEV_HOME=str(home), DEV_CONTAINERS="0",
                       DEV_BIN=str(Path(tmp) / "bin"))
            log_path = Path(tmp) / "daemon.log"
            with log_path.open("w") as log:
                proc = subprocess.Popen([str(SCRIPT), "daemon"], env=env, stdout=log,
                                        stderr=subprocess.STDOUT, start_new_session=True)
            try:
                version = self.wait_for(proc, base + "/api/version", BUILD_TIMEOUT, log_path)
                self.assertIn("version", version)
                health = self.wait_for(proc, base + "/api/health", ANSWER_TIMEOUT, log_path)
                self.assertIn("warnings", health)
                # MODEL picks the simulated speaker the API describes, with no model problem.
                self.assertEqual(get(base + "/api/info")[1]["model"], "SoundTouch 20")
                self.assertEqual(health["warnings"], [], health)
            finally:
                if proc.poll() is None:
                    os.killpg(proc.pid, signal.SIGINT)  # what Ctrl-C sends the group
                try:
                    proc.wait(timeout=STOP_TIMEOUT)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid, signal.SIGKILL)
                    proc.wait()
                    self.fail(f"soundtapd -dev did not stop within {STOP_TIMEOUT:d} s of SIGINT")
            self.assertEqual(proc.returncode, 0, log_path.read_text())
            out = log_path.read_text()
            self.assertIn("development run", out)
            self.assertNotIn("comp=mdns", out)  # no responder, no avahi
            self.assertTrue((home / "st20" / "soundtap").is_dir())

    def wait_for(self, proc: subprocess.Popen[bytes], url: str, timeout: int, log_path: Path) -> dict[str, object]:
        """Poll url until it answers 200 and return its body; fail if proc exits or timeout passes."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if proc.poll() is not None:
                self.fail(f"dev.sh daemon exited with {proc.returncode}:\n{log_path.read_text()}")
            try:
                status, body = get(url)
                if status == HTTPStatus.OK:
                    return body
            except (OSError, ValueError):
                pass
            time.sleep(0.25)
        msg = f"{url} did not answer within {timeout:d} s:\n{log_path.read_text()}"
        raise self.failureException(msg)


if __name__ == "__main__":
    unittest.main()
