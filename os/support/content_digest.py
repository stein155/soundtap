#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Print a deterministic SHA-256 digest for files and directory trees."""

import argparse
import hashlib
import os
import stat
import tempfile
from pathlib import Path


def _excluded(relative: str, exclusions: tuple[str, ...]) -> bool:
    return any(
        relative == exclusion or relative.startswith(exclusion + "/")
        for exclusion in exclusions
    )


def _entry_digest(
    digest, root: Path, relative: str, exclusions: tuple[str, ...]
) -> None:
    if _excluded(relative, exclusions):
        return

    path = root / relative if relative else root
    metadata = path.lstat()
    mode = stat.S_IMODE(metadata.st_mode)
    name = relative.encode("utf-8", errors="surrogateescape")

    if stat.S_ISLNK(metadata.st_mode):
        digest.update(b"L\0" + name + b"\0" + f"{mode:o}".encode() + b"\0")
        target = os.readlink(path).encode("utf-8", errors="surrogateescape")
        digest.update(target + b"\0")
        return
    if stat.S_ISREG(metadata.st_mode):
        digest.update(b"F\0" + name + b"\0" + f"{mode:o}".encode() + b"\0")
        with path.open("rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
        digest.update(b"\0")
        return
    if stat.S_ISDIR(metadata.st_mode):
        digest.update(b"D\0" + name + b"\0" + f"{mode:o}".encode() + b"\0")
        for child in sorted(path.iterdir(), key=lambda item: os.fsencode(item.name)):
            child_relative = f"{relative}/{child.name}" if relative else child.name
            _entry_digest(digest, root, child_relative, exclusions)
        return
    raise ValueError(f"unsupported file type: {path}")


def content_digest(paths: list[Path], exclusions: tuple[str, ...] = ()) -> str:
    """Return a digest independent of source location and mtimes."""
    digest = hashlib.sha256()
    for index, path in enumerate(paths):
        if not path.exists() and not path.is_symlink():
            raise FileNotFoundError(path)
        digest.update(b"R\0" + str(index).encode() + b"\0")
        _entry_digest(digest, path, "", exclusions)
    return digest.hexdigest()


def update_stamp(path: Path, value: str) -> None:
    """Atomically update path only when its stored digest differs."""
    contents = value + "\n"
    try:
        if path.read_text() == contents:
            return
    except FileNotFoundError:
        pass

    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        mode="w", dir=path.parent, prefix=path.name + ".", delete=False
    ) as temporary:
        temporary.write(contents)
        temporary_path = Path(temporary.name)
    os.replace(temporary_path, path)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths", nargs="+", type=Path)
    parser.add_argument(
        "--exclude",
        action="append",
        default=[],
        help="relative file or subtree to omit (directory inputs only)",
    )
    parser.add_argument(
        "--stamp", type=Path, help="update this digest stamp atomically"
    )
    args = parser.parse_args()
    exclusions = tuple(item.strip("/") for item in args.exclude)
    value = content_digest(args.paths, exclusions)
    if args.stamp:
        update_stamp(args.stamp, value)
    print(value)


if __name__ == "__main__":
    main()
