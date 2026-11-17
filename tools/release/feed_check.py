#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The first-install feed names a published, signed SoundTap Core release.

A speaker still on Bose's software installs whatever `updates/soundtouch` on main names, and
Bose's updater checks only the length and the CRC32 the index states (ADR 0048: the first
install is unchanged). Whoever changes the index chooses the software. This check holds the
index to one release:

- every DEVICE of the supported models offers the same image;
- its URL is `https://github.com/<Repo>/releases/download/os-vX.Y.Z/SoundTap-Core-X.Y.Z.stu`,
  with `Repo` from `internal/update`;
- LENGTH and CRC are those of the asset;
- `<asset>.sig` states kind core, profile production, version X.Y.Z and the asset's SHA-256,
  and its signature verifies with a key compiled into soundtapd (`release.PublicKey` or
  `release.ReservePublicKey`; the empty placeholder is skipped).

X.Y.Z may be an alpha, X.Y.Z-alpha.N (ADR 0035): a published GitHub prerelease is a release
the feed may name, signed like any other.

    feed_check.py <index> [--assets DIR]

Without --assets it downloads the asset and its .sig from the URL the index names, which
works only for a published release, and asks GitHub that the release is not a draft and not
withdrawn (RELEASING.md); CI runs it so. With --assets it reads `<FILENAME>` and
`<FILENAME>.sig` from DIR and touches no network (dry-run.sh, the tests). Needs openssl with
ed25519. Exit 0 when the index passes, 1 with the reasons otherwise.
"""
import argparse
import base64
import hashlib
import json
import re
import subprocess
import sys
import tempfile
import urllib.request
import xml.etree.ElementTree as ET
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools/release"))
from mock_update_server import MODELS  # noqa: E402

HOST = "https://github.com"
VERSION = r"\d{1,9}\.\d{1,9}\.\d{1,9}(?:-alpha\.[1-9]\d{0,8})?"  # release.Version
SIG_MAX = 1024  # release.SigMax
# An ed25519 public key in DER: this fixed prefix and the 32 raw bytes.
DER_PREFIX = bytes.fromhex("302a300506032b6570032100")
WITHDRAWN = "Withdrawn:"  # update.WithdrawnMarker


class FeedError(Exception):
    pass


def go_const(path, name):
    m = re.search(rf'^const {name} = "(.*)"$', Path(path).read_text(), re.M)
    if not m:
        raise FeedError(f"{path} has no const {name}")
    return m.group(1)


def release_keys(root=ROOT):
    """The keys soundtapd verifies with, without the empty placeholder."""
    go = root / "app/internal/release/release.go"
    keys = [go_const(go, "PublicKey"), go_const(go, "ReservePublicKey")]
    keys = [k for k in keys if k.strip()]
    for k in keys:
        try:
            raw = base64.b64decode(k, validate=True)
        except ValueError:
            raw = b""
        if len(raw) != 32:
            raise FeedError(f"release.go holds a key that is not an ed25519 key: {k!r}")
    return keys


def read_index(path, repo):
    """The one image the index offers: (url, version, filename, length, crc)."""
    try:
        root = ET.parse(path).getroot()
    except ET.ParseError as e:
        raise FeedError(f"{path} does not parse: {e}") from e
    if root.tag != "INDEX":
        raise FeedError(f"{path} is not an INDEX")
    offers, ids = set(), set()
    for device in root.findall("DEVICE"):
        ids.add(device.get("ID"))
        releases = device.findall("HARDWARE/RELEASE")
        images = device.findall("HARDWARE/RELEASE/IMAGE")
        if len(releases) != 1 or len(images) != 1:
            raise FeedError(f"DEVICE {device.get('ID')} offers {len(images)} images, not one")
        rel, img = releases[0], images[0]
        offers.add((rel.get("HTTPHOST"), rel.get("URLPATH"), img.get("FILENAME"),
                    img.get("LENGTH"), img.get("CRC")))
    want_ids = {i for i, _ in MODELS}
    if ids != want_ids:
        raise FeedError(f"the index lists devices {sorted(ids)}, not the supported {sorted(want_ids)}")
    if len(offers) != 1:
        raise FeedError("the devices do not all offer the same image")
    host, urlpath, name, length, crc = offers.pop()
    m = re.fullmatch(rf"{re.escape(repo)}/releases/download/os-v({VERSION})", urlpath or "")
    if host != HOST or not m:
        raise FeedError(f"the index points at {host}/{urlpath}, not a release of {repo} on {HOST}")
    version = m.group(1)
    if name != f"SoundTap-Core-{version}.stu":
        raise FeedError(f"the index names {name}, not SoundTap-Core-{version}.stu")
    try:
        length = int(length)
        crc = int(crc, 16)
    except (TypeError, ValueError):
        raise FeedError("the index has no numeric LENGTH and CRC") from None
    return f"{host}/{urlpath}/{name}", version, name, length, crc


def verify_ed25519(keys, message, signature):
    """Whether one of keys (base64 raw ed25519) signed message, checked by openssl."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        (tmp / "message").write_bytes(message)
        (tmp / "signature").write_bytes(signature)
        for key in keys:
            der = base64.b64encode(DER_PREFIX + base64.b64decode(key)).decode()
            (tmp / "pub.pem").write_text(f"-----BEGIN PUBLIC KEY-----\n{der}\n-----END PUBLIC KEY-----\n")
            done = subprocess.run(["openssl", "pkeyutl", "-verify", "-pubin", "-inkey", str(tmp / "pub.pem"),
                                   "-rawin", "-in", str(tmp / "message"),
                                   "-sigfile", str(tmp / "signature")], capture_output=True)
            if done.returncode == 0:
                return True
    return False


def check_signature(sig_text, version, data, keys):
    """The .sig is the production Core statement for version over data, signed by a key."""
    if len(sig_text) > SIG_MAX:
        raise FeedError("the .sig is too long to be a statement")
    line = sig_text.removesuffix("\n").removesuffix("\r")
    statement = (f"soundtap-package v1 kind=core profile=production version={version} "
                 f"sha256={hashlib.sha256(data).hexdigest()}")
    m = re.fullmatch(re.escape(statement) + r" sig=([A-Za-z0-9+/]{86}==)", line)
    if not m:
        stated = line.split(" sig=", 1)[0]
        raise FeedError(f"the .sig states '{stated}'; the feed's asset needs '{statement}'")
    if not keys:
        raise FeedError("release.go holds no release key")
    if not verify_ed25519(keys, statement.encode(), base64.b64decode(m.group(1))):
        raise FeedError("the .sig does not verify with the release keys compiled into soundtapd")


def fetch(url, limit):
    """GET url, at most limit bytes."""
    req = urllib.request.Request(url, headers={"User-Agent": "soundtap-feed-check"})
    with urllib.request.urlopen(req, timeout=120) as r:
        body = r.read(limit + 1)
    if len(body) > limit:
        raise FeedError(f"{url} is larger than {limit} bytes")
    return body


def check_published(repo, version):
    """GitHub has os-v<version> as a published release that is not withdrawn."""
    tag = f"os-v{version}"
    rel = json.loads(fetch(f"https://api.github.com/repos/{repo}/releases/tags/{tag}", 1 << 20))
    if rel.get("draft"):
        raise FeedError(f"{tag} is a draft")
    if any(line.startswith(WITHDRAWN) for line in (rel.get("body") or "").split("\n")):
        raise FeedError(f"{tag} is withdrawn")


def check(index, assets=None, root=ROOT):
    """Raises FeedError unless the index names a release the speakers can trust."""
    repo = go_const(root / "app/internal/update/update.go", "Repo")
    keys = release_keys(root)
    url, version, name, length, crc = read_index(index, repo)
    if assets is None:
        check_published(repo, version)
        data = fetch(url, length)
        sig = fetch(url + ".sig", SIG_MAX).decode("utf-8", "replace")
    else:
        asset = Path(assets) / name
        if not asset.is_file():
            raise FeedError(f"no {name} in {assets}")
        data = asset.read_bytes()
        sig_path = Path(assets) / f"{name}.sig"
        if not sig_path.is_file():
            raise FeedError(f"no {name}.sig in {assets}")
        sig = sig_path.read_bytes()[:SIG_MAX + 1].decode("utf-8", "replace")
    if len(data) != length:
        raise FeedError(f"the index says {length} bytes; {name} has {len(data)}")
    if zlib.crc32(data) != crc:
        raise FeedError(f"the index's CRC 0x{crc:08x} is not {name}'s 0x{zlib.crc32(data):08x}")
    check_signature(sig, version, data, keys)
    return url, version


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("index")
    ap.add_argument("--assets", help="read the asset and its .sig from here instead of downloading")
    args = ap.parse_args(argv)
    try:
        url, version = check(args.index, args.assets)
    except (FeedError, OSError, ValueError) as e:
        print(f"feed_check.py: {e}", file=sys.stderr)
        return 1
    print(f"feed OK: SoundTap Core {version}, {url}, signed with a compiled release key")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
