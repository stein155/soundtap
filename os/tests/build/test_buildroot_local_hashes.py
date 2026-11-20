# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Local package hashes pin files that live in this repository, so they drift silently.

A changed os/bootloader source without its .hash update only fails hours into a
Buildroot build (seen 2026-09-28, aic3256-loader.c). Check them here instead.
"""
import hashlib
import pathlib
import unittest

REPO = pathlib.Path(__file__).resolve().parents[3]


class LocalHashTests(unittest.TestCase):
    def test_pinned_repository_sources_match(self):
        checked = 0
        for hash_file in (REPO / "os/package").rglob("*.hash"):
            for line in hash_file.read_text().splitlines():
                parts = line.split()
                if len(parts) != 3 or parts[0] != "sha256":
                    continue
                source = next((d / parts[2] for d in (REPO / "os/drivers", REPO / "os/bootloader")
                               if (d / parts[2]).is_file()), None)
                if source is None:
                    continue
                checked += 1
                with self.subTest(hash_file=hash_file.name, source=parts[2]):
                    self.assertEqual(hashlib.sha256(source.read_bytes()).hexdigest(), parts[1],
                                     f"update {hash_file.relative_to(REPO)}")
        self.assertGreater(checked, 0)


if __name__ == "__main__":
    unittest.main()
