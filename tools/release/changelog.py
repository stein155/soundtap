#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Refuse a release whose tag has no section in CHANGELOG.md (RELEASING.md, Changelog).

    changelog.py check os-v0.1.0 [--commit <sha>] [--root <checkout>]

Every tag, on either lane, gets its own section, headed by the tag and the release date:

    ## [os-v0.1.0] - 2026-12-01

with at least one `- ` entry under it. An alpha's tag (`os-v0.1.0-alpha.1`) gets its own too. With --commit the file is read from that commit, as
dry-run.sh does; without it, from the working tree.
"""
import argparse
import re
import subprocess
import sys
from pathlib import Path

TAG = re.compile(r"^(os|app)-v\d+\.\d+\.\d+(-alpha\.[1-9]\d*)?$")
HEADING = re.compile(r"^## \[(?P<tag>[^\]]+)\](?: - (?P<date>\S+))?\s*$")
DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")


def section(text, tag):
    """The body of tag's section; ValueError when it is missing, undated or empty."""
    if not TAG.match(tag):
        raise ValueError(f"{tag!r} is not a release tag (os-vX.Y.Z or app-vX.Y.Z, with -alpha.N for an alpha)")
    lines = text.splitlines()
    starts = [(n, HEADING.match(line)) for n, line in enumerate(lines)]
    starts = [(n, m) for n, m in starts if m]
    found = [(n, m) for n, m in starts if m.group("tag") == tag]
    if not found:
        raise ValueError(f"CHANGELOG.md has no '## [{tag}] - YYYY-MM-DD' section")
    if len(found) > 1:
        raise ValueError(f"CHANGELOG.md has {len(found)} sections for {tag}")
    n, m = found[0]
    if not m.group("date") or not DATE.match(m.group("date")):
        raise ValueError(f"the {tag} section needs its release date: '## [{tag}] - YYYY-MM-DD'")
    ends = [k for k, _ in starts if k > n] + [len(lines)]
    body = lines[n + 1:ends[0]]
    if not any(line.lstrip().startswith("- ") for line in body):
        raise ValueError(f"the {tag} section lists no changes")
    return "\n".join(body).strip() + "\n"


def read(root, commit):
    if commit:
        return subprocess.run(["git", "-C", str(root), "show", f"{commit}:CHANGELOG.md"],
                              check=True, capture_output=True, text=True).stdout
    return (root / "CHANGELOG.md").read_text()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("command", choices=["check"])
    parser.add_argument("tag")
    parser.add_argument("--commit")
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    args = parser.parse_args(argv)
    try:
        section(read(args.root, args.commit), args.tag)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        detail = getattr(error, "stderr", "") or ""
        print(f"changelog.py: {error} {detail}".rstrip(), file=sys.stderr)
        return 1
    print(f"CHANGELOG.md has its {args.tag} section")
    return 0


if __name__ == "__main__":
    sys.exit(main())
