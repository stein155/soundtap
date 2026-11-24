#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Fail when a binary in the source tree or in the image has no provenance entry.

os/upstream/MANIFEST.json lists every binary file we ship but do not compile from text:
origin (upstream, generated or original), source, version, license and the sha256 of each
file. "files" paths are repository-relative, or start with "/" for a file in the image;
"globs" cover files a tool regenerates on every run, where a pinned hash would only churn.
The source is a URL or a path to the tool that makes the file, and never a Bose product:
SoundTap is built without Bose material (ADR 0049).

In the image every binary file is judged, except ELF programs, libraries and modules, which
Buildroot compiles from pinned source. What the build makes from our tree or a pinned tool
(kernel and device trees, module indexes, terminfo, the BCO app) is covered by "generated"
globs, so a prebuilt file that slips in without an entry fails the build.

  check_provenance.py [--rootfs DIR]     the tree always; the whole image with --rootfs
"""
import argparse
import fnmatch
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "os/upstream/MANIFEST.json"
ORIGINS = {"upstream", "generated", "original"}
# Build and download output, not the repository's own files.
SKIP = {".git", "node_modules", "buildroot", "dl", "output", "artifacts", ".cache", "dist", "__pycache__", ".gocache", "coverage"}
# Image directories that are generated at run time on the speaker or hold no files of the
# build's own: nothing to judge there.
IMAGE_SKIP = {"dev", "proc", "sys", "tmp", "run", "mnt", "var/run"}


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def is_binary(path: Path) -> bool:
    with open(path, "rb") as f:
        return b"\0" in f.read(8000)


def is_elf(path: Path) -> bool:
    with open(path, "rb") as f:
        return f.read(4) == b"\x7fELF"


def image_files(rootfs: Path):
    """Every regular file of the image, outside IMAGE_SKIP, in a stable order."""
    for top, dirs, names in os.walk(rootfs):
        rel = Path(top).relative_to(rootfs).as_posix()
        dirs[:] = sorted(d for d in dirs if (d if rel == "." else f"{rel}/{d}") not in IMAGE_SKIP)
        for n in sorted(names):
            path = Path(top, n)
            if path.is_file() and not path.is_symlink():
                yield path


def tree_files(tree):
    """Tracked files; a tree without git (the test container) is walked, minus build output."""
    try:
        out = subprocess.run(["git", "-C", str(tree), "ls-files", "-z"], capture_output=True, check=True).stdout
        return out.decode().split("\0")[:-1]
    except (OSError, subprocess.CalledProcessError):
        found = []
        for top, dirs, names in os.walk(tree):
            dirs[:] = [d for d in dirs if d not in SKIP]
            found += [str(Path(top, n).relative_to(tree)) for n in names]
        return found


def entry_errors(manifest) -> list:
    errors = []
    for i, e in enumerate(manifest):
        name = f"entry {i} ({e.get('source', '?')[:40]})"
        for key in ("source", "version", "license"):
            if not e.get(key):
                errors.append(f"{name}: missing {key}")
        if e.get("origin") not in ORIGINS:
            errors.append(f"{name}: origin must be one of {sorted(ORIGINS)}")
        if re.search(r"bose|soundtouch", e.get("source", ""), re.I):
            errors.append(f"{name}: a Bose product is not an allowed source")
        if not e.get("files") and not e.get("globs"):
            errors.append(f"{name}: lists no files")
        for path, digest in e.get("files", {}).items():
            if not re.fullmatch(r"[0-9a-f]{64}", digest):
                errors.append(f"{name}: {path} has no sha256")
    return errors


def check(manifest, tree=ROOT, rootfs=None) -> list:
    errors = entry_errors(manifest)
    files = {p: d for e in manifest for p, d in e.get("files", {}).items()}
    globs = [g for e in manifest for g in e.get("globs", [])]
    seen = set()

    def verify(where, path, key):
        seen.add(key)
        if key in files and sha(path) != files[key]:
            errors.append(f"WRONG HASH: {where} is {sha(path)[:12]}, the manifest says {files[key][:12]}")
        elif key not in files and not any(fnmatch.fnmatch(key, g) for g in globs):
            errors.append(f"NO PROVENANCE: {where} is not in os/upstream/MANIFEST.json")

    for rel in tree_files(tree):
        path = Path(tree, rel)
        if path.is_file() and not path.is_symlink() and is_binary(path):
            verify(rel, path, rel)
    if rootfs is not None:
        for path in image_files(Path(rootfs)):
            if is_elf(path) or not is_binary(path):
                continue
            rel = path.relative_to(rootfs).as_posix()
            verify("image " + rel, path, "/" + rel)
    for path in files:
        if path.startswith("/") and rootfs is None:
            continue  # an image file is only judged against an image
        if path not in seen:
            errors.append(f"STALE: {path} is in the manifest but not in the tree or image")
    return errors


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--rootfs", type=Path)
    args = ap.parse_args(argv)
    errors = check(json.loads(MANIFEST.read_text()), rootfs=args.rootfs)
    for line in errors:
        print(line, file=sys.stderr)
    if not errors:
        print("provenance: every binary has a manifest entry with a matching hash")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
