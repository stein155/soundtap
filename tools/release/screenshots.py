#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The README screenshots name the web UI they were made from (RELEASING.md, Steps).

    screenshots.py ready  [--root DIR]           before make screenshots: the UI sources are committed
    screenshots.py record [--root DIR]           after it: write docs/assets/screenshots.source
    screenshots.py check <commit> [--root DIR]   the release guard: <commit>'s record names its own UI

`make screenshots` builds the web UI (`make web`) from this checkout, photographs it with a
fake speaker (docs/tools) and composes the README art into docs/assets. The record it leaves
next to the images holds the git tree of each input, one `<path> <tree>` line per path:
app/frontend, the UI itself, and docs/tools, the fake speaker and the shots. A release checks
the record against the commit it releases (release.yml, dry-run.sh), so a UI or tool change
since the screenshots were made fails until they are made again.

The record names source trees rather than a hash of the built dist: `make web` builds the dist
from app/frontend alone, with npm's lockfile and a Node image pinned by digest, and git names
that tree for any commit in one command. A dist hash would make the release check build the
UI again or unpack it from the rootfs. That is why `ready` refuses uncommitted changes under
those paths: the record names HEAD's trees, which must be what was built.
"""

import argparse
import subprocess
import sys
from pathlib import Path

SOURCES = ("app/frontend", "docs/tools")
RECORD = "docs/assets/screenshots.source"


class RefusedError(Exception):
    """A git command failed or the UI sources are not committed."""


def git(root: Path, *args: str) -> str:
    """Run git in root and return its output; raise RefusedError when it fails."""
    done = subprocess.run(["git", "-C", str(root), *args], check=False, capture_output=True, text=True)
    if done.returncode != 0:
        msg = f"git {' '.join(args)}: {done.stderr.strip()}"
        raise RefusedError(msg)
    return done.stdout.strip()


def trees(root: Path, rev: str) -> dict[str, str]:
    """Return the git tree of each source path at rev."""
    return {path: git(root, "rev-parse", f"{rev}:{path}") for path in SOURCES}


def parse(text: str) -> dict[str, str]:
    """Parse a record into its path to tree mapping, skipping comments and blank lines."""
    found = {}
    for line in text.splitlines():
        if line.strip() and not line.startswith("#"):
            path, _, tree = line.partition(" ")
            found[path] = tree.strip()
    return found


def ready(root: Path) -> None:
    """Raise RefusedError when the UI sources have uncommitted changes."""
    dirty = git(root, "status", "--porcelain", "--", *SOURCES)
    if dirty:
        raise RefusedError(
            "uncommitted changes under " + " and ".join(SOURCES)
            + ": commit them first, the record names HEAD's trees\n" + dirty)


def record(root: Path) -> str:
    """Write the record of HEAD's source trees and return its text."""
    ready(root)
    lines = [f"{path} {tree}" for path, tree in trees(root, "HEAD").items()]
    text = ("# Written by make screenshots (tools/release/screenshots.py): the trees the README\n"
            "# screenshots in this directory were made from. A release refuses another UI.\n"
            + "\n".join(lines) + "\n")
    (root / RECORD).write_text(text)
    return text


def check(root: Path, commit: str) -> list[str]:
    """Return the problems with commit's record; an empty list when it names commit's own trees."""
    try:
        made = parse(git(root, "show", f"{commit}:{RECORD}"))
    except RefusedError:
        return [f"{commit} has no {RECORD}: run make screenshots and commit docs/assets"]
    problems = []
    for path, tree in trees(root, commit).items():
        if made.get(path) != tree:
            problems.append(f"the screenshots were made from {path} {made.get(path) or '(none)'}, "
                            f"{commit} has {tree}")
    if problems:
        problems.append("run make screenshots on this commit's tree and commit docs/assets")
    return problems


def main(argv: list[str]) -> int:
    """Run the command given in argv and return the exit code."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("ready")
    sub.add_parser("record")
    sub.add_parser("check").add_argument("commit")
    args = parser.parse_args(argv)
    try:
        if args.command == "ready":
            ready(args.root)
        elif args.command == "record":
            print(record(args.root), end="")
        else:
            problems = check(args.root, args.commit)
            for problem in problems:
                print(f"screenshots.py: {problem}", file=sys.stderr)
            if problems:
                return 1
            print(f"README screenshots were made from {args.commit}'s web UI")
    except RefusedError as err:
        print(f"screenshots.py: {err}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
