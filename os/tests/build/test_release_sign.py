# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""sign.sh writes the signed package statement of ADR 0048, and fails without a signature
when it cannot sign; a production signature needs a key under a passphrase. The real-key
tests use a throwaway key in a copied tree; the release key is never read."""

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
                   (ROOT / "app/internal/release/release.go").read_text(), re.M).group(1)

# A stand-in openssl: the right public key, a digest, and a signer that fails.
FAKE = f"""#!/bin/sh
case "$1" in
pkey) printf 'DERHEAD'; printf '%s' '{PUBLIC}' | base64 -d ;;
dgst) printf '%032d' 0 ;;
pkeyutl) echo injected signing failure >&2; exit 1 ;;
esac
"""

# The line release.Verify parses, field for field.
LINE = re.compile(r"soundtap-package v1 kind=(core|app) profile=(production|alpha) "
                  r"version=(\d+\.\d+\.\d+(?:-alpha\.[1-9]\d*)?|stock) sha256=([0-9a-f]{64}) sig=([A-Za-z0-9+/]{86}==)\n")


def ed25519_available():
    with tempfile.TemporaryDirectory() as tmp:
        done = subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519",
                               "-out", os.path.join(tmp, "k.pem")], capture_output=True)
        return done.returncode == 0


class SignTest(unittest.TestCase):
    def test_a_failed_signer_fails_without_a_signature(self):
        self.assertEqual(len(base64.b64decode(PUBLIC)), 32)
        with tempfile.TemporaryDirectory() as tmp:
            tmp = pathlib.Path(tmp)
            (tmp / "openssl").write_text(FAKE)
            (tmp / "openssl").chmod(0o755)
            (tmp / "key.pem").write_text("not used\n")
            asset = tmp / "asset.bin"
            asset.write_bytes(b"payload")
            env = dict(os.environ, PATH=f"{tmp}:{os.environ['PATH']}",
                       SOUNDTAP_RELEASE_KEY_FILE=str(tmp / "key.pem"))
            out = subprocess.run(["sh", str(SIGN), str(asset)], env=env,
                                 capture_output=True, text=True)
            self.assertNotEqual(out.returncode, 0, out.stdout)
            self.assertNotIn("signed", out.stdout)
            self.assertFalse((tmp / "asset.bin.sig").exists())

    def test_a_version_that_is_not_one_is_refused(self):
        for version in ("latest", "0.1.0-alpha", "0.1.0-alpha.0", "0.1.0-beta.1", "0.1", "1.2.3.4"):
            with tempfile.TemporaryDirectory() as tmp:
                tmp = pathlib.Path(tmp)
                (tmp / "key.pem").write_text("not used\n")
                asset = tmp / "asset.bin"
                asset.write_bytes(b"payload")
                env = dict(os.environ, SOUNDTAP_RELEASE_KEY_FILE=str(tmp / "key.pem"),
                           SOUNDTAP_PACKAGE_VERSION=version)
                out = subprocess.run(["sh", str(SIGN), str(asset)], env=env,
                                     capture_output=True, text=True)
                self.assertNotEqual(out.returncode, 0, out.stdout)
                self.assertIn("bad version", out.stderr, version)
                self.assertFalse((tmp / "asset.bin.sig").exists())


@unittest.skipUnless(ed25519_available(), "openssl without ed25519")
class SignStatementTest(unittest.TestCase):
    """sign.sh run for real, in a copy of the tree whose release.PublicKey is a throwaway key."""

    def setUp(self):
        self.tmp = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        (self.tmp / "tools/release").mkdir(parents=True)
        shutil.copy(SIGN, self.tmp / "tools/release/sign.sh")
        self.key = self.tmp / "key.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", str(self.key)],
                       check=True, capture_output=True)
        # The same key under a passphrase, as a production key must be.
        self.encrypted = self.tmp / "encrypted.pem"
        subprocess.run(["openssl", "pkey", "-in", str(self.key), "-aes256", "-passout", "pass:x",
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

    def sign(self, data, key=None, **env):
        asset = self.tmp / "asset"
        asset.write_bytes(data)
        sig = self.tmp / "asset.sig"
        if sig.exists():
            sig.unlink()
        base = {k: v for k, v in os.environ.items() if not k.startswith("SOUNDTAP_")}
        out = subprocess.run(["sh", str(self.tmp / "tools/release/sign.sh"), str(asset)],
                             env=dict(base, SOUNDTAP_RELEASE_KEY_FILE=str(key or self.key), **env),
                             capture_output=True, text=True, stdin=subprocess.DEVNULL)
        return out, (sig.read_text() if sig.exists() else None)

    def production(self, **env):
        """A production signature with the encrypted key and its passphrase."""
        return dict(key=self.encrypted, SOUNDTAP_SIGN_PROFILE="production",
                    SOUNDTAP_RELEASE_KEY_PASSIN="env:SIGN_TEST_PASS", SIGN_TEST_PASS="x", **env)

    def verifies(self, statement, sig_b64):
        (self.tmp / "statement").write_bytes(statement.encode())
        (self.tmp / "sig.raw").write_bytes(base64.b64decode(sig_b64))
        done = subprocess.run(["openssl", "pkeyutl", "-verify", "-pubin", "-inkey", str(self.pub),
                               "-rawin", "-in", str(self.tmp / "statement"),
                               "-sigfile", str(self.tmp / "sig.raw")], capture_output=True)
        return done.returncode == 0

    def statement(self, data, key=None, **env):
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

    def test_a_package_is_core_from_os_version_and_alpha_by_default(self):
        self.assertEqual(self.statement(b"BOSE package"), ("core", "alpha", "0.4.0"))

    def test_an_elf_is_an_app_from_app_version(self):
        self.assertEqual(self.statement(b"\x7fELF binary"), ("app", "alpha", "0.7.0"))

    def test_a_release_names_kind_version_and_profile(self):
        self.assertEqual(self.statement(b"\x7fELF binary", **self.production(
                                            SOUNDTAP_PACKAGE_KIND="app", SOUNDTAP_PACKAGE_VERSION="1.2.3")),
                         ("app", "production", "1.2.3"))
        self.assertEqual(self.statement(b"Bose package", **self.production(SOUNDTAP_PACKAGE_VERSION="stock")),
                         ("core", "production", "stock"))
        # An alpha is a release too: production, with its prerelease version (ADR 0035).
        self.assertEqual(self.statement(b"BOSE package", **self.production(SOUNDTAP_PACKAGE_VERSION="0.1.0-alpha.1")),
                         ("core", "production", "0.1.0-alpha.1"))

    def test_production_refuses_a_key_without_a_passphrase(self):
        out, sig = self.sign(b"Bose package", SOUNDTAP_SIGN_PROFILE="production")
        self.assertNotEqual(out.returncode, 0, out.stdout)
        self.assertIn("has no passphrase", out.stderr)
        self.assertIsNone(sig)
        # Saying how to read a passphrase does not make a plain key pass.
        out, sig = self.sign(b"Bose package", SOUNDTAP_SIGN_PROFILE="production",
                             SOUNDTAP_RELEASE_KEY_PASSIN="pass:x")
        self.assertIn("has no passphrase", out.stderr)
        self.assertIsNone(sig)

    def test_alpha_may_use_either_key(self):
        self.assertEqual(self.statement(b"Bose package"), ("core", "alpha", "0.4.0"))
        self.assertEqual(self.statement(b"Bose package", key=self.encrypted,
                                        SOUNDTAP_RELEASE_KEY_PASSIN="pass:x"), ("core", "alpha", "0.4.0"))

    def test_a_wrong_passphrase_signs_nothing(self):
        out, sig = self.sign(b"Bose package", **dict(self.production(), SIGN_TEST_PASS="y"))
        self.assertNotEqual(out.returncode, 0, out.stdout)
        self.assertNotIn("signed", out.stdout)
        self.assertIsNone(sig)

    def test_an_owner_signs_with_their_own_key(self):
        # ADR 0048: any ed25519 key in SOUNDTAP_OWNER_KEY_FILE, not release.PublicKey; sign.sh
        # says what to set on the speaker.
        own = self.tmp / "own.pem"
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", str(own)],
                       check=True, capture_output=True)
        der = subprocess.run(["openssl", "pkey", "-in", str(own), "-pubout", "-outform", "DER"],
                             check=True, capture_output=True).stdout
        subprocess.run(["openssl", "pkey", "-in", str(own), "-pubout", "-out", str(self.pub)],
                       check=True, capture_output=True)
        # The release key alone is refused for it, as for any key that is not release.PublicKey.
        out, sig = self.sign(b"Owner build", key=own)
        self.assertIn("does not match release.PublicKey", out.stderr)
        self.assertIsNone(sig)
        out, sig = self.sign(b"Owner build", SOUNDTAP_OWNER_KEY_FILE=str(own))
        self.assertEqual(out.returncode, 0, out.stderr)
        pub = base64.b64encode(der[-32:]).decode()
        fingerprint = hashlib.sha256(der[-32:]).hexdigest()[:16]
        self.assertIn(f"owner key {pub}, fingerprint {fingerprint}", out.stdout)
        self.assertEqual(self.statement(b"Owner build", SOUNDTAP_OWNER_KEY_FILE=str(own)), ("core", "alpha", "0.4.0"))
        # A production signature holds an owner key to the passphrase rule as well.
        out, sig = self.sign(b"Owner build", SOUNDTAP_OWNER_KEY_FILE=str(own), SOUNDTAP_SIGN_PROFILE="production")
        self.assertIn("has no passphrase", out.stderr)
        self.assertIsNone(sig)

    def test_refusals_write_nothing(self):
        for env in ({"SOUNDTAP_PACKAGE_KIND": "app", "SOUNDTAP_PACKAGE_VERSION": "stock"},
                    {"SOUNDTAP_PACKAGE_KIND": "bco"},
                    {"SOUNDTAP_SIGN_PROFILE": "beta"}):
            out, sig = self.sign(b"\x7fELF binary", **env)
            self.assertNotEqual(out.returncode, 0, env)
            self.assertIsNone(sig, env)


if __name__ == "__main__":
    unittest.main()
