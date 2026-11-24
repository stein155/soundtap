#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Write the release notes for one SoundTap release (ADR 0035).

    notes.py os  0.1.0 SoundTap-Core-0.1.0.stu  <sha256> [previous-tag]
                 [--preview SoundTap-Core-0.1.0-preview.stu <sha256>]
    notes.py app 0.1.0 SoundTap-app-0.1.0-armv7.bin <sha256> [previous-tag] --os-api N

The notes say what the release is, how to install it, its checksum, and what changed since
the previous release of the same lane, grouped from the commit subjects.
"""
import argparse
import re
import subprocess

GROUPS = [
    ("Fixes", re.compile(r"^(fix|fixes|fixed)\b|\bfix(es)?\b", re.I)),
    ("Build and CI", re.compile(r"^(build|ci|deps?|chore)\b", re.I)),
    ("Documentation", re.compile(r"^docs?\b", re.I)),
]
OTHER = "Changes"


def group(subject):
    for name, rx in GROUPS:
        if rx.search(subject):
            return name
    return OTHER


def changes(previous, paths):
    if not previous:
        return []
    out = subprocess.run(
        ["git", "log", "--no-merges", "--format=%s", f"{previous}..HEAD", "--", *paths],
        check=True, capture_output=True, text=True,
    ).stdout
    return [s.strip() for s in out.splitlines() if s.strip()]


def render(lane, version, asset, sha256, previous, subjects, os_api=None, preview=None):
    """preview: (asset, sha256) of the preview package (ADR 0133), on the os lane."""
    if lane == "os":
        title = f"SoundTap Core {version}"
        intro = ("One update for every supported speaker: the Bose SoundTouch 10, the SoundTouch 20 "
                 "and the SoundTouch Wireless Link Adapter. It replaces the whole system on the "
                 "speaker and restarts it once.")
        install = [
            f"1. Download **{asset}** and **{asset}.sig** below. The speaker refuses a package "
            "without its signature.",
            "2. Open the speaker's SoundTap page in a browser (`http://<speaker>/`), go to "
            "**Settings → Updates → SoundTap Core** and choose both files together.",
            "3. The speaker installs, restarts and comes back on the new version. Your settings stay.",
            "",
            "Still on Bose firmware? Follow "
            "[installing the universal package](../../blob/main/docs/owners/install.md).",
        ]
    else:
        title = f"SoundTap app {version}"
        intro = ("The SoundTap app on its own: the player and the web page, without a new system. "
                 "It installs in seconds and needs no restart of the speaker.")
        install = [
            f"1. Download **{asset}** and **{asset}.sig** below. The speaker refuses an app "
            "without its signature.",
            "2. Open the speaker's SoundTap page, go to **Settings → Updates → App** and choose "
            "both files together.",
            f"3. It needs SoundTap Core level {os_api} or newer; the speaker refuses it otherwise.",
        ]
    if "-alpha." in version:
        # ADR 0035: published before the hardware approval of a release, as a prerelease.
        intro += ("\n\n**This is an alpha**, a prerelease: it is signed like a release but has not "
                  "passed the release's hardware tests on every model. Speakers on an alpha are "
                  "offered newer alphas and releases; speakers on a release are never offered an alpha.")
    lines = [f"## {title}", "", intro, "", "### Install", "", *install, ""]
    lines += ["### What changed", ""]
    if not previous:
        lines.append(f"The first {'SoundTap Core' if lane == 'os' else 'SoundTap app'} release in this numbering.")
    elif not subjects:
        lines.append(f"Nothing since {previous}.")
    else:
        by = {}
        for s in subjects:
            by.setdefault(group(s), []).append(s)
        for name in [OTHER] + [g for g, _ in GROUPS]:
            if by.get(name):
                lines += [f"**{name}**", "", *[f"- {s}" for s in by[name]], ""]
        lines.append(f"Compared with {previous}.")
    if preview:
        lines += ["", "### Preview package", "",
                  f"**{preview[0]}** is the same release for the preview models (ADR 0133): models "
                  "SoundTap was worked out for from Bose's own software, which nobody has run it on "
                  "yet. Only it installs on them; the installers send a preview model to it after "
                  "their extra confirmation. On every other model it installs the same system as "
                  f"**{asset}**. It has its own **{preview[0]}.sig**."]
    lines += ["", "### Checksum", "", f"`{sha256}  {asset}`"]
    if preview:
        lines.append(f"`{preview[1]}  {preview[0]}`")
    lines.append("")
    return "\n".join(lines)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("lane", choices=["os", "app"])
    p.add_argument("version")
    p.add_argument("asset")
    p.add_argument("sha256")
    p.add_argument("previous", nargs="?", default="")
    p.add_argument("--os-api")
    p.add_argument("--preview", nargs=2, metavar=("ASSET", "SHA256"))
    a = p.parse_args()
    # The app lane only changes with soundtap/; Core carries everything.
    paths = ["app"] if a.lane == "app" else ["."]
    print(render(a.lane, a.version, a.asset, a.sha256, a.previous, changes(a.previous, paths), a.os_api,
                 a.preview))


if __name__ == "__main__":
    main()
