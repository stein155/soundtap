#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Tests for check_dts_memory, including a run over the trees this repo ships."""

import importlib.util
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("check_dts_memory",
                                              ROOT / "os/support/check_dts_memory.py")
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)

MATCHING = '''/dts-v1/;
/ {
	chosen {
		bootargs = "console=ttyGS0,115200n8 mem=128M cma=8M panic=10";
	};
	memory@80000000 {
		device_type = "memory";
		reg = <0x80000000 0x08000000>;
	};
};
'''


class DtsMemoryTest(unittest.TestCase):
    def write(self, text):
        path = Path(self.tmp.name) / "tree.dts"
        path.write_text(text)
        return path

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def test_accepts_a_tree_whose_numbers_agree(self):
        self.assertIsNone(check.check(self.write(MATCHING)))

    def test_rejects_the_wla_mismatch_that_corrupted_userspace(self):
        text = MATCHING.replace("mem=128M", "mem=256M")
        message = check.check(self.write(text))
        self.assertIsNotNone(message)
        self.assertIn("mem=256M", message.replace("mem=256M", "mem=256M"))
        self.assertIn("declares 128M", message)

    def test_rejects_a_node_that_disagrees_with_the_argument(self):
        text = MATCHING.replace("0x08000000", "0x10000000")
        self.assertIn("declares 256M", check.check(self.write(text)))

    def test_ignores_a_tree_with_no_memory_node(self):
        text = MATCHING[:MATCHING.index("\tmemory@")] + "};\n"
        self.assertIsNone(check.check(self.write(text)))

    def test_ignores_a_tree_with_no_mem_argument(self):
        self.assertIsNone(check.check(self.write(MATCHING.replace("mem=128M ", ""))))

    def test_the_house_figure_applies_without_being_asked(self):
        # A bare run holds every tree to 128 MiB, so a new model cannot quietly
        # inherit a number nobody measured -- which is how the WLA got 256.
        text = MATCHING.replace("mem=128M", "mem=256M").replace("0x08000000", "0x10000000")
        self.assertIn("not the 128M", check.check(self.write(text)))

    def test_expect_catches_a_build_that_disagrees_with_the_tree(self):
        path = self.write(MATCHING)
        self.assertIsNone(check.check(path, check.parse_size("128M")))
        message = check.check(path, check.parse_size("256M"))
        self.assertIn("is 128M, not the 256M", message)

    def test_expect_rejects_a_size_it_cannot_read(self):
        with self.assertRaises(ValueError):
            check.parse_size("lots")

    def test_every_tree_in_the_repo_agrees(self):
        trees = sorted((ROOT / "os/board/common/dts/ti/omap").glob("am335x-soundtap-*.dts"))
        self.assertTrue(trees)
        self.assertEqual([m for m in (check.check(p) for p in trees) if m], [])


if __name__ == "__main__":
    unittest.main(verbosity=1)
