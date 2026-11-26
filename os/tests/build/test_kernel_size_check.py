# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""os/support/kernel_size_check.py: the kernel's size budget, run against files of known size."""

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "os/support"))

import kernel_size_check as ksc  # noqa: E402

SCRIPT = ROOT / "os/support/kernel_size_check.py"


class KernelSizeCheckTest(unittest.TestCase):
    """A kernel passes, warns within WARN_BELOW of the limit, and fails over it."""

    def setUp(self) -> None:
        """Make a temporary directory for the fake kernels."""
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)

    def kernel(self, name: str, size: int) -> Path:
        """Make a sparse file of size bytes."""
        path = self.dir / name
        with path.open("wb") as f:
            f.truncate(size)
        return path

    def run_check(self, *paths: Path, actions: bool = False) -> subprocess.CompletedProcess[str]:
        """Run the script as the build does."""
        env = {k: v for k, v in os.environ.items() if not k.startswith("GITHUB_")}
        if actions:
            env |= {"GITHUB_ACTIONS": "true", "GITHUB_STEP_SUMMARY": str(self.dir / "summary.md")}
        return subprocess.run([sys.executable, str(SCRIPT), *map(str, paths)], check=False,
                              env=env, capture_output=True, text=True)

    def test_room_left_passes_quietly(self) -> None:
        """A kernel with more than WARN_BELOW left passes and says how much is left."""
        done = self.run_check(self.kernel("uImage-st10", ksc.LIMIT - ksc.WARN_BELOW))
        self.assertEqual(done.returncode, 0)
        self.assertIn(f"{ksc.WARN_BELOW:,} bytes left", done.stdout)
        self.assertEqual(done.stderr, "")

    def test_little_room_warns_but_passes(self) -> None:
        """Less than WARN_BELOW left warns, and the build goes on."""
        done = self.run_check(self.kernel("uImage-st20", ksc.LIMIT - ksc.WARN_BELOW + 1))
        self.assertEqual(done.returncode, 0)
        self.assertIn("warning: uImage-st20", done.stderr)

    def test_exactly_the_limit_fits(self) -> None:
        """The limit itself fits, as the old shell test (-le) allowed."""
        self.assertEqual(self.run_check(self.kernel("uImage-st10", ksc.LIMIT)).returncode, 0)

    def test_over_the_limit_fails(self) -> None:
        """One byte over fails the build, also when another kernel fits."""
        done = self.run_check(self.kernel("uImage-st10", 1000), self.kernel("uImage-st20", ksc.LIMIT + 1))
        self.assertEqual(done.returncode, 1)
        self.assertIn("error: uImage-st20", done.stderr)
        self.assertIn("1 bytes over", done.stderr)

    def test_github_annotation_and_summary(self) -> None:
        """Under GitHub Actions a warning is an annotation and every size is in the summary."""
        done = self.run_check(self.kernel("uImage-st10", 1000), self.kernel("uImage-st20", ksc.LIMIT - 10),
                              actions=True)
        self.assertEqual(done.returncode, 0)
        self.assertIn("::warning title=Kernel size::uImage-st20", done.stderr)
        summary = (self.dir / "summary.md").read_text()
        self.assertIn("### Kernel size", summary)
        self.assertIn("uImage-st10: 1,000 bytes", summary)
        self.assertIn("uImage-st20:", summary)

    def test_build_scripts_use_the_checker(self) -> None:
        """Both build scripts check the kernel through this script, and hold no limit of their own.

        Text: the scripts need a whole image build to reach the check.
        """
        for name in ("post-build.sh", "post-image.sh"):
            text = (ROOT / "os/board/image" / name).read_text()
            with self.subTest(script=name):
                self.assertIn("support/kernel_size_check.py", text)
                self.assertNotIn(str(ksc.LIMIT), text)


if __name__ == "__main__":
    unittest.main()
