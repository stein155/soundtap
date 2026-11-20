# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""shell_scripts.py lists what `make lint-shell` checks: every tracked *.sh and every tracked
file with a sh shebang, never a submodule or an untracked file. Run on a throwaway git
repository."""

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CHECK = ROOT / "tools/ci/shell_scripts.py"


def git(repo, *args):
    subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True)


class ShellScriptsTest(unittest.TestCase):
    def test_lists_tracked_scripts_by_name_or_shebang(self):
        repo = Path(self.enterContext(tempfile.TemporaryDirectory()))
        git(repo, "init", "-q")
        files = {
            "build.sh": "echo build\n",
            "etc/init.d/soundtap": "#!/bin/sh\necho init\n",
            "tools/run": "#!/usr/bin/env bash\necho run\n",
            "hooks/ash": "#!/bin/ash -e\n",
            "notes/zsh": "#!/bin/zsh\n",
            "tool.py": "#!/usr/bin/env python3\n",
            "icon.png": "\x89PNG\r\n\x1a\n",
            "with space.sh": "echo spaced\n",
            "app/vendor/golang.org/x/sys/unix/mkall.sh": "#!/usr/bin/env bash\n",
        }
        for name, text in files.items():
            (repo / name).parent.mkdir(parents=True, exist_ok=True)
            (repo / name).write_text(text)
        (repo / "untracked.sh").write_text("echo no\n")
        git(repo, "add", *files)
        git(repo, "update-index", "--add", "--cacheinfo", f"160000,{'a' * 40},buildroot")
        out = subprocess.run([sys.executable, str(CHECK), str(repo)], check=True,
                             capture_output=True).stdout.decode()
        self.assertTrue(out.endswith("\0"))
        self.assertEqual(sorted(out.split("\0")[:-1]),
                         ["build.sh", "etc/init.d/soundtap", "hooks/ash", "tools/run", "with space.sh"])

    def test_vendored_modules_are_left_out(self):
        out = subprocess.run([sys.executable, str(CHECK), str(ROOT)], check=True,
                             capture_output=True).stdout.decode().split("\0")
        self.assertEqual([p for p in out if p.startswith("app/vendor/")], [])

    def test_the_repository_init_script_is_listed(self):
        out = subprocess.run([sys.executable, str(CHECK), str(ROOT)], check=True,
                             capture_output=True).stdout.decode().split("\0")
        self.assertIn("os/overlay/etc/init.d/soundtap", out)
        self.assertIn("os/board/rootfs/rootfs-overlay/init", out)


if __name__ == "__main__":
    unittest.main()
