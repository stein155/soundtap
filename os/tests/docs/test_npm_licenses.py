# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import hashlib
import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("npm_licenses", ROOT / "tools/ci/npm_licenses.py")
lic = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(lic)


class PackageDirTest(unittest.TestCase):
    def test_scoped_and_unscoped_same_basename_split(self):
        plain = {"url": "https://registry.npmjs.org/react/-/react-19.3.0.tgz"}
        scoped = {"url": "https://registry.npmjs.org/@types/react/-/react-19.3.0.tgz"}
        self.assertNotEqual(lic.package_dir(plain), lic.package_dir(scoped))

    def test_the_bundle_matches_the_lock(self):
        lock = ROOT / "app/frontend/package-lock.json"
        bundle = ROOT / "app/frontend/THIRD_PARTY_LICENSES.txt"
        digest = hashlib.sha256(lock.read_bytes()).hexdigest()
        self.assertEqual(bundle.read_text().splitlines()[1], f"package-lock sha256: {digest}")


if __name__ == "__main__":
    unittest.main()
