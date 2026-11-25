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

MATCHING = """/dts-v1/;
/ {
	chosen {
		bootargs = "console=ttyGS0,115200n8 mem=128M cma=8M panic=10";
	};
	memory@80000000 {
		device_type = "memory";
		reg = <0x80000000 0x08000000>;
	};
};
"""


class DtsMemoryTest(unittest.TestCase):
    """The memory node and the mem= boot argument of a device tree agree."""

    def write(self, text: str) -> Path:
        """Write text as a device tree source and return its path."""
        path = Path(self.tmp.name) / "tree.dts"
        path.write_text(text)
        return path

    def setUp(self) -> None:
        """Create a temporary directory for the trees."""
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def test_accepts_a_tree_whose_numbers_agree(self) -> None:
        """A tree whose mem= and memory node agree passes."""
        self.assertIsNone(check.check(self.write(MATCHING)))

    def test_rejects_the_wla_mismatch_that_corrupted_userspace(self) -> None:
        """A mem= larger than the memory node, as on the WLA that corrupted userspace, fails."""
        text = MATCHING.replace("mem=128M", "mem=256M")
        message = check.check(self.write(text))
        self.assertIsNotNone(message)
        self.assertIn("mem=256M", message.replace("mem=256M", "mem=256M"))
        self.assertIn("declares 128M", message)

    def test_rejects_a_node_that_disagrees_with_the_argument(self) -> None:
        """A memory node that disagrees with mem= fails with the size it declares."""
        text = MATCHING.replace("0x08000000", "0x10000000")
        self.assertIn("declares 256M", check.check(self.write(text)))

    def test_ignores_a_tree_with_no_memory_node(self) -> None:
        """A tree without a memory node is not checked."""
        text = MATCHING[:MATCHING.index("\tmemory@")] + "};\n"
        self.assertIsNone(check.check(self.write(text)))

    def test_ignores_a_tree_with_no_mem_argument(self) -> None:
        """A tree without a mem= argument is not checked."""
        self.assertIsNone(check.check(self.write(MATCHING.replace("mem=128M ", ""))))

    def test_the_house_figure_applies_without_being_asked(self) -> None:
        """A bare run holds every tree to 128 MiB, so a new model cannot quietly inherit a number nobody measured.

        That is how the WLA got 256.
        """
        text = MATCHING.replace("mem=128M", "mem=256M").replace("0x08000000", "0x10000000")
        self.assertIn("not the 128M", check.check(self.write(text)))

    def test_expect_catches_a_build_that_disagrees_with_the_tree(self) -> None:
        """An expected size different from the tree's fails."""
        path = self.write(MATCHING)
        self.assertIsNone(check.check(path, check.parse_size("128M")))
        message = check.check(path, check.parse_size("256M"))
        self.assertIn("is 128M, not the 256M", message)

    def test_expect_rejects_a_size_it_cannot_read(self) -> None:
        """An expected size that is not a size raises ValueError."""
        with self.assertRaises(ValueError):
            check.parse_size("lots")

    def test_every_tree_in_the_repo_agrees(self) -> None:
        """Every SoundTap device tree in the repository passes."""
        trees = sorted((ROOT / "os/board/common/dts/ti/omap").glob("am335x-soundtap-*.dts"))
        self.assertTrue(trees)
        self.assertEqual([m for m in (check.check(p) for p in trees) if m], [])


if __name__ == "__main__":
    unittest.main(verbosity=1)
