#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""A fake speaker for the bench scripts in tools/hil/: the HTTP API they use, and a boot.

    fake_speaker.py [--port 8080]      (then: tools/hil/install-candidate.sh 127.0.0.1:8080 ...)

It answers what a production image answers to the scripts, and nothing else:

- GET /api/health with the running boot (id, slot, confirmed, previous) and GET
  /api/version with the running slot's build;
- GET/POST /api/volume;
- POST /api/firmware?install=1, /api/soundtapd and /api/reboot, which wait for a key held
  on the speaker (press()) or carry the one-time token (token_file), as ADR 0055 says.

The boot follows our U-Boot and soundtapd (ADR 0010): a reboot takes the speaker off the
network for a moment and comes back with a new boot id. U-Boot adds one to its boot count
at every boot and boots the other slot once the count passes bootlimit (4); soundtapd confirms
a boot `confirm_after` seconds after it started, which resets the count and selects the slot
it runs from. A reboot before the confirm is warm and keeps the count; after it, and at a
power cut, the reset is cold and clears it, except for a slot an install selected and no
boot has confirmed yet (upgrade_available): U-Boot keeps that count in the environment too.
"""
import argparse
import json
import threading
import time
import uuid
from collections.abc import Callable, Mapping
from dataclasses import InitVar, dataclass
from email.message import Message
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

BOOTLIMIT = 4

# A request's headers: the handler's message, or a plain mapping in a test.
Headers = Mapping[str, str] | Message
# An answer: the HTTP status and the JSON body.
Answer = tuple[int, dict[str, object]]


def other(slot: str) -> str:
    """Return the slot that is not slot."""
    return "PONG" if slot == "PING" else "PING"


@dataclass(eq=False)
class FakeSpeaker:
    """A speaker's API, its boot slots and U-Boot's boot count, with a key a test can hold."""

    slot: InitVar[str] = "PING"
    build: InitVar[str] = "0.1.0"
    confirm_after: float = 1.0
    down_for: float = 0.2
    reboot_delay: float = 0.0
    hold_window: float = 5.0
    token_file: str | None = None

    def __post_init__(self, slot: str, build: str) -> None:
        """Start on slot, both slots holding build, with the running boot confirmed long ago."""
        self.cond = threading.Condition()
        # The NAND: what each slot holds, which one the environment selects.
        self.builds = {"PING": build, "PONG": build}
        self.selected = slot
        self.count = 0
        self.cold = True
        # upgrade_available: the selection an install made, not confirmed yet.
        self.unconfirmed = False
        # The running boot: confirmed long ago.
        self.running, self.boot_id = slot, str(uuid.uuid4())
        self.booted_at = time.monotonic() - self.confirm_after
        self.confirmed, self.previous = True, "reboot"
        self.app = None
        self.volume = 12
        # A reset in progress: off the network from down_from to up_at, then booting.
        self.down_from = self.up_at = None
        self.pending = None
        self.never_back = False
        # The press: requests waiting for it, holds not yet taken.
        self.waiting = self.holds = 0
        self._new_token()
        # What happened, for the tests: (method, path, how it was confirmed).
        self.requests = []
        self.boots = []

    # --- the speaker's side -------------------------------------------------------------

    def _new_token(self) -> None:
        self.token = uuid.uuid4().hex
        if self.token_file:
            with Path(self.token_file).open("w") as f:
                f.write(self.token + "\n")

    def tick(self) -> None:
        """Move time on: a reset that is due, a boot confirm that is due."""
        now = time.monotonic()
        if self.pending and not self.never_back and now >= self.up_at:
            self._boot(self.pending)
        if not self.confirmed and not self.down() and now - self.booted_at >= self.confirm_after:
            # soundtapd: a count past bootlimit means U-Boot fell back, so select this slot.
            if self.count > BOOTLIMIT:
                self.selected = self.running
            self.count, self.confirmed, self.cold = 0, True, True
            self.unconfirmed = False

    def down(self) -> bool:
        """Return whether the speaker is off the network for a reset."""
        return self.pending is not None and time.monotonic() >= self.down_from

    def _reset(self, how: str, delay: float = 0.0) -> None:
        now = time.monotonic()
        self.pending = how
        self.down_from = now + delay
        self.up_at = now + delay + self.down_for

    def _boot(self, how: str) -> None:
        if (self.cold or how == "power loss") and not self.unconfirmed:
            self.count = 0
        self.count += 1
        self.running = self.selected if self.count <= BOOTLIMIT else other(self.selected)
        self.boot_id = str(uuid.uuid4())
        self.booted_at = time.monotonic()
        # Init keeps the reset warm while U-Boot counts (init.d/soundtap).
        self.confirmed, self.cold, self.previous = False, False, how
        self.pending = None
        self.volume = 30  # a boot does not keep the bench's volume
        self.boots.append(self.running)

    def power_cut(self) -> None:
        """Cut the power and restore it, as the person at the speaker pulling the plug does."""
        with self.cond:
            self.cold = True
            self._reset("power loss")

    def press(self, timeout: float = 10.0) -> bool:
        """Hold a key once a request waits for it, as a person reading the prompt does."""
        deadline = time.monotonic() + timeout
        with self.cond:
            while self.waiting == 0:
                left = deadline - time.monotonic()
                if left <= 0:
                    return False
                self.cond.wait(left)
            self.holds += 1
            self.cond.notify_all()
            return True

    def gate(self, token: str | None) -> str | None:
        """Whether a dangerous request may run: the token, or a hold in time."""
        if token and token == self.token:
            self._new_token()
            return "token"
        self.waiting += 1
        self.cond.notify_all()
        deadline = time.monotonic() + self.hold_window
        try:
            while self.holds == 0:
                left = deadline - time.monotonic()
                if left <= 0:
                    return None
                self.cond.wait(left)
            self.holds -= 1
            return "hold"
        finally:
            self.waiting -= 1

    # --- the API ------------------------------------------------------------------------

    def health(self) -> dict[str, object]:
        """Return the /api/health body with the running boot."""
        return {"warnings": [], "firewall": "loaded",
                "boot": {"id": self.boot_id, "slot": self.running,
                         "confirmed": self.confirmed, "previous": self.previous}}

    def version(self) -> dict[str, object]:
        """Return the /api/version body with the running slot's build."""
        app = self.app or self.builds[self.running]
        return {"version": "app-v" + app, "os": self.builds[self.running], "app": app,
                "commit": "0123abcd", "updatable": True, "updateMode": "firmware", "api": 1}

    def get(self, path: str) -> Answer:
        """Answer GET path: (status, JSON body)."""
        self.requests.append(("GET", path, None))
        answers = {"/api/health": self.health, "/api/version": self.version,
                   "/api/volume": lambda: {"volume": self.volume}}
        if path not in answers:
            return 404, {"error": "not found"}
        return 200, answers[path]()

    def post(self, path: str, query: str, headers: Headers, body: bytes) -> Answer:
        """Answer POST path: (status, JSON body). A dangerous one waits for the hold first."""
        gated = path in ("/api/reboot", "/api/soundtapd") or (
            path == "/api/firmware" and parse_qs(query).get("install") == ["1"])
        how = self.gate(headers.get("X-SoundTap-Device-Token")) if gated else "open"
        self.tick()  # the boot went on while the request waited for the hold
        self.requests.append(("POST", path, how))
        if how is None:
            return 403, {"error": "hold a button on the speaker to confirm", "code": "not_confirmed"}
        answers: dict[str, Callable[[Headers, bytes], Answer]] = {
            "/api/volume": self._post_volume, "/api/reboot": self._post_reboot,
            "/api/firmware": self._post_firmware, "/api/soundtapd": self._post_soundtapd}
        if path not in answers:
            return 404, {"error": "not found"}
        return answers[path](headers, body)

    def _post_volume(self, _headers: Headers, body: bytes) -> Answer:
        self.volume = json.loads(body)["volume"]
        return 200, {"volume": self.volume}

    def _post_reboot(self, _headers: Headers, body: bytes) -> Answer:
        if json.loads(body or b"{}").get("confirm") is not True:
            return 400, {"error": "confirm"}
        self._reset("reboot", self.reboot_delay)
        return 200, {"status": "rebooting"}

    def _post_firmware(self, headers: Headers, body: bytes) -> Answer:
        if headers.get("X-SoundTap-Confirm") != "install" or not headers.get("X-SoundTap-Signature"):
            return 400, {"error": "unsigned or unconfirmed"}
        target = other(self.running)
        self.builds[target] = body.decode().strip()
        self.selected = target
        # The selection's write: upgrade_available, and the count starts over.
        self.unconfirmed, self.count = True, 0
        return 200, {"slot": target, "runningSlot": self.running}

    def _post_soundtapd(self, headers: Headers, body: bytes) -> Answer:
        if not headers.get("X-SoundTap-Signature"):
            return 400, {"error": "unsigned"}
        self.app = body.decode().strip()
        return 200, {"status": "restarting"}


def make_handler(sp: FakeSpeaker) -> type[BaseHTTPRequestHandler]:
    """Return a request handler class that serves sp."""

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args: object) -> None:
            pass

        def reply(self, code: int, body: dict[str, object]) -> None:
            data = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def off_network(self) -> bool:
            """Close the connection unanswered while the speaker resets, as a resetting speaker does; return whether it did."""
            sp.tick()
            if sp.down():
                self.close_connection = True
                return True
            return False

        def do_GET(self) -> None:
            with sp.cond:
                if not self.off_network():
                    self.reply(*sp.get(urlsplit(self.path).path))

        def do_POST(self) -> None:
            body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
            url = urlsplit(self.path)
            with sp.cond:
                if not self.off_network():
                    self.reply(*sp.post(url.path, url.query, self.headers, body))

    return Handler


def serve(sp: FakeSpeaker, port: int = 0) -> ThreadingHTTPServer:
    """Start the fake on 127.0.0.1 in a thread; return the server (server_address[1])."""
    httpd = ThreadingHTTPServer(("127.0.0.1", port), make_handler(sp))
    httpd.daemon_threads = True
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd


def main() -> None:
    """Serve a fake speaker until Ctrl-C, holding a key at each Enter."""
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--port", type=int, default=8080)
    ap.add_argument("--confirm-after", type=float, default=60.0)
    opts = ap.parse_args()
    sp = FakeSpeaker(confirm_after=opts.confirm_after, down_for=5.0, hold_window=60.0)
    httpd = serve(sp, opts.port)
    print(f"fake speaker on 127.0.0.1:{httpd.server_address[1]}; Enter holds a key, Ctrl-C ends")
    try:
        while True:
            input()
            print("held" if sp.press(timeout=0.1) else "nothing waits for a hold")
    except (KeyboardInterrupt, EOFError):
        httpd.shutdown()


if __name__ == "__main__":
    main()
