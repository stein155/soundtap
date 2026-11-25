# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""changelog.py: a release refuses a tag without its CHANGELOG.md section.

Runs the script against throwaway text and a throwaway git repository, and checks the real CHANGELOG.md keeps
the shape the script and Keep a Changelog expect.
"""

import os
import pathlib
import re
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/release/changelog.py"
sys.path.insert(0, str(SCRIPT.parent))
import changelog  # noqa: E402

ENV = {**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.invalid",
       "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid",
       "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"}

TEXT = """# Changelog

## [Unreleased]

### Added

- Something new.

## [app-v0.1.1] - 2026-12-10

### Fixed

- The alarm rings again.

## [os-v0.1.0] - 2026-12-01

### Added

- The first release.

## [app-v0.1.0] - 2026-12-01

## [os-v0.1.0-alpha.1] - 2026-11-23

- The first alpha.

## [os-v0.0.9]

- Undated.
"""


class SectionTest(unittest.TestCase):
    """Which CHANGELOG.md sections a tag passes or is refused with."""

    def test_a_tag_with_a_dated_section_of_entries_passes(self) -> None:
        """A tag with a dated section of entries passes, an alpha's included."""
        self.assertEqual(changelog.section(TEXT, "os-v0.1.0"), "### Added\n\n- The first release.\n")
        self.assertIn("alarm", changelog.section(TEXT, "app-v0.1.1"))
        # An alpha (ADR 0035) has its own section like any tag.
        self.assertEqual(changelog.section(TEXT, "os-v0.1.0-alpha.1"), "- The first alpha.\n")

    def test_missing_empty_undated_and_unknown_tags_are_refused(self) -> None:
        """A missing, empty or undated section, or a tag that is not a release's, is refused."""
        for tag, error in (("os-v0.2.0", "no '## [os-v0.2.0]"),
                           ("os-v0.1.0-alpha.2", "no '## [os-v0.1.0-alpha.2]"),
                           ("os-v0.1.0-beta.1", "not a release tag"),
                           ("os-v0.1.0-alpha.0", "not a release tag"),
                           ("app-v0.1.0", "lists no changes"),
                           ("os-v0.0.9", "release date"),
                           ("v0.1.0", "not a release tag"),
                           ("Unreleased", "not a release tag")):
            with self.subTest(tag=tag), self.assertRaisesRegex(ValueError, re.escape(error)):
                changelog.section(TEXT, tag)

    def test_a_tag_listed_twice_is_refused(self) -> None:
        """A tag with two sections is refused."""
        with self.assertRaisesRegex(ValueError, "2 sections"):
            changelog.section(TEXT + "\n## [os-v0.1.0] - 2026-12-02\n\n- Again.\n", "os-v0.1.0")

    def test_the_section_is_read_from_the_named_commit(self) -> None:
        """With --commit the section is read from that commit, not the working tree."""
        with tempfile.TemporaryDirectory() as tmp:
            repo = pathlib.Path(tmp)

            def git(*args: str) -> str:
                return subprocess.run(["git", "-C", tmp, "-c", "commit.gpgsign=false", *args], env=ENV,
                                      check=True, capture_output=True, text=True).stdout.strip()

            git("init", "-q")
            (repo / "CHANGELOG.md").write_text(TEXT)
            git("add", "-A")
            git("commit", "-q", "-m", "start")
            first = git("rev-parse", "HEAD")
            (repo / "CHANGELOG.md").write_text("# Changelog\n")  # The working tree lost it.

            def run(*args: str) -> subprocess.CompletedProcess[str]:
                return subprocess.run([sys.executable, "-I", str(SCRIPT), "check", *args,
                                       "--root", tmp], check=False, env=ENV, capture_output=True, text=True)

            ok = run("os-v0.1.0", "--commit", first)
            self.assertEqual(ok.returncode, 0, ok.stderr)
            self.assertNotEqual(run("os-v0.1.0").returncode, 0)
            missing = run("os-v0.2.0", "--commit", first)
            self.assertEqual(missing.returncode, 1)
            self.assertIn("no '## [os-v0.2.0]", missing.stderr)


class RepositoryChangelogTest(unittest.TestCase):
    """The repository's own CHANGELOG.md and the release paths that check it."""

    def test_the_changelog_starts_with_unreleased_and_dates_every_tag(self) -> None:
        """CHANGELOG.md starts with Unreleased and every tag below has a valid section."""
        text = (ROOT / "CHANGELOG.md").read_text()
        headings = [m for m in map(changelog.HEADING.match, text.splitlines()) if m]
        self.assertTrue(headings, "CHANGELOG.md has no '## [...]' sections")
        self.assertEqual(headings[0].group("tag"), "Unreleased")
        for m in headings[1:]:
            with self.subTest(tag=m.group("tag")):
                changelog.section(text, m.group("tag"))

    def test_dry_run_and_both_release_lanes_check_the_section(self) -> None:
        """The dry run and both release lanes check the section; the order matters less than that each asks."""
        self.assertIn('changelog.py" check "os-v$version" --commit "$commit"',
                      (ROOT / "tools/release/dry-run.sh").read_text())
        workflow = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertEqual(workflow.count('python3 tools/release/changelog.py check "$tag"'), 2)


if __name__ == "__main__":
    unittest.main()
