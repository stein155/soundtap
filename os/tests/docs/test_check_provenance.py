# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import hashlib
import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "support"))
import check_provenance as gate

BLOB = b"\0blob\0"
DIGEST = hashlib.sha256(BLOB).hexdigest()


def entry(**kw):
    return {"origin": "upstream", "source": "https://example.org/x", "version": "1", "license": "MIT",
            "files": {"fw/a.bin": DIGEST}, **kw}


class Provenance(unittest.TestCase):
    def setUp(self):
        self.tree = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tree)
        (self.tree / "fw").mkdir()
        (self.tree / "fw/a.bin").write_bytes(BLOB)

    def errors(self, manifest, **kw):
        return gate.check(manifest, tree=self.tree, **kw)

    def test_listed_blob_passes(self):
        self.assertEqual(self.errors([entry()]), [])

    def test_unlisted_blob_fails(self):
        self.assertIn("NO PROVENANCE", self.errors([entry(files={"fw/other.bin": DIGEST})])[0])

    def test_wrong_hash_fails(self):
        (self.tree / "fw/a.bin").write_bytes(BLOB + b"x")
        self.assertIn("WRONG HASH", self.errors([entry()])[0])

    def test_glob_covers_regenerated_files(self):
        self.assertEqual(self.errors([entry(files={}, globs=["fw/*.bin"])]), [])

    def test_bose_source_and_missing_fields_fail(self):
        self.assertTrue(any("Bose" in e for e in self.errors([entry(source="Bose rootfs")])))
        self.assertTrue(any("license" in e for e in self.errors([entry(license="")])))

    def test_image_firmware_needs_an_entry(self):
        rootfs = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, rootfs)
        (rootfs / "lib/firmware").mkdir(parents=True)
        (rootfs / "lib/firmware/b.bin").write_bytes(BLOB)
        errors = self.errors([entry()], rootfs=rootfs)
        self.assertTrue(any("NO PROVENANCE: image lib/firmware/b.bin" in e for e in errors), errors)

    def test_repository_manifest_covers_the_repository(self):
        self.assertEqual(gate.check(json.loads(gate.MANIFEST.read_text())), [])


if __name__ == "__main__":
    unittest.main()
