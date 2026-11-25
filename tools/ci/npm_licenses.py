#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Bundle the licenses of every npm package in package-lock.json.

The frontend is built in the app lane with `npm ci` (ADR 0011), not by Buildroot, so the
lock's SHA-512 integrities are what pins the dependencies. This writes the license texts
the shipped bundle owes its dependencies into app/frontend/THIRD_PARTY_LICENSES.txt, and
--check fails when that file was made from another lock.
"""

import argparse
import base64
import hashlib
import json
import tarfile
import urllib.parse
import urllib.request
from collections import defaultdict
from pathlib import Path

# A licence file at a package's root: "package/LICENSE" in the tarball.
PACKAGE_ROOT_DEPTH = 2


def package_tarballs(lock_path: Path) -> list[dict]:
    """Return each package the lock resolves, with its tarball URL and SHA-512 integrity, sorted by URL."""
    lock = json.loads(lock_path.read_text())
    tarballs = []
    for location, package in lock["packages"].items():
        url = package.get("resolved")
        integrity = package.get("integrity")
        if not url:
            continue
        if not integrity or not integrity.startswith("sha512-"):
            msg = f"missing SHA-512 integrity for {url}"
            raise ValueError(msg)
        name = Path(urllib.parse.urlparse(url).path).name
        digest = base64.b64decode(integrity.removeprefix("sha512-"))
        tarballs.append({
            "url": url,
            "file": name,
            "integrity": digest,
            "package": location.removeprefix("node_modules/"),
            "version": package.get("version", "unknown"),
            "license": package.get("license", "unknown"),
        })

    return sorted(tarballs, key=lambda package: package["url"])


def package_dir(tarball: dict) -> str:
    """Scoped and unscoped tarballs can share a basename (@types/react, react)."""
    return "scoped" if urllib.parse.urlparse(tarball["url"]).path.startswith("/@") else "plain"


def download(url: str, path: Path, expected: bytes) -> bytes:
    """Return the tarball at path, downloading it from url first if needed; fail unless its SHA-512 is expected."""
    if path.exists():
        data = path.read_bytes()
    else:
        with urllib.request.urlopen(url, timeout=120) as response:
            data = response.read()
        path.write_bytes(data)
    if hashlib.sha512(data).digest() != expected:
        path.unlink(missing_ok=True)
        msg = f"npm integrity mismatch for {url}"
        raise ValueError(msg)
    return data


def license_bundle(tarballs: list[dict], download_dir: Path, lock_digest: str) -> str:
    """Return the licence bundle: each licence text once, with the packages that carry it."""
    licenses = defaultdict(list)
    for package in tarballs:
        archive = download_dir / package_dir(package) / package["file"]
        with tarfile.open(archive, "r:gz") as source:
            candidates = [
                member for member in source.getmembers()
                if member.isfile()
                and len(Path(member.name).parts) == PACKAGE_ROOT_DEPTH
                and Path(member.name).name.lower().startswith(
                    ("license", "copying", "notice", "unlicense"),
                )
            ]
            texts = []
            for member in sorted(candidates, key=lambda item: item.name):
                extracted = source.extractfile(member)
                if extracted:
                    texts.append(extracted.read().decode("utf-8", errors="replace").strip())
        text = "\n\n".join(filter(None, texts))
        if not text:
            text = f"Declared package license: {package['license']}"
        licenses[text].append(f"{package['package']} {package['version']}")

    sections = [
        "SoundTap frontend third-party licenses",
        f"package-lock sha256: {lock_digest}",
        "Generated from the integrity-checked npm archives.",
    ]
    for text, packages in sorted(licenses.items(), key=lambda item: item[1]):
        sections.extend(("", "Packages: " + ", ".join(sorted(packages)), "", text))
    return "\n".join(sections).rstrip() + "\n"


def main() -> None:
    """Write the licence bundle, or with --check fail when it was made from another lock."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--lock", type=Path, required=True)
    parser.add_argument("--licenses", type=Path, required=True)
    parser.add_argument("--download-dir", type=Path)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()

    lock_digest = hashlib.sha256(args.lock.read_bytes()).hexdigest()
    if args.check:
        if args.licenses.read_text().splitlines()[1] != f"package-lock sha256: {lock_digest}":
            msg = "npm license bundle is stale: rerun tools/ci/npm_licenses.py"
            raise SystemExit(msg)
        return
    if args.download_dir is None:
        parser.error("--download-dir is required to write the bundle")
    tarballs = package_tarballs(args.lock)
    for package in tarballs:
        path = args.download_dir / package_dir(package) / package["file"]
        path.parent.mkdir(parents=True, exist_ok=True)
        download(package["url"], path, package["integrity"])
    args.licenses.write_text(license_bundle(tarballs, args.download_dir, lock_digest))


if __name__ == "__main__":
    main()
