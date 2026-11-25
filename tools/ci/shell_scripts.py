#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every tracked shell script, NUL-separated, for `make lint-shell`.

    shell_scripts.py [repo root]

A script is a tracked *.sh, or a tracked file whose first line is a sh, bash, ash or dash
shebang: the speaker's /init, init.d and udhcpc hooks carry no extension. Submodules are
left out, and so are the vendored Go modules (app/vendor, ADR 0124): upstream's maintainer
scripts, kept byte for byte, never run on the speaker or in the build. In Python rather than awk and xargs so the list is the same on GNU and BSD hosts.
"""

import re
import subprocess
import sys
from pathlib import Path

SHEBANG = re.compile(rb"#!.*[/ ](ba|a|da)?sh( |$)")


def scripts(root: Path) -> list[str]:
    """Return the tracked shell scripts under root, by name or shebang, without submodules or vendored modules."""
    listed = subprocess.run(["git", "-C", str(root), "ls-files", "-s", "-z"],
                            capture_output=True, check=True).stdout
    out = []
    for entry in listed.split(b"\0"):
        if not entry:
            continue
        meta, name = entry.split(b"\t", 1)
        if meta.startswith(b"160000"):
            continue
        path = name.decode()
        if path.startswith("app/vendor/"):
            continue
        if path.endswith(".sh"):
            out.append(path)
            continue
        try:
            with (root / path).open("rb") as f:
                first = f.readline(256).rstrip(b"\r\n")
        except OSError:
            continue
        if SHEBANG.match(first):
            out.append(path)
    return out


def main(argv: list[str]) -> int:
    """Write the shell scripts of the repository root in argv[1], NUL-separated, to stdout."""
    root = Path(argv[1] if len(argv) > 1 else Path(__file__).resolve().parents[2])
    sys.stdout.write("".join(f"{p}\0" for p in scripts(root)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
