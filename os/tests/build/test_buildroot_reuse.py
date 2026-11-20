# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""What tools/ci/buildroot-reuse.sh actually does to a tree, not just what it says."""
import shutil
import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/ci/buildroot-reuse.sh"
OUT = ROOT / "output/os"

STAMPS = (".stamp_rsynced", ".stamp_configured", ".stamp_built",
          ".stamp_target_installed", ".stamp_staging_installed")


def clear_output():
    """Restore any permissions a test set before removing, so nothing survives into the
    next test -- setUp skips when output/ exists, which would silently pass everything."""
    for path in (ROOT / "output").rglob("*"):
        if path.is_dir():
            path.chmod(0o700)
    shutil.rmtree(ROOT / "output", ignore_errors=True)


def run(mode):
    return subprocess.run([str(SCRIPT), mode], cwd=ROOT,
                          capture_output=True, text=True, check=True).stdout


class BuildrootReuseTests(unittest.TestCase):
    def setUp(self):
        if (ROOT / "output").exists():
            self.skipTest("a real build tree is present; refusing to touch it")
        self.addCleanup(clear_output)
        for package in ("soundtapd-1.0", "soundtap-runtime-1.0", "boost-1.87.0", "linux-6.12"):
            directory = OUT / "build" / package
            directory.mkdir(parents=True)
            for stamp in STAMPS:
                (directory / stamp).touch()
        (OUT / "host/bin").mkdir(parents=True)
        (OUT / "host/bin/cmake").touch()
        (OUT / "target/usr/bin").mkdir(parents=True)
        (OUT / "target/usr/bin/gone-from-the-overlay").touch()

    def test_a_tree_with_no_recorded_key_is_thrown_away(self):
        # Without a key there is nothing to say the tree matches this configuration.
        self.assertIn("from scratch", run("auto"))
        self.assertFalse(OUT.exists())

    def test_a_tree_that_cannot_be_deleted_is_still_moved_out_of_the_way(self):
        """On 2026-09-16 a run died here: `rm: cannot remove
        output/nand/build/host-cmake-4.3.2: Directory not empty`. A cancelled run's
        make was still writing into the tree while the next run tried to clear it, and
        the step failed before the build even started. Whatever the old tree is doing,
        this run has to get a clean directory."""
        stuck = OUT / "build" / "host-cmake-4.3.2"
        stuck.mkdir(parents=True)
        (stuck / "busy").touch()
        stuck.chmod(0o500)  # rm cannot unlink inside it; mv of the tree above still can
        # Only matters if the assertion below fails and the tree is still there.
        self.addCleanup(lambda: stuck.exists() and stuck.chmod(0o700))
        self.assertIn("from scratch", run("auto"))
        self.assertFalse(OUT.exists(), "the build tree must be gone from where make looks")

    def test_reuse_none_throws_the_tree_away(self):
        run("record")
        self.assertIn("from scratch", run("none"))
        self.assertFalse(OUT.exists())

    def test_a_changed_toolchain_throws_everything_away(self):
        run("record")
        (OUT / ".soundtap-build-key").write_text("some other host other target\n")
        self.assertIn("toolchain or a host-side config option changed", run("auto"))
        self.assertFalse(OUT.exists())

    def test_a_board_change_keeps_the_host_tree(self):
        # The host tools are over 40 of the 97 minutes and depend on the Buildroot
        # version, the defconfig and the toolchain -- not on the board directory. A
        # one-line device tree change used to throw host-cmake away with them.
        run("record")
        key = OUT / ".soundtap-build-key"
        host = key.read_text().split()[0]
        key.write_text(f"{host} a-different-board\n")
        self.assertIn("keeping the host tree only", run("auto"))
        self.assertTrue((OUT / "host/bin/cmake").exists(), "the host tree was thrown away")
        for gone in ("build/boost-1.87.0", "target", "images"):
            self.assertFalse((OUT / gone).exists(), f"{gone} survived a board change")

    def test_a_board_change_reinstalls_the_bootloader_images(self):
        """buildroot-nand run 36774108811: a board change discarded images/, the
        bootloader package kept its install stamp, and the rootfs step then found no
        bootloader images. Run 39944981315 hit the same with the BCO app. The build is
        kept; only the copy runs again."""
        packages = [OUT / "build/host-soundtap-bootloaders-2026.07", OUT / "build/host-soundtap-bco-app-1"]
        for package in packages:
            package.mkdir(parents=True)
            for stamp in (*STAMPS, ".stamp_host_installed"):
                (package / stamp).touch()
        run("record")
        key = OUT / ".soundtap-build-key"
        host, config = key.read_text().split()[:2]
        key.write_text(f"{host} {config} a-different-board\n")
        self.assertIn("the board configuration changed", run("auto"))
        for package in packages:
            self.assertFalse((package / ".stamp_host_installed").exists(),
                             f"images/ is gone but {package.name} would not be copied back")
            self.assertTrue((package / ".stamp_built").exists(), f"{package.name} would rebuild")

    def test_an_unfinished_build_after_a_board_change_keeps_the_tree(self):
        """A run that is cancelled or fails after the board step must not leave the
        next run with no key: that started from scratch (90 minutes on 2026-10-26)."""
        run("record")
        key = OUT / ".soundtap-build-key"
        host, config = key.read_text().split()[:2]
        key.write_text(f"{host} {config} a-different-board\n")
        run("auto")  # this build is then cancelled: record never runs
        second = run("auto")
        self.assertIn("the board configuration changed", second)
        self.assertTrue((OUT / "build/boost-1.87.0/.stamp_built").exists())
        self.assertTrue((OUT / "host/bin/cmake").exists())

    def test_an_unfinished_build_after_a_defconfig_change_keeps_the_host_tree(self):
        run("record")
        key = OUT / ".soundtap-build-key"
        host = key.read_text().split()[0]
        key.write_text(f"{host} a-different-config x\n")
        run("auto")
        self.assertIn("keeping the host tree only", run("auto"))
        self.assertTrue((OUT / "host/bin/cmake").exists())

    def test_a_defconfig_change_keeps_host_package_stamps_and_drops_target_ones(self):
        host = OUT / "build/host-cmake-4.3.2"
        host.mkdir()
        (host / ".stamp_built").touch()
        run("record")
        key = OUT / ".soundtap-build-key"
        key.write_text(f"{key.read_text().split()[0]} a-different-config x\n")
        run("auto")
        self.assertTrue((host / ".stamp_built").exists())
        self.assertFalse((OUT / "build/boost-1.87.0").exists())
        self.assertFalse((OUT / "target").exists())

    def test_a_matching_tree_keeps_compiled_packages(self):
        run("record")
        run("auto")
        # Third-party packages keep everything that took time to produce.
        for stamp in (".stamp_built", ".stamp_configured"):
            self.assertTrue((OUT / "build/boost-1.87.0" / stamp).exists(), stamp)
            self.assertTrue((OUT / "build/linux-6.12" / stamp).exists(), stamp)
        self.assertTrue((OUT / "host/bin/cmake").exists())

    def test_a_matching_tree_rebuilds_the_packages_we_write(self):
        run("record")
        run("auto")
        for package in ("soundtapd-1.0", "soundtap-runtime-1.0"):
            for stamp in (".stamp_rsynced", ".stamp_configured", ".stamp_built"):
                self.assertFalse((OUT / "build" / package / stamp).exists(),
                                 f"{package} kept {stamp}, so a source change would be ignored")

    def test_a_matching_tree_reinstalls_everything_into_a_new_target(self):
        run("record")
        run("auto")
        self.assertFalse((OUT / "target").exists(),
                         "a file a package stopped installing could reach an image")
        for package in ("boost-1.87.0", "linux-6.12", "soundtapd-1.0"):
            self.assertFalse((OUT / "build" / package / ".stamp_target_installed").exists(),
                             f"{package} would not reinstall into the new target tree")

    def test_a_changed_package_definition_rebuilds_that_package(self):
        """2026-10-07: a change added afade to the ffmpeg-tunein .mk and three candidates
        later the speakers still had the old ffmpeg, because .stamp_built survived.
        That package is gone; snapcast stands in for any of ours."""
        pkg = OUT / "build/soundtap-snapcast-0.35.0"
        pkg.mkdir(parents=True)
        (pkg / ".stamp_built").touch()
        run("record")
        run("auto")
        self.assertTrue((pkg / ".stamp_built").exists(), "an unchanged package was rebuilt")
        mk = ROOT / "os/package/soundtap-snapcast/soundtap-snapcast.mk"
        original = mk.read_text()
        try:
            mk.write_text(original + "\n# changed\n")
            self.assertIn("soundtap-snapcast changed", run("auto"))
        finally:
            mk.write_text(original)
        self.assertFalse(pkg.exists(), "the changed package kept its build")
        self.assertTrue((OUT / "build/boost-1.87.0/.stamp_built").exists())

    def test_a_changed_upstream_patch_rebuilds_that_package(self):
        """GLib's CVE fix went in as a patch under os/board/common/patches, and a
        warm libglib2 would have kept its .stamp_built and shipped without it."""
        glib = OUT / "build/libglib2-2.82.5"
        headers = OUT / "build/linux-headers-6.12"
        for directory in (glib, headers):
            directory.mkdir(parents=True)
            (directory / ".stamp_built").touch()
        run("record")
        run("auto")
        self.assertTrue((glib / ".stamp_built").exists(), "an unchanged package was rebuilt")
        patch = ROOT / "os/board/common/patches/libglib2/0002-test.patch"
        try:
            patch.write_text("changed\n")
            self.assertIn("patches/libglib2 changed", run("auto"))
        finally:
            patch.unlink()
        self.assertFalse(glib.exists(), "the patched package kept its build")
        self.assertTrue(headers.exists(), "linux's patches dropped linux-headers")

    def test_the_key_covers_the_defconfig(self):
        run("record")
        first = (OUT / ".soundtap-build-key").read_text()
        defconfig = ROOT / "os/configs/soundtap_defconfig"
        original = defconfig.read_text()
        try:
            defconfig.write_text(original + "\n# BR2_PACKAGE_SOMETHING is not set\n")
            run("record")
            self.assertNotEqual(first, (OUT / ".soundtap-build-key").read_text())
        finally:
            defconfig.write_text(original)


if __name__ == "__main__":
    unittest.main()
