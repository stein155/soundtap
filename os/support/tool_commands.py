#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every bluetoothctl subcommand the speaker uses exists in the image.

soundtapd speaks to wpa_supplicant over its control socket (app/internal/wpactrl), so no
wpa_cli subcommand is relied on any more.

BlueZ 5.79 dropped `bluetoothctl paired-devices` and nothing noticed until a speaker kept
its old pairings. This reads the binaries in a built rootfs and fails on a command
they do not carry.

A command counts as present when its name ends a NUL-terminated string in the binary. The
linker merges a string into the tail of a longer one ("devices" lives in "clr-devices"), so
an exact match misses real commands.

    python3 os/support/tool_commands.py <rootfs>
"""
import sys
from pathlib import Path

# tail match, so a removed command whose name ends another string still passes;
# running each tool's help under qemu would be exact, but bluetoothctl waits for D-Bus.

# Subcommands, including the ones bt-ctl.sh and bt-nowplaying.sh feed on stdin.
COMMANDS = {
    "usr/bin/bluetoothctl": [
        "devices", "connect", "disconnect", "info", "remove", "menu", "player", "play", "pause", "next",
        "previous", "show", "quit",
    ],
}
# Arguments the scripts pass, found anywhere in the binary's strings.
ARGUMENTS = {"usr/bin/bluetoothctl": ["Connected"]}
USAGE_ARGC = 2


def missing(rootfs: str | Path) -> list[str]:
    """Return one problem per tool, subcommand or argument the rootfs lacks."""
    out = []
    for rel, commands in COMMANDS.items():
        path = Path(rootfs) / rel
        if not path.is_file():
            out.append(f"/{rel} is not in the image")
            continue
        data = path.read_bytes()
        out += [f"/{rel} has no '{c}'" for c in commands if c.encode() + b"\0" not in data]
        out += [f"/{rel} does not know '{a}'" for a in ARGUMENTS.get(rel, [])
                if a.encode() not in data]
    return out


if __name__ == "__main__":
    if len(sys.argv) != USAGE_ARGC:
        sys.exit("usage: tool_commands.py <rootfs>")
    if problems := missing(sys.argv[1]):
        sys.exit("TOOL COMMANDS: " + "; ".join(problems))
    print("TOOL COMMANDS OK: every bluetoothctl subcommand is in the image")
