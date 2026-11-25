# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The provenance gate (os/support/check_provenance.py): every binary we ship but do not compile has a source."""

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


def entry(**kw: object) -> dict[str, object]:
    """Return a manifest entry for fw/a.bin, with `kw` on top."""
    return {"origin": "upstream", "source": "https://example.org/x", "version": "1", "license": "MIT",
            "files": {"fw/a.bin": DIGEST}, **kw}


class Provenance(unittest.TestCase):
    """The gate against a small tree, a fake image and the repository's own manifest."""

    def setUp(self) -> None:
        """Make a tree holding one blob, fw/a.bin."""
        self.tree = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tree)
        (self.tree / "fw").mkdir()
        (self.tree / "fw/a.bin").write_bytes(BLOB)

    def errors(self, manifest: list[dict[str, object]], **kw: Path) -> list[str]:
        """Return the gate's errors for `manifest` over the test tree."""
        return gate.check(manifest, tree=self.tree, **kw)

    def test_listed_blob_passes(self) -> None:
        """A blob listed with its hash passes."""
        self.assertEqual(self.errors([entry()]), [])

    def test_unlisted_blob_fails(self) -> None:
        """A blob the manifest does not list fails."""
        self.assertIn("NO PROVENANCE", self.errors([entry(files={"fw/other.bin": DIGEST})])[0])

    def test_wrong_hash_fails(self) -> None:
        """A blob whose bytes differ from the listed hash fails."""
        (self.tree / "fw/a.bin").write_bytes(BLOB + b"x")
        self.assertIn("WRONG HASH", self.errors([entry()])[0])

    def test_glob_covers_regenerated_files(self) -> None:
        """A glob covers files that are regenerated, without a hash."""
        self.assertEqual(self.errors([entry(files={}, globs=["fw/*.bin"])]), [])

    def test_bose_source_and_missing_fields_fail(self) -> None:
        """An entry sourced from Bose or without a licence fails."""
        self.assertTrue(any("Bose" in e for e in self.errors([entry(source="Bose rootfs")])))
        self.assertTrue(any("license" in e for e in self.errors([entry(license="")])))

    def test_image_firmware_needs_an_entry(self) -> None:
        """Firmware in the image needs a manifest entry."""
        rootfs = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, rootfs)
        (rootfs / "lib/firmware").mkdir(parents=True)
        (rootfs / "lib/firmware/b.bin").write_bytes(BLOB)
        errors = self.errors([entry()], rootfs=rootfs)
        self.assertTrue(any("NO PROVENANCE: image lib/firmware/b.bin" in e for e in errors), errors)

    def test_any_image_blob_needs_an_entry_but_elf_and_text_do_not(self) -> None:
        """Any binary blob in the image needs an entry; ELF files, text and /proc do not."""
        rootfs = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, rootfs)
        (rootfs / "usr/share/x").mkdir(parents=True)
        (rootfs / "usr/share/x/prebuilt.dat").write_bytes(BLOB)
        (rootfs / "usr/share/x/prog").write_bytes(b"\x7fELF\0\0\0")
        (rootfs / "usr/share/x/notes.txt").write_text("text\n")
        (rootfs / "proc").mkdir()
        (rootfs / "proc/ignored").write_bytes(BLOB)
        errors = self.errors([entry()], rootfs=rootfs)
        self.assertEqual([e for e in errors if "image" in e],
                         ["NO PROVENANCE: image usr/share/x/prebuilt.dat is not in os/upstream/MANIFEST.json"])
        self.assertEqual(self.errors([entry(), entry(files={}, globs=["/usr/share/x/*.dat"])], rootfs=rootfs), [])

    def test_the_manifest_covers_what_the_build_makes(self) -> None:
        """The image's generated binaries, as a build of 2026-11-22 holds them."""
        rootfs = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, rootfs)
        made = ["boot/uImage-spotty", "boot/am335x-soundtap-st20.dtb", "lib/modules/6.12.117/modules.dep.bin",
                "usr/share/terminfo/x/xterm", "usr/share/soundtap/bco-app.bcd"]
        for rel in made:
            (rootfs / rel).parent.mkdir(parents=True, exist_ok=True)
            (rootfs / rel).write_bytes(BLOB)
        manifest = [e for e in json.loads(gate.MANIFEST.read_text()) if "globs" in e]
        self.assertEqual([e for e in gate.check(manifest, tree=self.tree, rootfs=rootfs) if "image" in e], [])

    def test_repository_manifest_covers_the_repository(self) -> None:
        """The repository's own manifest covers every blob in the repository."""
        self.assertEqual(gate.check(json.loads(gate.MANIFEST.read_text())), [])


if __name__ == "__main__":
    unittest.main()
