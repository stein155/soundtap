# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The reproducibility check (ADR 0011): repro_compare.py and repro-check.sh.

repro_compare.py runs on fake image directories; repro-check.sh runs with a stub `make`
on PATH that writes such a directory, the way `make os` and `make stu` would.
"""
import hashlib
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
COMPARE = ROOT / "os/support/repro_compare.py"
CHECK = ROOT / "os/support/repro-check.sh"


def write_images(images: Path, files: dict, tree: dict | None = None) -> None:
    """files: name -> bytes, listed in SHA256SUMS; tree: rootfs-tree path -> bytes."""
    images.mkdir(parents=True, exist_ok=True)
    lines = []
    for name, data in files.items():
        (images / name).parent.mkdir(parents=True, exist_ok=True)
        (images / name).write_bytes(data)
        lines.append(f"{hashlib.sha256(data).hexdigest()}  {name}\n")
    (images / "SHA256SUMS").write_text("".join(lines))
    for rel, data in (tree or {}).items():
        path = images / "rootfs-tree" / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)


def run(*args, env=None):
    return subprocess.run([sys.executable, str(COMPARE), *map(str, args)], capture_output=True,
                          text=True, env=env, check=False)


IMAGES = {"zImage": b"kernel", "rootfs.ubi": b"ubi", "wla-spl/MLO": b"spl"}
TREE = {"usr/bin/soundtapd": b"daemon", "etc/os-release": b"v1"}


class ReproCompare(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(subprocess.run, ["rm", "-rf", str(self.tmp)], check=False)

    def saved(self, name, files=IMAGES, tree=TREE, extra=None):
        images = self.tmp / f"images-{name}"
        write_images(images, files, tree)
        args = ["save", images, self.tmp / name]
        if extra is not None:
            stu = self.tmp / f"stu-{name}" / "Update.stu"
            stu.parent.mkdir()
            stu.write_bytes(extra)
            args += ["--extra", stu]
        r = run(*args)
        self.assertEqual(r.returncode, 0, r.stderr)
        return self.tmp / name

    def test_same_builds_pass(self):
        r = run("compare", self.saved("a", extra=b"stu"), self.saved("b", extra=b"stu"))
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("SHA256SUMS: 3 of 3 files the same", r.stdout)
        self.assertIn("extra.SHA256SUMS: 1 of 1 files the same", r.stdout)
        self.assertIn("reproducible", r.stdout)

    def test_names_each_image_that_differs(self):
        b = self.saved("b", files={**IMAGES, "zImage": b"other kernel", "wla-spl/MLO": b"x"})
        r = run("compare", self.saved("a"), b)
        self.assertEqual(r.returncode, 1)
        self.assertIn("differs: zImage", r.stdout)
        self.assertIn("differs: wla-spl/MLO", r.stdout)
        self.assertNotIn("differs: rootfs.ubi", r.stdout)
        self.assertIn("NOT reproducible", r.stdout)

    def test_rootfs_difference_lists_the_paths(self):
        tree = {**TREE, "usr/bin/soundtapd": b"other daemon", "etc/extra": b""}
        del tree["etc/os-release"]
        b = self.saved("b", files={**IMAGES, "rootfs.ubi": b"other ubi"}, tree=tree)
        r = run("compare", self.saved("a"), b)
        self.assertEqual(r.returncode, 1)
        self.assertIn("differs: rootfs.ubi", r.stdout)
        self.assertIn("rootfs.ubi: 3 paths of the packed tree differ", r.stdout)
        self.assertIn("differs: /usr/bin/soundtapd (size, content)", r.stdout)
        self.assertIn("only in A: /etc/os-release", r.stdout)
        self.assertIn("only in B: /etc/extra", r.stdout)

    def test_same_tree_points_at_the_image_writer(self):
        b = self.saved("b", files={**IMAGES, "rootfs.ubi": b"other ubi"})
        r = run("compare", self.saved("a"), b)
        self.assertEqual(r.returncode, 1)
        self.assertIn("the packed trees are the same, so mkfs.ubifs or ubinize", r.stdout)

    def test_stu_difference_is_named(self):
        r = run("compare", self.saved("a", extra=b"stu"), self.saved("b", extra=b"other"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("differs: Update.stu", r.stdout)

    def test_image_missing_from_one_build(self):
        files = {k: v for k, v in IMAGES.items() if k != "zImage"}
        r = run("compare", self.saved("a"), self.saved("b", files=files))
        self.assertEqual(r.returncode, 1)
        self.assertIn("only in A: zImage", r.stdout)

    def test_save_refuses_images_that_do_not_match_their_sums(self):
        images = self.tmp / "images"
        write_images(images, IMAGES)
        (images / "zImage").write_bytes(b"changed after the build")
        r = run("save", images, self.tmp / "a")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("zImage", r.stderr)
        self.assertFalse((self.tmp / "a").exists())

    def test_manifest_records_type_mode_size_content_and_link(self):
        tree = self.tmp / "tree"
        (tree / "bin").mkdir(parents=True)
        (tree / "bin").chmod(0o750)
        (tree / "bin/tool").write_bytes(b"x")
        (tree / "bin/tool").chmod(0o4755)
        (tree / "bin/alias").symlink_to("tool")
        (tree / "bin" / "hard").hardlink_to(tree / "bin/tool")
        r = run("manifest", tree)
        self.assertEqual(r.returncode, 0, r.stderr)
        digest = hashlib.sha256(b"x").hexdigest()
        lines = [line.split("\t") for line in r.stdout.splitlines()]
        self.assertEqual([path for _, path in lines], ["bin", "bin/alias", "bin/hard", "bin/tool"])
        attrs = {path: a.split(" ") for a, path in lines}
        self.assertEqual(attrs["bin"], ["d", "0750", "-", "-"])
        # A link's own mode is the platform's (0777 on Linux); its target is what counts.
        self.assertEqual((attrs["bin/alias"][0], attrs["bin/alias"][3]), ("l", "tool"))
        self.assertEqual(attrs["bin/tool"], ["f", "4755", "1", digest])
        self.assertEqual(attrs["bin/hard"], attrs["bin/tool"])

    def test_mode_change_alone_is_a_difference(self):
        a, b = self.saved("a"), self.saved("b", files={**IMAGES, "rootfs.ubi": b"other ubi"})
        manifest = (b / "rootfs-tree.manifest").read_text().replace("f 0644", "f 0755", 1)
        (b / "rootfs-tree.manifest").write_text(manifest)
        r = run("compare", a, b)
        self.assertRegex(r.stdout, r"differs: /\S+ \(mode\)")


class ReproCheckScript(unittest.TestCase):
    """repro-check.sh builds twice from an empty output/os and compares."""

    def check(self, differ=False, env_extra=None, ccache=True):
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(subprocess.run, ["rm", "-rf", str(tmp)], check=False)
        repo = tmp / "repo"
        (repo / "os/support").mkdir(parents=True)
        for script in (COMPARE, CHECK):
            (repo / "os/support" / script.name).write_bytes(script.read_bytes())
        if ccache:
            (repo / ".cache/buildroot-ccache").mkdir(parents=True)
            (repo / ".cache/buildroot-ccache/obj").write_text("warm")
        stub = tmp / "bin"
        stub.mkdir()
        # `make -C REPO os VAR=...` writes images and a tree, `make -C REPO stu` the package.
        # Each os build records what it was given and whether it started clean.
        (stub / "make").write_text(f"""#!/bin/sh
repo=$2 target=$3; shift 3
log={tmp}/calls
if [ "$target" = os ]; then
	n=$(($(grep -c '^os ' "$log" 2>/dev/null || echo 0) + 1))
	echo "os $* clean=$(test -e "$repo/output/os" && echo no || echo yes) ccache=$(cat "$repo/.cache/buildroot-ccache/obj" 2>/dev/null || echo none)" >>"$log"
	images=$repo/output/os/images
	mkdir -p "$images/rootfs-tree/etc"
	printf kernel >"$images/zImage"
	value=same; [ "{int(differ)}" = 1 ] && value=$n
	printf "$value" >"$images/rootfs-tree/etc/build"
	printf "ubi-$value" >"$images/rootfs.ubi"
	(cd "$images" && {sys.executable} -c 'import hashlib,sys
for n in sys.argv[1:]: print(hashlib.sha256(open(n,"rb").read()).hexdigest()+"  "+n)' zImage rootfs.ubi >SHA256SUMS)
	mkdir -p "$repo/.cache/buildroot-ccache" && echo cold >"$repo/.cache/buildroot-ccache/obj.new"
else
	echo "$target skip_dry_run=$SOUNDTAP_SKIP_DRY_RUN" >>"$log"
	mkdir -p "$repo/dist" && printf package >"$repo/dist/Update-soundtap-buildroot.stu"
fi
""")
        (stub / "make").chmod(0o755)
        env = dict(os.environ, PATH=f"{stub}:{os.environ['PATH']}", SOURCE_DATE_EPOCH="1789766294",
                   SOUNDTAP_COMMIT="c0ffee")
        for name in ("REPRO_DIR", "REPRO_STU", "REPRO_FRESH_CCACHE"):
            env.pop(name, None)
        env.update(env_extra or {})
        r = subprocess.run(["sh", str(repo / "os/support/repro-check.sh"), str(repo)],
                           capture_output=True, text=True, env=env, check=False)
        calls = (tmp / "calls").read_text().splitlines() if (tmp / "calls").exists() else []
        return r, calls, repo

    def test_two_clean_builds_with_the_same_ids(self):
        r, calls, repo = self.check()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(calls, [
            "os SOURCE_DATE_EPOCH=1789766294 SOUNDTAP_COMMIT=c0ffee clean=yes ccache=warm",
            "stu skip_dry_run=1",
            "os SOURCE_DATE_EPOCH=1789766294 SOUNDTAP_COMMIT=c0ffee clean=yes ccache=warm",
            "stu skip_dry_run=1",
        ])
        self.assertIn("reproducible", r.stdout)
        self.assertTrue((repo / "output/repro/a/rootfs-tree.manifest").is_file())
        self.assertTrue((repo / "output/repro/b/Update-soundtap-buildroot.stu").is_file())

    def test_a_difference_fails_and_names_the_path(self):
        r, _, _ = self.check(differ=True)
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("differs: rootfs.ubi", r.stdout)
        self.assertIn("differs: /etc/build (content)", r.stdout)

    def test_fresh_ccache_for_the_second_build_and_put_back(self):
        r, calls, repo = self.check(env_extra={"REPRO_FRESH_CCACHE": "1"})
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertTrue(calls[0].endswith("ccache=warm"))
        self.assertTrue(calls[2].endswith("ccache=none"))
        cache = repo / ".cache/buildroot-ccache"
        self.assertEqual((cache / "obj").read_text(), "warm")
        self.assertFalse((repo / ".cache/buildroot-ccache.repro-kept").exists())

    def test_images_only(self):
        r, calls, repo = self.check(env_extra={"REPRO_STU": "0"})
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual([c.split()[0] for c in calls], ["os", "os"])
        self.assertFalse((repo / "output/repro/a/extra.SHA256SUMS").exists())

    def test_refuses_without_the_build_ids(self):
        r, calls, _ = self.check(env_extra={"SOURCE_DATE_EPOCH": ""})
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
