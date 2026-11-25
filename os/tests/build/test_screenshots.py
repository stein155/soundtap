# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""screenshots.py: the README images name the web UI they were made from.

make screenshots records the web UI the README images were made from, and a
release refuses a commit whose UI or screenshot tools changed since. Runs the script against a
throwaway git repository; nothing is built or photographed.
"""

import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/release/screenshots.py"
ENV = {**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.invalid",
       "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid",
       "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"}


class ScreenshotsTest(unittest.TestCase):
    """The record make screenshots writes and the release check against it."""

    def setUp(self) -> None:
        """Create a throwaway repository with a UI, screenshot tools and old images."""
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = pathlib.Path(self.tmp.name)
        self.git("init", "-q")
        self.write("app/frontend/src/App.tsx", "export const App = 1;\n")
        self.write("docs/tools/shoot.mjs", "// shots\n")
        self.write("docs/assets/screens.png", "old pixels\n")
        self.write("README.md", "# SoundTap\n")
        self.commit("start")

    def tearDown(self) -> None:
        """Remove the throwaway repository."""
        self.tmp.cleanup()

    def git(self, *args: str) -> str:
        """Run git in the throwaway repository and return its output."""
        return subprocess.run(["git", "-C", str(self.repo), "-c", "commit.gpgsign=false", *args],
                              env=ENV, check=True, capture_output=True, text=True).stdout.strip()

    def write(self, path: str, text: str) -> None:
        """Write text to path in the throwaway repository."""
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)

    def commit(self, message: str) -> str:
        """Commit everything and return the new commit."""
        self.git("add", "-A")
        self.git("commit", "-q", "-m", message)
        return self.git("rev-parse", "HEAD")

    def run_script(self, *args: str) -> subprocess.CompletedProcess[str]:
        """Run screenshots.py on the throwaway repository."""
        return subprocess.run([sys.executable, "-I", str(SCRIPT), "--root", str(self.repo), *args],
                              check=False, env=ENV, capture_output=True, text=True)

    def make_screenshots(self) -> str:
        """Do what make screenshots does: check, refresh the images, record, commit."""
        self.assertEqual(self.run_script("ready").returncode, 0)
        self.write("docs/assets/screens.png", "new pixels\n")
        done = self.run_script("record")
        self.assertEqual(done.returncode, 0, done.stderr)
        return self.commit("docs: refresh the README screenshots")

    def test_a_commit_without_a_record_is_refused(self) -> None:
        """A commit without a record is refused with a pointer to make screenshots."""
        done = self.run_script("check", "HEAD")
        self.assertEqual(done.returncode, 1)
        self.assertIn("no docs/assets/screenshots.source", done.stderr)
        self.assertIn("make screenshots", done.stderr)

    def test_the_commit_the_screenshots_were_made_on_passes(self) -> None:
        """The commit the screenshots were made on passes, and its record names its trees."""
        commit = self.make_screenshots()
        done = self.run_script("check", commit)
        self.assertEqual(done.returncode, 0, done.stderr)
        record = self.git("show", f"{commit}:docs/assets/screenshots.source")
        self.assertIn("app/frontend " + self.git("rev-parse", f"{commit}:app/frontend"), record)
        self.assertIn("docs/tools " + self.git("rev-parse", f"{commit}:docs/tools"), record)

    def test_changes_elsewhere_keep_the_screenshots_current(self) -> None:
        """Changes outside the UI and the screenshot tools keep the screenshots current."""
        self.make_screenshots()
        self.write("README.md", "# SoundTap\n\nThe SoundTouch 300 is supported.\n")
        self.write("os/VERSION", "0.2.0\n")
        commit = self.commit("raise the version")
        self.assertEqual(self.run_script("check", commit).returncode, 0)

    def test_a_ui_change_after_the_screenshots_is_refused(self) -> None:
        """A UI change after the screenshots is refused, while the older commit still passes."""
        made = self.make_screenshots()
        self.write("app/frontend/src/App.tsx", "export const App = 2;\n")
        commit = self.commit("feat: a new page")
        done = self.run_script("check", commit)
        self.assertEqual(done.returncode, 1)
        self.assertIn("app/frontend", done.stderr)
        self.assertNotIn("docs/tools", done.stderr.split("run make")[0])
        # The guard reads the record of the commit it checks.
        self.assertEqual(self.run_script("check", made).returncode, 0)

    def test_a_screenshot_tool_change_is_refused(self) -> None:
        """A change to the screenshot tools after the screenshots is refused."""
        self.make_screenshots()
        self.write("docs/tools/shoot.mjs", "// shots, one more page\n")
        commit = self.commit("docs: shoot one more page")
        done = self.run_script("check", commit)
        self.assertEqual(done.returncode, 1)
        self.assertIn("docs/tools", done.stderr)

    def test_a_hand_edited_record_is_refused(self) -> None:
        """A record edited by hand is refused."""
        self.make_screenshots()
        record = self.repo / "docs/assets/screenshots.source"
        record.write_text(record.read_text().replace("app/frontend ", "app/frontend 0"))
        commit = self.commit("edit the record")
        self.assertEqual(self.run_script("check", commit).returncode, 1)

    def test_uncommitted_ui_changes_are_not_recorded(self) -> None:
        """Uncommitted UI or tool changes are refused and leave no record."""
        for change in ("app/frontend/src/App.tsx", "app/frontend/src/New.tsx", "docs/tools/x.mjs"):
            with self.subTest(change=change):
                self.write(change, "// not committed\n")
                self.assertEqual(self.run_script("ready").returncode, 1)
                done = self.run_script("record")
                self.assertEqual(done.returncode, 1)
                self.assertIn("uncommitted", done.stderr)
                self.assertFalse((self.repo / "docs/assets/screenshots.source").exists())
                self.git("checkout", "-q", "--", ".")
                self.git("clean", "-q", "-fd")

    def test_an_unknown_commit_is_refused(self) -> None:
        """An unknown commit is refused."""
        done = self.run_script("check", "0" * 40)
        self.assertEqual(done.returncode, 1)


if __name__ == "__main__":
    unittest.main()
