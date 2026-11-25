# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""sign.sh writes the signed package statement of ADR 0048.

It fails without a signature when it cannot sign; a production signature needs a key under a passphrase. The real-key
tests use a throwaway key in a copied tree; the release key is never read.
"""

import base64
import hashlib
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[3]
SIGN = ROOT / "tools/release/sign.sh"
PUBLIC = re.search(r'^const PublicKey = "(.*)"$',
                   (ROOT / "app/internal/release/release.go").read_text(), re.MULTILINE).group(1)

# A stand-in openssl: the right public key, a digest, and a signer that fails.
FAKE = f"""#!/bin/sh
case "$1" in
pkey) printf 'DERHEAD'; printf '%s' '{PUBLIC}' | base64 -d ;;
dgst) printf '%032d' 0 ;;
pkeyutl) echo injected signing failure >&2; exit 1 ;;
esac
"""

# The throwaway encrypted key's passphrase, and one that is not it.
PHRASE = "x"
WRONG_PHRASE = "y"

# The line release.Verify parses, field for field.
LINE = re.compile(r"soundtap-package v1 kind=(core|app) profile=(production|alpha) "
                  r"version=(\d+\.\d+\.\d+(?:-alpha\.[1-9]\d*)?|stock) sha256=([0-9a-f]{64}) sig=([A-Za-z0-9+/]{86}==)\n")


def ed25519_available() -> bool:
    """Return whether this openssl can make an ed25519 key."""
    with tempfile.TemporaryDirectory() as tmp:
        done = subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519",
                               "-out", str(pathlib.Path(tmp) / "k.pem")], check=False, capture_output=True)
        return done.returncode == 0


class SignTest(unittest.TestCase):
    """sign.sh refusing to sign, with a failing signer or a bad version."""

    def test_a_failed_signer_fails_without_a_signature(self) -> None:
        """A signer that fails leaves no signature and no success message."""
        self.assertEqual(len(base64.b64decode(PUBLIC)), 32)
        with tempfile.TemporaryDirectory() as tmp_name:
            tmp = pathlib.Path(tmp_name)
            (tmp / "openssl").write_text(FAKE)
            (tmp / "openssl").chmod(0o755)
            (tmp / "key.pem").write_text("not used\n")
            asset = tmp / "asset.bin"
            asset.write_bytes(b"payload")
            env = dict(os.environ, PATH=f"{tmp}:{os.environ['PATH']}",
                       SOUNDTAP_RELEASE_KEY_FILE=str(tmp / "key.pem"))
            out = subprocess.run(["sh", str(SIGN), str(asset)], check=False, env=env,
                                 capture_output=True, text=True)
            self.assertNotEqual(out.returncode, 0, out.stdout)
            self.assertNotIn("signed", out.stdout)
            self.assertFalse((tmp / "asset.bin.sig").exists())

    def test_a_version_that_is_not_one_is_refused(self) -> None:
        """A package version that is not a release version is refused and nothing is signed."""
        for version in ("latest", "0.1.0-alpha", "0.1.0-alpha.0", "0.1.0-beta.1", "0.1", "1.2.3.4"):
            with tempfile.TemporaryDirectory() as tmp_name:
                tmp = pathlib.Path(tmp_name)
                (tmp / "key.pem").write_text("not used\n")
                asset = tmp / "asset.bin"
                asset.write_bytes(b"payload")
                env = dict(os.environ, SOUNDTAP_RELEASE_KEY_FILE=str(tmp / "key.pem"),
                           SOUNDTAP_PACKAGE_VERSION=version)
                out = subprocess.run(["sh", str(SIGN), str(asset)], check=False, env=env,
                                     capture_output=True, text=True)
                self.assertNotEqual(out.returncode, 0, out.stdout)
                self.assertIn("bad version", out.stderr, version)
                self.assertFalse((tmp / "asset.bin.sig").exists())


@unittest.skipUnless(ed25519_available(), "openssl without ed25519")
class SignStatementTest(unittest.TestCase):
    """sign.sh run for real, in a copy of the tree whose release.PublicKey is a throwaway key."""

    def setUp(self) -> None:
        """Copy sign.sh into a tree whose release.PublicKey is a throwaway key, plain and under a passphrase."""
        self.tmp = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        (self.tmp / "tools/release").mkdir(parents=True)
        shutil.copy(SIGN, self.tmp / "tools/release/sign.sh")
        self.key = self.tmp / "key.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", str(self.key)],
                       check=True, capture_output=True)
        # The same key under a passphrase, as a production key must be.
        self.encrypted = self.tmp / "encrypted.pem"
        subprocess.run(["openssl", "pkey", "-in", str(self.key), "-aes256", "-passout", f"pass:{PHRASE}",
                        "-out", str(self.encrypted)], check=True, capture_output=True,
                       stdin=subprocess.DEVNULL)
        der = subprocess.run(["openssl", "pkey", "-in", str(self.key), "-pubout", "-outform", "DER"],
                             check=True, capture_output=True).stdout
        self.pub = self.tmp / "pub.pem"
        subprocess.run(["openssl", "pkey", "-in", str(self.key), "-pubout", "-out", str(self.pub)],
                       check=True, capture_output=True)
        release = self.tmp / "app/internal/release"
        release.mkdir(parents=True)
        (release / "release.go").write_text(
            f'package release\n\nconst PublicKey = "{base64.b64encode(der[-32:]).decode()}"\n')
        (self.tmp / "app/VERSION").write_text("0.7.0\n")
        (self.tmp / "os").mkdir()
        (self.tmp / "os/VERSION").write_text("0.4.0\n")

    def sign(self, data: bytes, key: pathlib.Path | None = None,
             **env: str) -> tuple[subprocess.CompletedProcess[str], str | None]:
        """Run sign.sh on data with key and env; return the run and the .sig text, None without one."""
        asset = self.tmp / "asset"
        asset.write_bytes(data)
        sig = self.tmp / "asset.sig"
        if sig.exists():
            sig.unlink()
        base = {k: v for k, v in os.environ.items() if not k.startswith("SOUNDTAP_")}
        out = subprocess.run(["sh", str(self.tmp / "tools/release/sign.sh"), str(asset)],
                             check=False, env=dict(base, SOUNDTAP_RELEASE_KEY_FILE=str(key or self.key), **env),
                             capture_output=True, text=True, stdin=subprocess.DEVNULL)
        return out, (sig.read_text() if sig.exists() else None)

    def production(self, **env: str) -> dict[str, object]:
        """Return the arguments for a production signature with the encrypted key and its passphrase."""
        return dict(key=self.encrypted, SOUNDTAP_SIGN_PROFILE="production",
                    SOUNDTAP_RELEASE_KEY_PASSIN="env:SIGN_TEST_PASS", SIGN_TEST_PASS=PHRASE, **env)

    def verifies(self, statement: str, sig_b64: str) -> bool:
        """Return whether sig_b64 is the throwaway key's signature over statement."""
        (self.tmp / "statement").write_bytes(statement.encode())
        (self.tmp / "sig.raw").write_bytes(base64.b64decode(sig_b64))
        done = subprocess.run(["openssl", "pkeyutl", "-verify", "-pubin", "-inkey", str(self.pub),
                               "-rawin", "-in", str(self.tmp / "statement"),
                               "-sigfile", str(self.tmp / "sig.raw")], check=False, capture_output=True)
        return done.returncode == 0

    def statement(self, data: bytes, key: pathlib.Path | None = None, **env: str) -> tuple[str, str, str]:
        """Sign data, check the .sig line verifies and names its SHA-256; return its kind, profile and version."""
        out, sig = self.sign(data, key=key, **env)
        self.assertEqual(out.returncode, 0, out.stderr)
        m = LINE.fullmatch(sig)
        self.assertIsNotNone(m, sig)
        text = sig[:sig.index(" sig=")]
        self.assertTrue(self.verifies(text, m.group(5)), sig)
        self.assertFalse(self.verifies(text.replace("profile=alpha", "profile=production")
                                       .replace("version=", "version=9"), m.group(5)))
        self.assertEqual(m.group(4), hashlib.sha256(data).hexdigest())
        return m.group(1), m.group(2), m.group(3)

    def test_a_package_is_core_from_os_version_and_alpha_by_default(self) -> None:
        """A package is Core, at os/VERSION, and alpha by default."""
        self.assertEqual(self.statement(b"BOSE package"), ("core", "alpha", "0.4.0"))

    def test_an_elf_is_an_app_from_app_version(self) -> None:
        """An ELF is an app, at app/VERSION."""
        self.assertEqual(self.statement(b"\x7fELF binary"), ("app", "alpha", "0.7.0"))

    def test_a_release_names_kind_version_and_profile(self) -> None:
        """A release names its kind, version and profile, an alpha included."""
        self.assertEqual(self.statement(b"\x7fELF binary", **self.production(
                                            SOUNDTAP_PACKAGE_KIND="app", SOUNDTAP_PACKAGE_VERSION="1.2.3")),
                         ("app", "production", "1.2.3"))
        self.assertEqual(self.statement(b"Bose package", **self.production(SOUNDTAP_PACKAGE_VERSION="stock")),
                         ("core", "production", "stock"))
        # An alpha is a release too: production, with its prerelease version (ADR 0035).
        self.assertEqual(self.statement(b"BOSE package", **self.production(SOUNDTAP_PACKAGE_VERSION="0.1.0-alpha.1")),
                         ("core", "production", "0.1.0-alpha.1"))

    def test_production_refuses_a_key_without_a_passphrase(self) -> None:
        """A production signature refuses a key without a passphrase."""
        out, sig = self.sign(b"Bose package", SOUNDTAP_SIGN_PROFILE="production")
        self.assertNotEqual(out.returncode, 0, out.stdout)
        self.assertIn("has no passphrase", out.stderr)
        self.assertIsNone(sig)
        # Saying how to read a passphrase does not make a plain key pass.
        out, sig = self.sign(b"Bose package", SOUNDTAP_SIGN_PROFILE="production",
                             SOUNDTAP_RELEASE_KEY_PASSIN=f"pass:{PHRASE}")
        self.assertIn("has no passphrase", out.stderr)
        self.assertIsNone(sig)

    def test_alpha_may_use_either_key(self) -> None:
        """An alpha signature takes the plain or the encrypted key."""
        self.assertEqual(self.statement(b"Bose package"), ("core", "alpha", "0.4.0"))
        self.assertEqual(self.statement(b"Bose package", key=self.encrypted,
                                        SOUNDTAP_RELEASE_KEY_PASSIN=f"pass:{PHRASE}"), ("core", "alpha", "0.4.0"))

    def test_a_wrong_passphrase_signs_nothing(self) -> None:
        """A wrong passphrase signs nothing."""
        out, sig = self.sign(b"Bose package", **dict(self.production(), SIGN_TEST_PASS=WRONG_PHRASE))
        self.assertNotEqual(out.returncode, 0, out.stdout)
        self.assertNotIn("signed", out.stdout)
        self.assertIsNone(sig)

    def test_only_the_release_key_signs(self) -> None:
        """Only the release key signs, whatever the environment names.

        ADR 0048: a speaker trusts the release keys alone; an own build installs unsigned
        behind the press, so sign.sh takes no other key.
        """
        own = self.tmp / "own.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", str(own)],
                       check=True, capture_output=True)
        out, sig = self.sign(b"Own build", key=own)
        self.assertIn("does not match release.PublicKey", out.stderr)
        self.assertIsNone(sig)
        out, sig = self.sign(b"Own build", key=own, SOUNDTAP_OWNER_KEY_FILE=str(own))
        self.assertIn("does not match release.PublicKey", out.stderr)
        self.assertIsNone(sig)

    def test_refusals_write_nothing(self) -> None:
        """A refused kind, version or profile writes no .sig."""
        for env in ({"SOUNDTAP_PACKAGE_KIND": "app", "SOUNDTAP_PACKAGE_VERSION": "stock"},
                    {"SOUNDTAP_PACKAGE_KIND": "bco"},
                    {"SOUNDTAP_SIGN_PROFILE": "beta"}):
            out, sig = self.sign(b"\x7fELF binary", **env)
            self.assertNotEqual(out.returncode, 0, env)
            self.assertIsNone(sig, env)


if __name__ == "__main__":
    unittest.main()
