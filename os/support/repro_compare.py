#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Compares two builds of one tree (ADR 0011: same commit and SOURCE_DATE_EPOCH, same bytes).

    repro_compare.py save IMAGES DEST [--extra FILE ...]   keep one build's images
    repro_compare.py compare A B                          two saved builds; exit 1 if they differ
    repro_compare.py manifest TREE                        one line per path of TREE

`save` checks IMAGES/SHA256SUMS against the files, copies them and the list to DEST, and
writes DEST/rootfs-tree.manifest from IMAGES/rootfs-tree, the tree mkfs.ubifs packed
(os/board/rootfs/preserve-rootfs-tree.sh). Each --extra file (the STU) is copied and listed
in DEST/extra.SHA256SUMS. `compare` reads every *SHA256SUMS of both and names each file that
differs. When rootfs.ubi differs it lists the paths whose type, mode, size, content or link
target differ, from the two manifests, so a difference is a file name and not 90 MB of UBI.
The manifest leaves out owners and times: rootfs-tree is a copy made under fakeroot, which
keeps neither, and mkfs.ubifs takes every time from --timestamp.
"""

import argparse
import hashlib
import os
import shutil
import stat
import sys
from pathlib import Path

SUMS = "SHA256SUMS"
EXTRA_SUMS = "extra.SHA256SUMS"
MANIFEST = "rootfs-tree.manifest"
ROOTFS = "rootfs.ubi"
SHA256_HEX_LEN = 64


def sha256(path: Path) -> str:
    """Return the SHA-256 of the file at path, in hex."""
    digest = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def read_sums(path: Path) -> dict[str, str]:
    """Name -> digest, from sha256sum's output ("digest  name" or "digest *name")."""
    sums = {}
    for n, line in enumerate(path.read_text().splitlines(), 1):
        if not line.strip():
            continue
        digest, sep, name = line.partition(" ")
        if not sep or len(digest) != SHA256_HEX_LEN or not name:
            msg = f"{path}:{n}: not a sha256sum line: {line!r}"
            raise ValueError(msg)
        sums[name[1:] if name[:1] in " *" else name] = digest
    return sums


def manifest(tree: Path) -> list[str]:
    """Sorted lines "type mode size content<TAB>path" for every path below tree."""
    lines = []
    for top, dirs, files in os.walk(tree):
        dirs.sort()
        for name in sorted(dirs + files):
            path = Path(top, name)
            st = path.lstat()
            mode = f"{stat.S_IMODE(st.st_mode):04o}"
            if stat.S_ISLNK(st.st_mode):
                kind, size, content = "l", "-", os.readlink(path)
            elif stat.S_ISDIR(st.st_mode):
                kind, size, content = "d", "-", "-"
            elif stat.S_ISREG(st.st_mode):
                kind, size, content = "f", str(st.st_size), sha256(path)
            else:  # fakeroot's device nodes are empty files on disk; a fifo or socket is rare
                kind, size, content = "o", "-", "-"
            rel = path.relative_to(tree).as_posix()
            lines.append(f"{kind} {mode} {size} {content}\t{rel}")
    return sorted(lines, key=lambda line: line.split("\t", 1)[1])


def parse_manifest(path: Path) -> dict[str, tuple[str, ...]]:
    """Return path -> (type, mode, size, content) from a manifest file."""
    entries = {}
    for line in path.read_text().splitlines():
        attrs, _, rel = line.partition("\t")
        entries[rel] = tuple(attrs.split(" ", 3))
    return entries


def save(images: Path, dest: Path, extra: list[Path]) -> None:
    """Check IMAGES against its SHA256SUMS and copy it, its manifest and the extra files to dest."""
    sums = read_sums(images / SUMS)
    bad = [name for name, digest in sums.items()
           if not (images / name).is_file() or sha256(images / name) != digest]
    if bad:
        msg = f"{images}/{SUMS} does not match: {', '.join(bad)}"
        raise SystemExit(msg)
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True)
    shutil.copy2(images / SUMS, dest / SUMS)
    for name in sums:
        (dest / name).parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(images / name, dest / name)
    tree = images / "rootfs-tree"
    if tree.is_dir():
        (dest / MANIFEST).write_text("".join(f"{line}\n" for line in manifest(tree)))
    if extra:
        lines = []
        for path in extra:
            shutil.copy2(path, dest / path.name)
            lines.append(f"{sha256(path)}  {path.name}\n")
        (dest / EXTRA_SUMS).write_text("".join(lines))


def compare_manifests(a: dict, b: dict) -> list[str]:
    """Return one line per path that is only in one manifest or differs between them."""
    fields = ("type", "mode", "size", "content")
    out = []
    for rel in sorted(a.keys() | b.keys()):
        if rel not in b:
            out.append(f"only in A: /{rel}")
        elif rel not in a:
            out.append(f"only in B: /{rel}")
        elif a[rel] != b[rel]:
            changed = [f for f, x, y in zip(fields, a[rel], b[rel], strict=False) if x != y]
            out.append(f"differs: /{rel} ({', '.join(changed)})")
    return out


def compare(a: Path, b: Path) -> tuple[bool, list[str]]:
    """Return whether the two saved builds are the same, and the report lines."""
    names = sorted({p.name for d in (a, b) for p in d.glob(f"*{SUMS}")})
    if SUMS not in names:
        msg = f"neither {a} nor {b} holds {SUMS}"
        raise SystemExit(msg)
    report, differ = [], set()
    for sums_name in names:
        if not (a / sums_name).is_file() or not (b / sums_name).is_file():
            missing = a if not (a / sums_name).is_file() else b
            report.append(f"{sums_name}: missing in {missing}")
            differ.add(sums_name)
            continue
        sa, sb = read_sums(a / sums_name), read_sums(b / sums_name)
        lines = []
        for name in sorted(sa.keys() | sb.keys()):
            if name not in sb:
                lines.append(f"  only in A: {name}")
            elif name not in sa:
                lines.append(f"  only in B: {name}")
            elif sa[name] != sb[name]:
                lines.append(f"  differs: {name}")
                differ.add(name)
        same = len([n for n in sa if sb.get(n) == sa[n]])
        report.append(f"{sums_name}: {same} of {len(sa.keys() | sb.keys())} files the same")
        report.extend(lines)
        if lines:
            differ.add(sums_name)
    if ROOTFS in differ:
        report.extend(rootfs_report(a, b))
    return not differ, report


def rootfs_report(a: Path, b: Path) -> list[str]:
    """Return the report lines naming the paths of the packed tree that differ."""
    ma, mb = a / MANIFEST, b / MANIFEST
    if not (ma.is_file() and mb.is_file()):
        return [f"{ROOTFS}: no {MANIFEST} in both; save both builds to list paths"]
    tree = compare_manifests(parse_manifest(ma), parse_manifest(mb))
    if not tree:
        return [f"{ROOTFS}: the packed trees are the same, so mkfs.ubifs or "
                "ubinize made the difference (UUID, timestamp, sequence number)"]
    return [f"{ROOTFS}: {len(tree)} paths of the packed tree differ", *(f"  {line}" for line in tree)]


def main() -> int:
    """Run the save, compare or manifest command and return the exit code."""
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("save")
    p.add_argument("images", type=Path)
    p.add_argument("dest", type=Path)
    p.add_argument("--extra", type=Path, action="append", default=[])
    p = sub.add_parser("compare")
    p.add_argument("a", type=Path)
    p.add_argument("b", type=Path)
    p = sub.add_parser("manifest")
    p.add_argument("tree", type=Path)
    args = parser.parse_args()
    try:
        if args.cmd == "save":
            save(args.images, args.dest, args.extra)
            return 0
        if args.cmd == "manifest":
            sys.stdout.write("".join(f"{line}\n" for line in manifest(args.tree)))
            return 0
        same, report = compare(args.a, args.b)
    except (OSError, ValueError) as error:
        print(f"repro_compare: {error}", file=sys.stderr)
        return 2
    print("\n".join(report))
    print("reproducible: the two builds are the same" if same
          else "NOT reproducible: the two builds differ")
    return 0 if same else 1


if __name__ == "__main__":
    sys.exit(main())
