# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The CI entry points, run against a fake tree with a stub `make` (ADR 0011).

tools/ci/run-ci.sh and tools/ci/run-build.sh are what every CI job and a build host call, so what they hand to make, which candidate they name and which install-gate cells
they run is what CI does. Each test copies the script into a temporary tree, so its logs and
dist/ land there, and puts stubs where make and the gate scripts would be.
"""

import hashlib
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]

# Records its arguments; fails for the targets named in FAIL; `make stu` leaves the
# package, the preview package (ADR 0133) and the images the stu step expects.
STUB_MAKE = """#!/bin/sh
echo "$*" >>"$TREE/make.calls"
for a in "$@"; do last=$a; done
case " $FAIL " in *" $last "*) echo "stub: $last fails"; exit 3 ;; esac
if [ "$last" = stu ]; then
	mkdir -p "$TREE/dist" "$TREE/output/os/images"
	printf 'package bytes' >"$TREE/dist/Update-soundtap-buildroot.stu"
	printf 'preview bytes' >"$TREE/dist/Update-soundtap-buildroot-preview.stu"
	echo sums >"$TREE/output/os/images/SHA256SUMS"
	echo config >"$TREE/output/os/images/kernel.config"
fi
echo "stub: $last"
"""

STUB_SCRIPT = """#!/bin/sh
echo "$(basename "$0") $* SCENARIOS=${SCENARIOS:-}" >>"$TREE/scripts.calls"
"""


class EntryTree:
    """A temporary tree holding the CI entry scripts, a stub make and stub gate scripts."""

    def __init__(self, tmp: str) -> None:
        """Lay out the tree in tmp."""
        self.tree = Path(tmp)
        (self.tree / "os/tests/installer").mkdir(parents=True)
        (self.tree / "tools/ci").mkdir(parents=True)
        (self.tree / "os/VERSION").write_text("0.3.1\n")
        for name in ("run-ci.sh", "run-build.sh"):
            shutil.copy(ROOT / "tools/ci" / name, self.tree / "tools/ci" / name)
        for name in ("os/tests/installer/nandsim-install-test.sh", "os/tests/installer/rootfs-run-test.sh",
                     "tools/ci/buildroot-reuse.sh"):
            (self.tree / name).write_text(STUB_SCRIPT)
        self.make = self.tree / "make"
        self.make.write_text(STUB_MAKE)
        self.make.chmod(0o755)

    def run(self, script: str, *args: str, **env: str) -> subprocess.CompletedProcess[str]:
        """Run tools/ci/script with args and the extra environment env."""
        full = {k: v for k, v in os.environ.items()
                if k not in ("SCENARIOS", "MODELS", "SOUNDTAP_COMMIT", "SOUNDTAP_PROFILE")}
        full.update(MAKE=str(self.make), TREE=str(self.tree), FAIL="")
        full.update(env)
        return subprocess.run(["sh", str(self.tree / "tools/ci" / script), *args], check=False, env=full,
                              cwd="/", text=True, capture_output=True, timeout=60)

    def calls(self, name: str) -> list[str]:
        """Return the calls the stubs recorded in name, or none."""
        path = self.tree / name
        return path.read_text().splitlines() if path.exists() else []


class RunCITest(unittest.TestCase):
    """run-ci.sh runs the make targets of the steps it is given."""

    def test_one_step_runs_its_make_target_in_the_foreground(self) -> None:
        """One step runs its make target in the foreground."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            result = t.run("run-ci.sh", "go-race")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("stub: test-go-race", result.stdout)
            self.assertEqual(t.calls("make.calls"), [f"-C {t.tree} --no-print-directory test-go-race"])

    def test_steps_run_side_by_side_and_a_failure_fails_the_run(self) -> None:
        """Several steps run side by side with their own logs, and one failure fails the run."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            result = t.run("run-ci.sh", "lint", "web", "bco", "vuln", FAIL="test-web")
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertIn("ok    lint", result.stdout)
            self.assertIn("FAIL  web (exit 3)", result.stdout)
            self.assertIn("stub: test-web fails", result.stdout)  # the failed log's tail
            targets = sorted(c.split()[-1] for c in t.calls("make.calls"))
            self.assertEqual(targets, ["lint", "test-bco", "test-web", "vuln"])
            self.assertTrue((t.tree / "output/ci/bco.log").exists())

    def test_without_arguments_every_ci_step_runs(self) -> None:
        """Without arguments every CI step runs."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            result = t.run("run-ci.sh")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            targets = sorted(c.split()[-1] for c in t.calls("make.calls"))
            self.assertEqual(targets, ["lint", "test-bco", "test-e2e", "test-firewall", "test-go",
                                       "test-go-race", "test-python", "test-web", "vuln"])

    def test_an_unknown_step_runs_nothing(self) -> None:
        """An unknown step stops the run with exit 2 before any make runs."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            result = t.run("run-ci.sh", "go", "nope")
            self.assertEqual(result.returncode, 2)
            self.assertIn("unknown step nope", result.stderr)
            self.assertEqual(t.calls("make.calls"), [])


@unittest.skipUnless(shutil.which("sha256sum"), "GNU checksum tools required")
class RunBuildTest(unittest.TestCase):
    """run-build.sh builds, names and gates the candidate."""

    def test_stu_names_the_candidate_after_the_version_with_its_commit_and_profile(self) -> None:
        """The stu step names both packages after the version, with sums, commit, profile and image records."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            result = t.run("run-build.sh", "stu", SOUNDTAP_COMMIT="c0ffee", SOUNDTAP_PROFILE="production")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            dist = t.tree / "dist"
            stu = dist / "SoundTap-Core-0.3.1.stu"
            self.assertEqual(stu.read_bytes(), b"package bytes")
            self.assertFalse((dist / "Update-soundtap-buildroot.stu").exists())
            digest = hashlib.sha256(b"package bytes").hexdigest()
            self.assertEqual((dist / "SoundTap-Core-0.3.1.stu.sha256").read_text(),
                             f"{digest}  SoundTap-Core-0.3.1.stu\n")
            # The preview package beside it, named after the same version, with its own sum.
            preview = dist / "SoundTap-Core-0.3.1-preview.stu"
            self.assertEqual(preview.read_bytes(), b"preview bytes")
            self.assertFalse((dist / "Update-soundtap-buildroot-preview.stu").exists())
            digest = hashlib.sha256(b"preview bytes").hexdigest()
            self.assertEqual((dist / "SoundTap-Core-0.3.1-preview.stu.sha256").read_text(),
                             f"{digest}  SoundTap-Core-0.3.1-preview.stu\n")
            self.assertEqual((dist / "commit").read_text(), "c0ffee\n")
            self.assertEqual((dist / "profile").read_text(), "production\n")
            self.assertEqual((dist / "SHA256SUMS").read_text(), "sums\n")
            self.assertEqual((dist / "kernel.config").read_text(), "config\n")

    def test_default_steps_build_then_package_without_reuse(self) -> None:
        """Without steps the build runs os then stu, reuses nothing and defaults to alpha."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            result = t.run("run-build.sh", SOUNDTAP_COMMIT="c0ffee")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual([c.split()[-1] for c in t.calls("make.calls")], ["os", "stu"])
            self.assertEqual(t.calls("scripts.calls"), [])
            self.assertEqual((t.tree / "dist/profile").read_text(), "alpha\n")

    def test_reuse_wraps_a_build_that_worked_and_records_nothing_after_a_failure(self) -> None:
        """--reuse records the tree after a build that worked and never after a failure."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            self.assertEqual(t.run("run-build.sh", "--reuse", "os").returncode, 0)
            self.assertEqual([c.split()[1] for c in t.calls("scripts.calls")], ["auto", "record"])
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            self.assertNotEqual(t.run("run-build.sh", "--reuse", "os", FAIL="os").returncode, 0)
            self.assertEqual([c.split()[1] for c in t.calls("scripts.calls")], ["auto"])

    def test_gate_runs_two_cells_per_model_on_the_versioned_candidate(self) -> None:
        """The gate runs two cells per model on the versioned candidate."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            result = t.run("run-build.sh", "gate", MODELS="wla st300")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            (call,) = t.calls("scripts.calls")
            name, package, scenarios = call.split(" ", 2)
            self.assertEqual(name, "nandsim-install-test.sh")
            self.assertEqual(package, "dist/SoundTap-Core-0.3.1.stu")
            self.assertEqual(scenarios.split("=", 1)[1].split(),
                             ["wla/8/some", "wla/9/none", "st300/8/none", "st300/9/some"])

    def test_gate_covers_all_four_models_unless_told_otherwise(self) -> None:
        """The gate covers all four models unless MODELS says otherwise."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            self.assertEqual(t.run("run-build.sh", "gate").returncode, 0)
            cells = t.calls("scripts.calls")[0].split("SCENARIOS=", 1)[1].split()
            self.assertEqual(sorted({c.split("/")[0] for c in cells}), ["st10", "st20", "st300", "wla"])
            self.assertEqual(len(cells), 8)
            self.assertEqual(len([c for c in cells if c.endswith("/cut")]), 2)

    def test_unknown_models_and_steps_are_refused(self) -> None:
        """An unknown model or step is refused before anything runs."""
        with tempfile.TemporaryDirectory() as tmp:
            t = EntryTree(tmp)
            self.assertNotEqual(t.run("run-build.sh", "gate", MODELS="st30").returncode, 0)
            self.assertEqual(t.run("run-build.sh", "os", "flash").returncode, 2)
            self.assertEqual(t.calls("scripts.calls") + t.calls("make.calls"), [])


if __name__ == "__main__":
    unittest.main()
