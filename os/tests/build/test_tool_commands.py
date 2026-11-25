# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every bluetoothctl call in the overlay and soundtapd is in tool_commands' list, and nothing calls wpa_cli.

soundtapd speaks to wpa_supplicant over its control socket.
"""
import re
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "support"))
import tool_commands

ROOT = Path(__file__).resolve().parents[3]
# A call starts a command: line start, after $( | ; & ! if, or after `timeout N`. A log
# message that names the tool ("bluetoothctl not found") does not.
AT = r"(?:^|[$(|;&!]|\bif|\btimeout\s+\d+)\s*"
SHELL = [(re.compile(AT + r"bluetoothctl\s+(?:--\w+\s+\S+\s+)?([a-z][a-z-]*)"), "usr/bin/bluetoothctl"),
         (re.compile(AT + r"wpa_cli\s+-i\s+\S+\s+([a-z_]+)"), "usr/sbin/wpa_cli")]
GO = [(re.compile(r'"bluetoothctl",\s*"([a-z-]+)"'), "usr/bin/bluetoothctl"),
      (re.compile(r'"wpa_cli",\s*"-i",\s*\w+,\s*"([a-z_]+)"'), "usr/sbin/wpa_cli")]


def calls() -> set[tuple[str, str]]:
    """Return the (tool path, command) pairs the overlay scripts and soundtapd call."""
    found = set()
    for path in (ROOT / "os/overlay").rglob("*"):
        if path.is_file() and path.suffix in ("", ".sh"):
            for line in path.read_text(errors="replace").splitlines():
                if not line.lstrip().startswith("#"):
                    found |= {(tool, m) for rx, tool in SHELL for m in rx.findall(line)}
    for path in (ROOT / "app").rglob("*.go"):
        if not path.name.endswith("_test.go"):
            text = path.read_text()
            found |= {(tool, m) for rx, tool in GO for m in rx.findall(text)}
    return found


class ToolCommands(unittest.TestCase):
    """The image's tool command list covers every call."""

    def test_every_call_is_listed(self) -> None:
        """Every bluetoothctl call is listed and nothing calls wpa_cli."""
        found = calls()
        self.assertIn(("usr/bin/bluetoothctl", "devices"), found)  # the scan itself works
        self.assertEqual([c for c in found if c[0] == "usr/sbin/wpa_cli"], [])
        unlisted = sorted(c for c in found if c[0] in tool_commands.COMMANDS
                          and c[1] not in tool_commands.COMMANDS[c[0]])
        self.assertEqual(unlisted, [], "add these to os/support/tool_commands.py COMMANDS")

    def test_missing_command_fails(self) -> None:
        """A tool binary that lacks a listed command is reported."""
        with tempfile.TemporaryDirectory() as tmp:
            for rel, commands in tool_commands.COMMANDS.items():
                path = Path(tmp) / rel
                path.parent.mkdir(parents=True, exist_ok=True)
                words = commands + tool_commands.ARGUMENTS.get(rel, [])
                path.write_bytes(b"".join(b"x-" + w.encode() + b"\0" for w in words))
            self.assertEqual(tool_commands.missing(tmp), [])
            bt = Path(tmp) / "usr/bin/bluetoothctl"
            bt.write_bytes(bt.read_bytes().replace(b"-devices\0", b"\0"))
            self.assertEqual(tool_commands.missing(tmp), ["/usr/bin/bluetoothctl has no 'devices'"])


if __name__ == "__main__":
    unittest.main()
