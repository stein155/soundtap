# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""feed_check.py holds the first-install feed to a published, signed Core release.

The release's .sig must verify with a key compiled into soundtapd. Throwaway keys in a copied release.go; the release key is
never read, and nothing reaches the network: the download is replaced by a fake.
"""

import base64
import contextlib
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = pathlib.Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools/release"))
import feed_check  # noqa: E402
from mock_update_server import MODELS, PREVIEW_MODELS, build_index  # noqa: E402

REPO = "stein155/soundtap"
VERSION = "0.4.0"
NAME = f"SoundTap-Core-{VERSION}.stu"
URLPATH = f"{REPO}/releases/download/os-v{VERSION}"
URL = f"https://github.com/{URLPATH}/{NAME}"
PACKAGE = b"a Core package for the feed test; not firmware\n" * 64
PREVIEW_NAME = f"SoundTap-Core-{VERSION}-preview.stu"
PREVIEW_URL = f"https://github.com/{URLPATH}/{PREVIEW_NAME}"
PREVIEW_PACKAGE = b"the preview package for the feed test; not firmware\n" * 64


def ed25519_available() -> bool:
    """Return whether this openssl can make an ed25519 key."""
    with tempfile.TemporaryDirectory() as tmp:
        done = subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519",
                               "-out", str(pathlib.Path(tmp) / "k.pem")], check=False, capture_output=True)
        return done.returncode == 0


@unittest.skipUnless(ed25519_available(), "openssl without ed25519")
class FeedCheckTest(unittest.TestCase):
    """The feed check against throwaway keys, packages and indexes."""

    def setUp(self) -> None:
        """Write throwaway keys, a copied release.go and update.go, a signed package and its index."""
        self.tmp = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        self.active = self.key("active")
        self.reserve = self.key("reserve")
        self.stranger = self.key("stranger")
        self.write_keys(self.public(self.active), "")
        (self.tmp / "app/internal/update").mkdir(parents=True)
        (self.tmp / "app/internal/update/update.go").write_text(
            f'package update\n\nconst Repo = "{REPO}"\n')
        self.assets = self.tmp / "assets"
        self.assets.mkdir()
        (self.assets / NAME).write_bytes(PACKAGE)
        self.sign(self.active)
        self.index = self.tmp / "soundtouch"
        self.write_index()

    def key(self, name: str) -> pathlib.Path:
        """Make a throwaway ed25519 key and return its path."""
        path = self.tmp / f"{name}.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", str(path)],
                       check=True, capture_output=True)
        return path

    def public(self, key: pathlib.Path) -> str:
        """Return the key's raw public key in base64, as release.go holds it."""
        der = subprocess.run(["openssl", "pkey", "-in", str(key), "-pubout", "-outform", "DER"],
                             check=True, capture_output=True).stdout
        return base64.b64encode(der[-32:]).decode()

    def write_keys(self, active: str, reserve: str) -> None:
        """Write a release.go with these public keys."""
        release = self.tmp / "app/internal/release"
        release.mkdir(parents=True, exist_ok=True)
        (release / "release.go").write_text(
            f'package release\n\nconst PublicKey = "{active}"\n\nconst ReservePublicKey = "{reserve}"\n')

    def sign(self, key: pathlib.Path, kind: str = "core", profile: str = "production", version: str = VERSION,
             data: bytes = PACKAGE) -> None:
        """Write the .sig sign.sh writes, with a throwaway key."""
        statement = (f"soundtap-package v1 kind={kind} profile={profile} version={version} "
                     f"sha256={hashlib.sha256(data).hexdigest()}")
        (self.tmp / "statement").write_text(statement)
        raw = subprocess.run(["openssl", "pkeyutl", "-sign", "-inkey", str(key), "-rawin",
                              "-in", str(self.tmp / "statement")],
                             check=True, capture_output=True).stdout
        (self.assets / f"{NAME}.sig").write_text(
            f"{statement} sig={base64.b64encode(raw).decode()}\n")

    def write_index(self, host: str = "https://github.com", urlpath: str = URLPATH,
                    package: pathlib.Path | None = None, devices: list[tuple[str, str]] = MODELS) -> None:
        """Write the index offering package to devices from host and urlpath."""
        package = package or self.assets / NAME
        self.index.write_bytes(build_index(str(package), devices, "99.0.0.1.1", host, urlpath))

    def check(self, *, assets: bool = True) -> tuple[str, str]:
        """Check the supported feed, from the assets or through the fake download."""
        return feed_check.check(self.index, self.assets if assets else None, root=self.tmp)

    def refused(self, why: str) -> None:
        """Check that the feed is refused with why in the reason."""
        with self.assertRaises(feed_check.FeedError) as e:
            self.check()
        self.assertIn(why, str(e.exception))

    def test_the_index_of_a_signed_release_passes(self) -> None:
        """The index of a signed release passes."""
        self.assertEqual(self.check(), (URL, VERSION))

    def test_the_index_written_by_the_release_steps_passes(self) -> None:
        """The index written by the command RELEASING.md runs passes."""
        out = self.tmp / "written"
        subprocess.run([sys.executable, str(ROOT / "tools/release/mock_update_server.py"),
                        "--serve", str(self.assets / NAME), "--write-index", str(out),
                        "--host", "https://github.com", "--urlpath", URLPATH], check=True)
        self.index = out
        self.assertEqual(self.check(), (URL, VERSION))

    def test_a_reserve_key_signature_passes(self) -> None:
        """A signature by the reserve key passes."""
        self.write_keys(self.public(self.active), self.public(self.reserve))
        self.sign(self.reserve)
        self.assertEqual(self.check(), (URL, VERSION))

    def test_a_signature_by_another_key_is_refused(self) -> None:
        """A signature by another key is refused; the empty reserve placeholder stands for no key."""
        self.sign(self.stranger)
        self.refused("does not verify")
        # The empty reserve placeholder does not stand for any key.
        self.write_keys(self.public(self.active), "")
        self.sign(self.reserve)
        self.refused("does not verify")

    def test_a_statement_for_something_else_is_refused(self) -> None:
        """A .sig stating another profile, kind, version or bytes is refused."""
        for name, kw in {
            "alpha": {"profile": "alpha"},
            "app": {"kind": "app"},
            "other version": {"version": "0.3.0"},
            "stock": {"version": "stock"},
            "other bytes": {"data": b"other"},
        }.items():
            self.sign(self.active, **kw)
            with self.assertRaises(feed_check.FeedError, msg=name) as e:
                self.check()
            self.assertIn("the .sig states", str(e.exception), name)

    def test_a_missing_or_mangled_sig_is_refused(self) -> None:
        """A missing or mangled .sig is refused."""
        sig = (self.assets / f"{NAME}.sig").read_text()
        (self.assets / f"{NAME}.sig").write_text(sig.split(" sig=")[0] + "\n")
        self.refused("the .sig states")
        (self.assets / f"{NAME}.sig").write_text(sig + "\n")
        self.refused("the .sig states")
        (self.assets / f"{NAME}.sig").unlink()
        self.refused(f"no {NAME}.sig")

    def test_length_and_crc_must_be_the_assets(self) -> None:
        """The index's length and CRC must be the asset's."""
        other = self.tmp / NAME
        other.write_bytes(PACKAGE + b"x")
        self.write_index(package=other)
        self.refused("bytes")
        other.write_bytes(bytes([PACKAGE[0] ^ 1]) + PACKAGE[1:])
        self.write_index(package=other)
        self.refused("CRC")

    def test_the_url_must_be_a_release_of_the_repo(self) -> None:
        """The URL must be a release of the repository on GitHub over https."""
        for host, urlpath in [
            ("http://github.com", URLPATH),
            ("https://example.com", URLPATH),
            ("https://github.com", f"someone/soundtap/releases/download/os-v{VERSION}"),
            ("https://github.com", f"{REPO}/releases/download/app-v{VERSION}"),
            ("https://github.com", f"{REPO}/releases/download/os-v{VERSION}/../os-v0.1.0"),
        ]:
            self.write_index(host=host, urlpath=urlpath)
            with self.assertRaises(feed_check.FeedError, msg=f"{host}/{urlpath}"):
                self.check()

    def test_the_file_name_must_be_the_versions_package(self) -> None:
        """The file name must be the package of the URL's version."""
        other = self.assets / "SoundTap-Core-0.3.0.stu"
        shutil.copy(self.assets / NAME, other)
        self.write_index(package=other)
        self.refused("not SoundTap-Core-0.4.0.stu")

    def test_every_model_gets_the_same_image(self) -> None:
        """Every supported model is listed and offered the same image."""
        self.write_index(devices=MODELS[:-1])
        self.refused("not the supported")
        # One device offered another image.
        self.write_index()
        self.index.write_text(self.index.read_text().replace('LENGTH="', 'LENGTH="1', 1))
        self.refused("not all offer the same image")

    def test_a_malformed_compiled_key_is_refused(self) -> None:
        """A compiled key that is not an ed25519 key is refused."""
        self.write_keys(self.public(self.active), "AAAA")
        self.refused("not an ed25519 key")

    def test_the_command_line(self) -> None:
        """The command line exits 1 when the real tree's keys did not sign the package."""
        run = [sys.executable, str(ROOT / "tools/release/feed_check.py"), str(self.index),
               "--assets", str(self.assets)]
        # The real tree's keys did not sign this test package.
        done = subprocess.run(run, check=False, capture_output=True, text=True)
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertIn("does not verify", done.stderr)

    def downloads(self, release: dict[str, object] | None = None) -> tuple[contextlib.AbstractContextManager[object], list[str]]:
        """Return a patch that fakes the downloads, and the list of URLs it fetched."""
        files = {
            f"https://api.github.com/repos/{REPO}/releases/tags/os-v{VERSION}":
                json.dumps(release or {"tag_name": f"os-v{VERSION}", "draft": False,
                                       "body": "## SoundTap Core 0.4.0"}).encode(),
            URL: (self.assets / NAME).read_bytes(),
            URL + ".sig": (self.assets / f"{NAME}.sig").read_bytes(),
        }
        fetched = []

        def fetch(url: str, limit: int) -> bytes:
            fetched.append(url)
            if url not in files:
                msg = f"404 {url}"
                raise OSError(msg)
            if len(files[url]) > limit:
                msg = "too large"
                raise feed_check.FeedError(msg)
            return files[url]
        return mock.patch.object(feed_check, "fetch", fetch), fetched

    def test_without_assets_it_checks_the_published_release(self) -> None:
        """Without assets the published release, the package and its .sig are downloaded and pass."""
        patch, fetched = self.downloads()
        with patch:
            self.assertEqual(self.check(assets=False), (URL, VERSION))
        self.assertIn(URL, fetched)
        self.assertIn(URL + ".sig", fetched)

    def test_a_withdrawn_or_draft_release_is_refused(self) -> None:
        """A withdrawn or draft release is refused before anything is downloaded."""
        for release, why in [
            ({"draft": True}, "draft"),
            ({"draft": False, "body": "Withdrawn: the ST20 loses Wi-Fi\n\n## SoundTap Core"}, "withdrawn"),
        ]:
            patch, fetched = self.downloads(release)
            with patch, self.assertRaises(feed_check.FeedError) as e:
                self.check(assets=False)
            self.assertIn(why, str(e.exception))
            # Nothing is downloaded for a release the speakers must not get.
            self.assertNotIn(URL, fetched)

    def test_an_alpha_release_passes(self) -> None:
        """ADR 0035: the feed may name an alpha, a published GitHub prerelease signed like a release; a suffix that is not -alpha.N is no version."""
        version = "0.1.0-alpha.1"
        name = f"SoundTap-Core-{version}.stu"
        urlpath = f"{REPO}/releases/download/os-v{version}"
        url = f"https://github.com/{urlpath}/{name}"
        (self.assets / name).write_bytes(PACKAGE)
        self.sign(self.active, version=version)
        (self.assets / f"{NAME}.sig").rename(self.assets / f"{name}.sig")
        self.write_index(urlpath=urlpath, package=self.assets / name)
        self.assertEqual(self.check(), (url, version))
        files = {
            f"https://api.github.com/repos/{REPO}/releases/tags/os-v{version}":
                json.dumps({"tag_name": f"os-v{version}", "draft": False, "prerelease": True}).encode(),
            url: PACKAGE,
            url + ".sig": (self.assets / f"{name}.sig").read_bytes(),
        }
        with mock.patch.object(feed_check, "fetch", lambda u, _limit: files[u]):
            self.assertEqual(self.check(assets=False), (url, version))
        for other in ("0.1.0-beta.1", "0.1.0-alpha.0", "0.1.0-alpha"):
            other_name = f"SoundTap-Core-{other}.stu"
            shutil.copy(self.assets / name, self.assets / other_name)
            self.write_index(urlpath=f"{REPO}/releases/download/os-v{other}",
                             package=self.assets / other_name)
            with self.assertRaises(feed_check.FeedError, msg=other):
                self.check()

    def test_nothing_is_fetched_from_a_url_that_is_not_a_release(self) -> None:
        """Nothing is fetched from a URL that is not a release."""
        self.write_index(host="https://example.com")
        patch, fetched = self.downloads()
        with patch, self.assertRaises(feed_check.FeedError):
            self.check(assets=False)
        self.assertEqual(fetched, [])

    # ADR 0133: the preview feed offers the release's preview package to the preview models,
    # and no model is in both feeds.
    def preview_feed(self, devices: list[tuple[str, str]] = PREVIEW_MODELS, name: str = PREVIEW_NAME,
                     data: bytes = PREVIEW_PACKAGE) -> None:
        """Put the preview package and its .sig in the assets, and write the preview feed.

        The feed is written as the release steps write it, through the script itself.
        """
        (self.assets / name).write_bytes(data)
        self.sign(self.active, data=data)
        (self.assets / f"{NAME}.sig").rename(self.assets / f"{name}.sig")
        self.index = self.tmp / "soundtouch-preview"
        if devices is PREVIEW_MODELS:
            subprocess.run([sys.executable, str(ROOT / "tools/release/mock_update_server.py"),
                            "--serve", str(self.assets / name), "--write-index", str(self.index),
                            "--host", "https://github.com", "--urlpath", URLPATH, "--preview"],
                           check=True)
        else:
            self.index.write_bytes(build_index(str(self.assets / name), devices, "99.0.0.1.1",
                                               "https://github.com", URLPATH))

    def check_preview(self, *, assets: bool = True) -> tuple[str, str]:
        """Check the preview feed, from the assets or through the fake download."""
        return feed_check.check(self.index, self.assets if assets else None, root=self.tmp,
                                preview=True)

    def test_the_preview_feed_of_a_signed_release_passes(self) -> None:
        """The preview feed of a signed release passes, and each feed fails the other's rules."""
        self.preview_feed()
        self.assertEqual(self.check_preview(), (PREVIEW_URL, VERSION))
        # The supported feed's rules do not pass it, nor the preview rules the supported feed.
        with self.assertRaises(feed_check.FeedError):
            self.check()
        self.index = self.tmp / "soundtouch"
        with self.assertRaises(feed_check.FeedError):
            self.check_preview()

    def test_the_preview_feed_names_the_preview_package(self) -> None:
        """The preview feed must name the preview package: the preview models' installer refuses the main one."""
        # The main package to the preview models: their installer check would refuse it.
        self.preview_feed(name=NAME, data=PACKAGE)
        with self.assertRaises(feed_check.FeedError) as e:
            self.check_preview()
        self.assertIn(f"not {PREVIEW_NAME}", str(e.exception))

    def test_the_preview_packages_sig_is_over_its_own_bytes(self) -> None:
        """The preview package's .sig must be over its own bytes."""
        self.preview_feed()
        self.sign(self.active, data=PACKAGE)
        (self.assets / f"{NAME}.sig").replace(self.assets / f"{PREVIEW_NAME}.sig")
        with self.assertRaises(feed_check.FeedError) as e:
            self.check_preview()
        self.assertIn("the .sig states", str(e.exception))

    def test_no_model_is_in_both_feeds(self) -> None:
        """No model is in both feeds, and each feed lists all its models."""
        self.assertFalse({i for i, _ in MODELS} & {i for i, _ in PREVIEW_MODELS})
        # The ST20 Series III (0x093B) comes from the preview feed only.
        self.assertIn("0x093B", {i for i, _ in PREVIEW_MODELS})
        series_iii = [d for d in PREVIEW_MODELS if d[0] == "0x093B"]
        self.write_index(devices=MODELS + series_iii)
        self.refused("each model is in one feed only")
        self.preview_feed(devices=PREVIEW_MODELS + MODELS[:1])
        with self.assertRaises(feed_check.FeedError) as e:
            self.check_preview()
        self.assertIn("each model is in one feed only", str(e.exception))
        self.preview_feed(devices=PREVIEW_MODELS[:-1])
        with self.assertRaises(feed_check.FeedError) as e:
            self.check_preview()
        self.assertIn("not the preview", str(e.exception))

    def test_the_preview_feed_leaves_out_what_the_installers_refuse(self) -> None:
        """The models the installers refuse are in no feed.

        Those are the Lifestyle ids on the sm2 (0x093F, 0x0947), the Lifestyle 600/650 module (0x0948)
        and 0x094B.
        """
        ids = {i for i, _ in MODELS + PREVIEW_MODELS}
        for refused in ("0x093F", "0x0947", "0x0948", "0x094B"):
            self.assertNotIn(refused, ids)

    def test_the_preview_command_line(self) -> None:
        """The preview command line gets past the devices and name to the real tree's keys."""
        self.preview_feed()
        run = [sys.executable, str(ROOT / "tools/release/feed_check.py"), str(self.index),
               "--preview", "--assets", str(self.assets)]
        done = subprocess.run(run, check=False, capture_output=True, text=True)
        # Past the device list and the name: it stops at the real tree's keys.
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertIn("does not verify", done.stderr)


class RepositoryFeedsTest(unittest.TestCase):
    """The feeds on this commit, without the network.

    Each lists its own models and offers one image of the right name (feed.yml checks the rest
    against the published release).
    """

    def test_the_feeds_list_their_own_models(self) -> None:
        """Each feed on this commit lists its own models and one image of the right name."""
        repo = feed_check.go_const(ROOT / "app/internal/update/update.go", "Repo")
        for path, preview in ((ROOT / "updates/soundtouch", False),
                              (ROOT / "updates/soundtouch-preview", True)):
            if path.exists():
                with self.subTest(path=path.name):
                    feed_check.read_index(path, repo, preview=preview)


if __name__ == "__main__":
    unittest.main()
