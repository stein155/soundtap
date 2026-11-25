# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The tree that goes public names nothing of the maintainer's home and cites no private history.

Neither its docs nor the text files the image ships cite anything of the private history. The public repository starts from one commit of this tree, so a commit hash or a pull request
number of the private repository points at nothing there, and hardware evidence names the
tested artifact (version and SHA-256) or the date and model instead. The maintainer's
identifiers are listed here only as SHA-256 digests, so this test does not publish them.
"""
import hashlib
import ipaddress
import re
import subprocess
import unittest
from collections.abc import Iterator
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]

# SHA-256 of the lower-case form of each identifier of the maintainer's speakers and
# network: packaged-product and SCM serial numbers, MAC addresses and BSSIDs (12 hex digits,
# no separators), the six-digit MAC suffix a speaker's setup name carries, a Bose account id, the home SSID, build host and login names, and the
# public addresses of the home connection (IPv4, and the first three groups of the IPv6
# prefix).
OWNER_DIGESTS = frozenset({
    "117f2ba727908844d2ca6dadc2ed96bd4da9d68494868c23922cf68f9b98894c",
    "bbf02171b5bf1f57e3a8eecd020aa2da0b6f9bad26eb2629bdd3008094866053",
    "78576e291fd9bd4f7b1448eaaf714ee3f7bb3e6451b671be41f7f56bb1c461d2",
    "0849f683504348d53dde927bcaf2ce6f4e1ccfb162eed6f17657b568ffda7c3f",
    "153a9b1afec5023afd706e8eee1b394b47d51d34035eb373c33169de6c6d142a",
    "21901f671fff6c0dfdfef4ae25134a3ed17453fdc61341db65e7b337452cfc2d",
    "792ef7ecbd2ec7b096ec27a5a054cd90120e2ac12a19f21987b694d17d77b360",
    "8f87e55b5f3b4e29d0bb9a17ff2f9df7fe2d6daecf59e39597e250e19638c317",
    "02358b91e786af66585ad2d7c0fee89f9f87c5645924a5882bcb7dc992b83114",
    "d421f65fe5c153aaba052ae4890a0c4a66363965c1f08808cabfd2c34224f0c3",
    "de517200205d457293c92f68616ada97a201a4aecbd87ff06beb5b7e95fd8697",
    "d1adde28a60fcc983455863da00dbd286c92a7a313d8b703238e858df9a216c6",
    "afb2b34632d375f95d20e256a92224881404d6484032cdf16581c15c24e1d3f5",
    "25df677663616db63669988b244f7ed7f29357c0d008c1fd9a21de289f49fa4a",
    "98f9342a8d913165f71d59506d9cc3be4e4b801469efa0ed3f00d888973d0ccb",
    "5d58b28cb36156a3ea835fe3cb418d42467721477666c4012d9bd5a76c826a8f",
    "3db08bc92db27cab42df633fb2654b30785d46d0bd11829119667b140d2c87ff",
    "b40f6562e92ec3c8b3f2ed616236f4070ac0c16884acdc32d5f2f3b5de7d514a",
    "96b9ec6406bf26b34d25667912974d17ce6806c5da0417d106bee85e8857902e",
    "65ce2f4207e40b87f61f8c155cd26260b1eb479b6379fe2105f256e1cd081b89",
    "5c7043c43f111a99d7d3109082996b4254fd683f5783770b7072ee01737dec0a",
    "626b973a10574d27c67140cfbd907e3a0b4af87ebaf8a767c732ea2bf0d6b9c6",
})

# The maintainer's home LAN, 192.168.88/24, written as a number so this file does not trip the
# check. Tests use 192.0.2.0/24 or, where a private address is needed, 10.0.1.0/24.
HOME_LAN = ipaddress.IPv4Network((0xC0A85800, 24))

WORD = re.compile(r"[a-z0-9]+(?:[-.][a-z0-9]+)*")
MAC = re.compile(r"(?<![0-9a-f])(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}(?![0-9a-f])")
IPV6_PREFIX = re.compile(r"(?<![0-9a-f:])([0-9a-f]{1,4}:[0-9a-f]{1,4}:[0-9a-f]{1,4}):")
IPV4 = re.compile(r"(?<![\d.])(?:\d{1,3}\.){3}\d{1,3}(?![\d.])")

# A pull request or issue of the private repository: "#2292", "(#1668)". A fragment link
# ("](#x)"), an HTML entity ("&#160;") or "owner/repo#12" is not one.
PR_REF = re.compile(r"(?<![\w/&#\]])(?<!\]\()#(\d{2,5})\b(?!-)")
# A commit cited as evidence: after a word that names one, in parentheses, or before a date as
# the release checklist's cells once held them ("4e5fa7f7 2026-11-17").
HEX = r"`?([0-9a-f]{7,40})`?"
COMMIT_REFS = (
    re.compile(r"\b(?i:commits?|builds?|candidate|main|since)`?\s+" + HEX + r"(?![\w])"),
    re.compile(r"\(" + HEX + r"[,)]"),
    re.compile(r"(?<![\w:])" + HEX + r",? 20\d\d-\d\d-\d\d"),
)
GIT_HISTORY = re.compile(r"\bgit history\b", re.IGNORECASE)
# A template's example, not a reference.
PR_EXAMPLES = {".github/PULL_REQUEST_TEMPLATE.md"}
# The image ships these trees: the rootfs overlay, and the board's scripts, device trees and
# config templates. Upstream patches keep their own headers (upstream commit ids, list links);
# Markdown is checked with the docs.
SHIPPED = ("os/overlay/", "os/board/")


def tracked() -> list[str]:
    """Return the tracked files of the tree."""
    return subprocess.run(["git", "ls-files"], cwd=ROOT, check=True, capture_output=True,
                          text=True).stdout.splitlines()


def text_of(path: str) -> str | None:
    """Return the UTF-8 text of the tracked file `path`, or None when it is not a readable text file."""
    try:
        return (ROOT / path).read_text(encoding="utf-8")
    except (UnicodeDecodeError, IsADirectoryError, FileNotFoundError):
        return None


def candidates(text: str) -> set[str]:
    """Return every string in text that could be one of the identifiers, in their digest form."""
    low = text.lower()
    found = set()
    for word in WORD.findall(low):
        found.add(word)
        found.update(re.split(r"[-.]", word))
    found.update(m.replace(":", "").replace("-", "") for m in MAC.findall(low))
    found.update(IPV6_PREFIX.findall(low))
    return found


def shipped_text_files() -> Iterator[tuple[str, str]]:
    """Yield (path, text) of every tracked text file under SHIPPED but the Markdown and the patches."""
    for path in tracked():
        if not path.startswith(SHIPPED) or path.endswith((".md", ".patch")):
            continue
        text = text_of(path)
        if text is not None and "\0" not in text:
            yield path, text


def owner_identifiers(text: str) -> list[str]:
    """Return the strings in `text` whose digest is one of the maintainer's identifiers."""
    return sorted(c for c in candidates(text)
                  if hashlib.sha256(c.encode()).hexdigest() in OWNER_DIGESTS)


def home_lan_addresses(text: str) -> list[str]:
    """Return the IPv4 addresses in `text` that lie in the maintainer's home LAN."""
    hits = []
    for addr in IPV4.findall(text):
        try:
            if ipaddress.ip_address(addr) in HOME_LAN:
                hits.append(addr)
        except ValueError:
            continue
    return hits


def prose(text: str) -> str:
    """Return the Markdown outside fenced blocks and inline code spans."""
    out, fence = [], False
    for line in text.splitlines():
        if re.match(r"^ {0,3}(`{3,}|~{3,})", line):
            fence = not fence
            continue
        if not fence:
            out.append(re.sub(r"`[^`]*`", "", line))
    return "\n".join(out)


def history_refs(text: str) -> list[str]:
    """Return the pull request numbers, commit hashes and pointers into git history in a text.

    The text is Markdown, or a script or config file, where a `...` span is left out as in Markdown.
    """
    hits = [f"#{n}" for n in PR_REF.findall(prose(text))]
    for rx in COMMIT_REFS:
        hits += [h for h in rx.findall(text)
                 if re.search(r"\d", h) and re.search(r"[a-f]", h)]
    hits += GIT_HISTORY.findall(text)
    return sorted(set(hits))


class PublicTreeTest(unittest.TestCase):
    """The tracked tree is fit to publish."""

    def test_no_owner_identifiers(self) -> None:
        """No tracked file names or holds one of the maintainer's identifiers."""
        found = {}
        for path in tracked():
            text = text_of(path)
            hits = owner_identifiers(path + "\n" + (text or ""))
            if hits:
                found[path] = len(hits)
        # The identifiers themselves stay out of the message: it names files only.
        self.assertEqual(found, {}, "the maintainer's serials, MACs, SSID, hosts or "
                         "addresses: use SERIAL0000000000A, 02:00:00:xx:xx:xx, 192.0.2.x")

    def test_no_home_lan_addresses(self) -> None:
        """No tracked file names or holds an address of the maintainer's home LAN."""
        found = {}
        for path in tracked():
            hits = home_lan_addresses(path + "\n" + (text_of(path) or ""))
            if hits:
                found[path] = sorted(set(hits))
        self.assertEqual(found, {}, "the maintainer's home LAN: use 192.0.2.x, or 10.0.1.x "
                         "where a test needs a private address")

    def test_docs_cite_no_private_history(self) -> None:
        """No Markdown file cites a pull request, commit or the git history of the private repository."""
        found = {}
        for path in tracked():
            if not path.endswith(".md"):
                continue
            hits = history_refs(text_of(path) or "")
            if path in PR_EXAMPLES:
                hits = [h for h in hits if not h.startswith("#")]
            if hits:
                found[path] = hits
        self.assertEqual(found, {}, "name hardware evidence by artifact version and SHA-256, "
                         "or by date and model, and say what a PR did instead of its number")

    def test_shipped_files_cite_no_private_history(self) -> None:
        """No text file the image ships cites a pull request, commit or the git history."""
        found = {}
        for path, text in shipped_text_files():
            hits = history_refs(text)
            if hits:
                found[path] = hits
        self.assertEqual(found, {}, "a comment in a file the image ships says what a change did, "
                         "not its PR number or commit; hardware evidence by date and model")

    def test_shipped_files_are_the_image_text(self) -> None:
        """The shipped files are the overlay and board text files, without patches and Markdown."""
        paths = {path for path, _ in shipped_text_files()}
        self.assertIn("os/overlay/etc/init.d/soundtap", paths)
        self.assertIn("os/board/rootfs/post-build.sh", paths)
        self.assertIn("os/overlay/opt/soundtap/wifi.conf.sample", paths)
        self.assertFalse([p for p in paths if p.endswith((".patch", ".md"))])

    def test_the_checks_catch_what_they_are_for(self) -> None:
        """The checks find home addresses and history references and pass placeholders and syntax."""
        home = str(HOME_LAN.network_address + 12)
        self.assertEqual(home_lan_addresses(f"leader {home}, router 192.168.1.1"), [home])
        self.assertEqual(home_lan_addresses("leader 10.0.1.12, doc 192.0.2.12"), [])
        refs = history_refs("Done in #1234 (#5678). Build 0a1b2c3d, 2026-11-11; see "
                            "`main` 9f8e7d6c and (abc1234). It is in git history.\n"
                            "| Install | 1234abcd 2026-11-13 |")
        self.assertEqual(refs, sorted(["#1234", "#5678", "0a1b2c3d", "9f8e7d6c", "abc1234",
                                       "1234abcd", "git history"]))
        self.assertEqual(history_refs("Kernel `Linux 3.14.43+ #137`, [link](#anchor-12), "
                                      "golang/go#24164, [11. Open](#11-open-points), "
                                      "SHA-256 `95f04731…dedc7c5c`, "
                                      "0.1.0 sha256:1a2b3c4d 2026-11-15, build 2026-11-14"), [])
        # Shell and device-tree syntax is not a reference; a comment that cites one is.
        self.assertEqual(history_refs("#!/bin/sh\n[ $# -ge 2 ] || n=${#list}\n"
                                      "#address-cells = <1>;\n# from owner/repo#12\n"), [])
        self.assertEqual(history_refs("# Fixed in #2292; see commit 4e5fa7f7.\n"),
                         ["#2292", "4e5fa7f7"])
        # A placeholder MAC or serial is not the maintainer's.
        self.assertEqual(owner_identifiers("02:00:00:a1:b2:30 SERIAL0000000010A"), [])


if __name__ == "__main__":
    unittest.main()
