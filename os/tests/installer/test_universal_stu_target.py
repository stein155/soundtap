# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""`make stu` asks for all three models: a missing ST20 kernel stops it.

Beside the universal package it builds the preview package (ADR 0133) from the same inputs.
"""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "os/installer/build-universal-stu.sh"


class UniversalTarget(unittest.TestCase):
    """build-universal-stu.sh and `make stu` build the three-model package and its preview."""

    def test_tri_target_refuses_a_missing_st20_kernel(self) -> None:
        """The tri-model target stops when the ST20 kernel is missing."""
        env = dict(os.environ, SOUNDTAP_STU_TARGET="soundtap-tri",
                   SOUNDTAP_SPOTTY_KERNEL="/nonexistent/uImage-st20")
        done = subprocess.run(["sh", str(SCRIPT)], env=env, capture_output=True, text=True,
                              check=False)
        self.assertEqual(done.returncode, 2, done.stderr)
        self.assertIn("refusing a two-model package", done.stderr)

    def test_make_stu_asks_for_tri(self) -> None:
        """`make stu` runs the builder for all three models, with each model's boot payloads."""
        with tempfile.TemporaryDirectory() as tmp:
            seen = Path(tmp) / "seen"
            stub = Path(tmp) / "sh"
            stub.write_text(f'#!/bin/sh\n{{ echo "$*"; env; }} >"{seen}"\n')
            stub.chmod(0o755)
            env = dict(os.environ, PATH=f"{tmp}:{os.environ['PATH']}")
            subprocess.run(["make", "-s", "stu", "OUTPUT=/out"], cwd=ROOT, env=env,
                           check=True, capture_output=True)
            lines = seen.read_text().splitlines()
        self.assertEqual(lines[0], str(SCRIPT))
        self.assertIn("SOUNDTAP_STU_TARGET=soundtap-tri", lines)
        for name, path in (("RHINO_KERNEL", "uImage-st10"), ("SPOTTY_KERNEL", "uImage-st20"),
                           ("SPOTTY_MLO", "st20-spl/MLO"), ("BINKY_MLO", "wla-spl/MLO"),
                           ("BINKY_UBOOT", "wla-uboot/u-boot.img")):
            self.assertIn(f"SOUNDTAP_{name}=/out/images/{path}", lines)

    def test_the_preview_package_is_the_same_build_with_the_preview_installer(self) -> None:
        """The preview installer goes into <name>-preview.stu only, and both packages are checked.

        The script itself, with the toolchain and Python stubbed: the preview installer
        (WLA_PREVIEW=1) goes into <name>-preview.stu only, both packages are checked, the pair
        is compared, and each gets its own .sha256.
        """
        with tempfile.TemporaryDirectory() as tmp_name:
            tmp = Path(tmp_name)
            log = tmp / "log"
            bin_ = tmp / "bin"
            bin_.mkdir()
            # build.sh run through sh: write the installer it would name, as build.sh names it.
            (bin_ / "sh").write_text(f"""#!/bin/sh
case "$1" in
*/module/build.sh)
	name=wla-installer-full
	[ "${{WLA_PREVIEW:-0}}" = 1 ] && name=$name-preview
	echo "build.sh $name" >>"{log}"
	mkdir -p "$2" && echo "$name" >"$2/$name" ;;
*) exec /bin/sh "$@" ;;
esac
""")
            # Every python3 call is logged; build-stu.py writes its installer's name to --out.
            (bin_ / "python3").write_text(f"""#!/bin/sh
echo "python3 $*" >>"{log}"
case "$*" in *build-stu.py*)
	while [ $# -gt 0 ]; do
		case "$1" in --installer) installer=$2 ;; --out) out=$2 ;; esac
		shift
	done
	cat "$installer" >"$out" ;;
esac
""")
            for stub in bin_.iterdir():
                stub.chmod(0o755)
            inputs = {}
            for name in ("RHINO_KERNEL", "RHINO_ROOTFS", "SPOTTY_KERNEL", "SPOTTY_MLO",
                         "BINKY_MLO", "BINKY_UBOOT"):
                inputs[f"SOUNDTAP_{name}"] = str(tmp / name)
                (tmp / name).write_text(name)
            out = tmp / "dist/Update-soundtap-buildroot.stu"
            env = dict(os.environ, PATH=f"{bin_}:{os.environ['PATH']}", SOUNDTAP_STU_OUT=str(out),
                       SOUNDTAP_STU_TARGET="soundtap-tri", **inputs)
            done = subprocess.run(["/bin/sh", str(SCRIPT)], env=env, capture_output=True,
                                  text=True, check=False)
            self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
            preview = tmp / "dist/Update-soundtap-buildroot-preview.stu"
            self.assertEqual(out.read_text(), "wla-installer-full\n")
            self.assertEqual(preview.read_text(), "wla-installer-full-preview\n")
            calls = log.read_text().splitlines()
            for package in (out, preview):
                self.assertIn(f"python3 {ROOT}/os/support/stu_check.py accept {package} soundtap-tri", calls)
                digest = (tmp / "dist" / (package.name + ".sha256")).read_text().split()
                self.assertEqual(digest[1], package.name)
            self.assertIn(f"python3 {ROOT}/os/support/stu_check.py same-payloads {out} {preview} soundtap-tri",
                          calls)
            self.assertIn(f"python3 -B {ROOT}/os/support/wla_stock_route_dryrun.py --bootloaders --preview "
                          f"--variant binky {preview}", calls)


if __name__ == "__main__":
    unittest.main()
