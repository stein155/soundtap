# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every copy of the model table agrees with os/models.json (ADR 0008).

The facts that tell the speakers apart -- which model a variant runs as, the STU modules and
boot chain a model takes, the device tree and kernel its slot boots, its codec script, its
rootfs ceiling -- live in languages that cannot share a file: the init script runs in BusyBox
before anything else, installer.c on Bose's 3.14 kernel, U-Boot before Linux, soundtapd from a
build that sees app/ alone. soundtapd's copy is generated (os/support/models_gen.py); the rest
are held to the table here, each by running it wherever that can be done offline:

- soundtapd: the generated table is current, and with Go on the host its tests that hold the
  profiles, the install path, the boot recipes and the rootfs ceilings to it run.
- the init script: detect_model and copy_codec_script, sourced and run per variant.
- installer.c: built for this host and asked (--board, --print-env, --which-module,
  --kernel-fits) what it would do on each variant.
- U-Boot: rt_board_setup, compiled out of the patch by test_uboot_usb_recovery's probe, for
  each model's manufacturing name and variant_raw; and the file names its recovery and default
  bootcmd build from rt_v and rt_m.
- the image: both post-build scripts run against a fake tree, and /boot is what the table says.
- the STU builder and checker, the env tool, the ROM-USB tool and the release check: imported,
  their tables compared.

Text is read only where nothing runs offline: the device trees' bootargs (data), gadget-console's
variant list (it mounts proc, sysfs and configfs before it reads anything), the kernel names
build-stu.py passes to require_kernel_model, and post-image.sh's per-model kernel loop.
"""
import importlib.util
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import boot.test_uboot_usb_recovery as uboot_tests
import installer.test_wla_installer_offline as installer_tests
from boot.init_script import InitScript

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "os/support"))
sys.path.insert(0, str(ROOT / "tools/release"))
import models_gen  # noqa: E402
import stu_check  # noqa: E402

TABLE = models_gen.load()
MODELS, VARIANTS = TABLE["models"], TABLE["variants"]
BOOTED = {name: v for name, v in VARIANTS.items() if not v.get("stock_only")}
SUFFIXES = models_gen.suffixes(TABLE)
DTS = ROOT / "os/board/common/dts/ti/omap"


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module  # a dataclass looks its module up there
    spec.loader.exec_module(module)
    return module


def own_variant(model):
    return VARIANTS[MODELS[model]["variant"]]


def package_modules():
    """The suffixed modules a universal package carries, as the table derives them."""
    names = set()
    for m in MODELS.values():
        names |= {f"soundtap-kernel.{m['module_suffix']}", f"soundtap-spl.{m['spl']}",
                  f"soundtap-uboot.{m['uboot']}"}
    return names


class Soundtapd(unittest.TestCase):
    def test_generated_go_table_is_current(self):
        self.assertEqual(models_gen.GO_FILE.read_text(), models_gen.go_source(TABLE),
                         "run `go generate ./internal/modeltable` in app/")

    @unittest.skipUnless(shutil.which("go"), "needs Go")
    def test_go_code_follows_the_table(self):
        done = subprocess.run(
            ["go", "test", "-count=1", "-run", "^(TestModelTable|TestGeneratedTableIsModelsJSON)$",
             "./internal/hw/model", "./internal/modeltable"],
            cwd=ROOT / "app", capture_output=True, text=True, timeout=600,
            env=dict(os.environ, GOFLAGS="-mod=vendor", GOTOOLCHAIN="local"))
        if done.returncode and "requires go >=" in done.stderr + done.stdout:
            self.skipTest("the host's Go is older than app/go.mod")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)


class InitScriptTable(unittest.TestCase):
    def identity(self, cmdline, variant=None):
        """MODEL, NOCODEC and the codec script the first boot copies, as the init script decides them."""
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            (init.proc / "cmdline").write_text(cmdline + "\n")
            if variant is not None:
                (init.proc / "variant").write_text(variant + "\n")
            # copy_codec_script reads /proc/cmdline itself: point that one path at the fake.
            grep = shutil.which("grep")
            init.stub("grep", f'for a do shift; [ "$a" = /proc/cmdline ] && a="{init.proc}/cmdline"; '
                              f'set -- "$@" "$a"; done\nexec "{grep}" "$@"')
            done = init.run("detect_model >/dev/null\n"
                            'codec_script_from_other_slot() { CODEC=$1; }\nCODEC=-\ncopy_codec_script\n'
                            'echo "$MODEL $NOCODEC $CODEC"', check=False)
            return done.returncode, done.stdout.strip()

    def test_each_variant_runs_as_its_model(self):
        for name, v in VARIANTS.items():
            codec = v["codec_script"] or "-"
            # A board without a codec script has no codec: the WLA, the ST300, the Lifestyle
            # module and the SM2 adapter (lisa2), whatever its model's own variant has.
            nocodec = "0" if v["codec_script"] else "1"
            want = (0, f"{v['model']} {nocodec} {codec}")
            with self.subTest(variant=name):
                self.assertEqual(self.identity(f"variant={name} soundtap.nandroot=1"), want)
                # Stock's /proc/variant alone. The codec script follows the command line,
                # which every kernel we boot carries variant= on, so only the model counts.
                code, out = self.identity("root=ubi0:rootfs", variant=name)
                self.assertEqual((code, out.split()[:2]), (0, [v["model"], nocodec]))

    def test_a_variant_the_table_does_not_name_stops_the_boot(self):
        for name in ("st10", "spotty4", "Rhino", "ginger2"):
            with self.subTest(variant=name):
                self.assertEqual(self.identity(f"variant={name}")[0], 1)

    def test_gadget_console_comes_up_on_every_board_we_boot(self):
        # Text: the script mounts proc, sysfs and configfs before it reads the command line.
        script = (ROOT / "os/overlay/etc/init.d/gadget-console").read_text()
        accepted = re.search(r"^([a-z0-9|]+)\) ;;$", script, re.M).group(1).split("|")
        self.assertEqual(sorted(accepted), sorted(BOOTED))


class InstallerTable(unittest.TestCase):
    """installer.c, module 0 of every package, built for this host."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = installer_tests.scratch_dir()
        cls.binary = installer_tests.build_installer(cls.tmp.name, nand=True, bootloaders=True,
                                                     port=installer_tests.free_port())

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def ask(self, variant, *args, success=True):
        out = subprocess.run([str(self.binary), *args], capture_output=True, text=True,
                             env={**{k: v for k, v in os.environ.items() if k != "SOUNDTAP_DT_COMPATIBLE"},
                                  "SOUNDTAP_VARIANT": variant})  # no device tree: the module is not judged
        self.assertEqual(out.returncode == 0, success, out.stderr)
        return out.stdout.strip()

    def test_it_installs_on_every_board_we_boot_and_on_no_other(self):
        for name, v in VARIANTS.items():
            with self.subTest(variant=name):
                known = self.ask(name, "--board").split()[1]
                self.assertEqual(known, "unknown" if v.get("stock_only") else "known")

    def test_recipes_boot_the_tables_tree_and_kernel(self):
        for name, v in BOOTED.items():
            with self.subTest(variant=name):
                env = dict(line.split("=", 1) for line in self.ask(name, "--print-env").splitlines())
                self.assertEqual(re.findall(r"ubifsload 0x8[23]000000 /boot/(\S+);", env["slot_boot"]),
                                 [v["dtb"], v["kernel"]])
                self.assertIn(f" variant={name} module_type={v['module_type']} ", env["soundtap_args"])
                self.assertIn(f" variant={name} ", env["stock_args"])

    def test_it_takes_its_models_modules_and_no_others(self):
        for name, v in BOOTED.items():
            m = MODELS[v["model"]]
            mine = {f"soundtap-kernel.{m['module_suffix']}", f"soundtap-spl.{m['spl']}",
                    f"soundtap-uboot.{m['uboot']}"}
            for module in sorted(package_modules()):
                with self.subTest(variant=name, module=module):
                    want = module.rsplit(".", 1)[0] if module in mine else "(another model)"
                    self.assertEqual(self.ask(name, "--which-module", module), want)

    def test_it_takes_its_models_kernel_and_no_others(self):
        names = {m["kernel_name"] for m in MODELS.values()}
        for name, v in BOOTED.items():
            for kernel in sorted(names):
                with self.subTest(variant=name, kernel=kernel):
                    want = "yes" if kernel == MODELS[v["model"]]["kernel_name"] else "no"
                    self.assertEqual(self.ask(name, "--kernel-fits", kernel), want)


def unq(s):
    """A C string literal's text, None for NULL."""
    return None if s == "NULL" else s.strip('"')


@unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
class UBootTable(unittest.TestCase):
    """Our U-Boot, which tells the board apart before any environment says what it is."""

    def probe(self, model, variant_raw, mfg):
        r = uboot_tests.UsbRecoveryProbe().run_probe(model, False, variant_raw, mfg=mfg)
        return r["rt_v"], r["env"].get("rt_m")

    def test_it_takes_each_board_for_its_model(self):
        for name, m in MODELS.items():
            want = (m["variant"], own_variant(name)["module_type"])
            for raw in m["variant_raw"] or [""]:
                with self.subTest(model=name, variant_raw=raw):
                    self.assertEqual(self.probe(name, raw, m["mfg_product"] or ""), want)
                    # Without the manufacturing name, variant_raw alone has to say it.
                    if m["variant_raw"]:
                        self.assertEqual(self.probe(name, raw, ""), want)

    def test_the_names_it_builds_are_the_tables(self):
        # The default bootcmd, compiled into the SPL's environment as well, loads
        # /boot/${rt_v}_${rt_m}.dtb and /boot/uImage-${rt_v}: every row's stem is a model's own
        # variant, whose files have exactly those names. rt_set_recipes writes the board's own
        # files (rt_dtb, rt_kernel) and its variant (rt_b).
        recovery = uboot_tests.patch_function(uboot_tests.PATCH, "static int rt_set_recipes(void)")
        self.assertIn('env_get("rt_dtb")', recovery)
        self.assertIn('env_get("rt_kernel")', recovery)
        self.assertIn('env_get("rt_b")', recovery)
        bootcmd = (ROOT / "os/bootloader/build-wla-uboot.sh").read_text()
        self.assertIn("/boot/${rt_v}_${rt_m}.dtb", bootcmd)
        self.assertIn("/boot/uImage-${rt_v};", bootcmd)
        for name, m in MODELS.items():
            v = own_variant(name)
            with self.subTest(model=name):
                self.assertEqual(v["dtb"], f"{m['variant']}_{v['module_type']}.dtb")
                self.assertEqual(v["kernel"], f"uImage-{m['variant']}")
        own = {m["variant"] for m in MODELS.values()}
        for v, row in self.board_rows().items():
            with self.subTest(board=v):
                self.assertIn(row[0], own)
                self.assertEqual(VARIANTS[row[0]]["module_type"], VARIANTS[v]["module_type"])

    def board_rows(self):
        """rt_boards as the patch has it: {variant: (stem, module, mfg, lo, hi, dtb, kernel)}."""
        block = re.search(r"^\+static const struct rt_board rt_boards\[\] = \{(.*?)^\+\};",
                          uboot_tests.SCM_PATCH, re.M | re.S).group(1)
        rows = {}
        for row in re.findall(r"\{ (.*?) \},", block):
            v, stem, m, mfg, lo, hi, dtb, kernel = [f.strip() for f in row.split(",")]
            rows[unq(v)] = (unq(stem), unq(m), unq(mfg), int(lo), int(hi), unq(dtb), unq(kernel))
        return rows

    def test_its_board_rows_are_the_tables(self):
        want = {}
        for name, v in VARIANTS.items():
            d = v["detect"]
            if d is None:
                continue
            lo, hi = d.get("variant_raw", [-1, -1])
            want[name] = (v["module_type"], d.get("mfg"), lo, hi, v["dtb"], v["kernel"])
        self.assertEqual({k: r[1:] for k, r in self.board_rows().items()}, want)
        # The stem is the model's own variant: the board runs as that model.
        for name, row in self.board_rows().items():
            self.assertEqual(row[0], MODELS[VARIANTS[name]["model"]]["variant"], name)

    def test_it_takes_each_detectable_board_for_its_variant(self):
        for name, v in VARIANTS.items():
            d = v["detect"]
            if d is None:
                continue
            board = "st20" if v["module_type"] == "scm" else "st10"
            if "variant_raw" in d:
                lo, hi = d["variant_raw"]
                cases = [(str(lo), ""), (str(hi), ""), (str((lo + hi) // 2), "")]
            else:
                cases = [("", d["mfg"])]
            for raw, mfg in cases:
                with self.subTest(variant=name, variant_raw=raw, mfg=mfg):
                    r = uboot_tests.UsbRecoveryProbe().run_probe(board, False, raw, mfg=mfg)
                    self.assertEqual(r["env"].get("rt_b"), name)
                    self.assertEqual(r["rt_v"], MODELS[v["model"]]["variant"])
                    self.assertEqual(r["env"].get("rt_m"), v["module_type"])
                    self.assertEqual(r["env"].get("rt_dtb"), v["dtb"])
                    self.assertEqual(r["env"].get("rt_kernel"), v["kernel"])

    def test_an_scm_count_no_row_knows_stays_an_st20(self):
        # Below 100 (stock's "mfg"), above taigan (stock's "sm2") and no sample: the ST20's files.
        for raw in ("", "50", "4000"):
            with self.subTest(variant_raw=raw):
                r = uboot_tests.UsbRecoveryProbe().run_probe("st20", False, raw, mfg="")
                self.assertEqual((r["rt_v"], r["env"].get("rt_dtb")), ("spotty", "spotty_scm.dtb"))


class ImageTable(unittest.TestCase):
    """/boot as the image build leaves it: both post-build scripts, in the order the defconfig
    runs them, against a fake tree with stub tools."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        tmp = Path(cls.tmp.name)
        target, binaries, host, bindir = tmp / "target", tmp / "images", tmp / "host", tmp / "bin"
        kver = "6.12.0"
        dtc = tmp / f"build/linux-{kver}/scripts/dtc/dtc"
        for d in (target / "boot", target / "etc", target / "var/lib", target / "usr/lib",
                  target / "lib/firmware/ti-connectivity", binaries / "bco-app/link-own",
                  host / "bin", bindir, dtc.parent):
            d.mkdir(parents=True)
        (target / "usr/lib/os-release").write_text("VERSION_ID=2026.02\n")
        (target / "lib/firmware/ti-connectivity/wl18xx-fw-4.bin").write_text("fw")
        (target / "lib/firmware/ti-connectivity/wl18xx-conf.bin").write_text("conf")
        (binaries / "zImage").write_text("zImage")
        (binaries / "bco-app/link-own/app.bcd").write_text("bcd")
        cls.trees = sorted({v["dts"] for v in BOOTED.values()})
        for tree in cls.trees:
            (binaries / f"{tree}.dtb").write_text(tree)
        config = tmp / ".config"
        config.write_text(f'BR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE="{kver}"\nBR2_PACKAGE_DROPBEAR=y\n')

        def tool(path, body):
            path.write_text("#!/bin/sh\n" + body + "\n")
            path.chmod(0o755)

        # mkimage writes the name it stamps, dtc copies, sha256sum answers what the scripts
        # want: the digests are of real firmware, which a test tree does not hold.
        tool(host / "bin/mkimage", 'while [ $# -gt 2 ]; do [ "$1" = -n ] && name=$2; shift; done\n'
                                   'printf "%s" "$name" >"$2"')
        tool(dtc, 'while [ $# -gt 2 ]; do [ "$1" = -o ] && out=$2; shift; done\ncp "$2" "$out"')
        tool(bindir / "sha256sum", 'case $1 in\n'
             '*TIInit_11.8.32.bts) echo "f7dfa9d8072eacf492414fabd35fb6287b18eb0c40bad8022d4aa0802dedfb78  $1" ;;\n'
             '*) echo "95f04731257febabc358b95ff06dbb2025d047c6ad3ae0239c8afb92dedc7c5c  $1" ;;\nesac')
        # GNU install's -D and -m, which macOS lacks.
        tool(bindir / "install", 'mode=0644; dir=0\nwhile :; do case $1 in -D) shift ;; -d) dir=1; shift ;;\n'
             '-m) mode=$2; shift 2 ;; *) break ;; esac; done\n'
             'if [ $dir = 1 ]; then mkdir -p "$@"; exit; fi\n'
             'mkdir -p "$(dirname "$2")" && cp "$1" "$2" && chmod "$mode" "$2"')
        defconfig = (ROOT / "os/configs/soundtap_defconfig").read_text()
        scripts = re.search(r'^BR2_ROOTFS_POST_BUILD_SCRIPT="(.*)"$', defconfig, re.M).group(1)
        env = dict(os.environ, PATH=f"{bindir}:{os.environ['PATH']}", BINARIES_DIR=str(binaries),
                   HOST_DIR=str(host), BUILD_DIR=str(tmp / "build"), BR2_CONFIG=str(config),
                   SOURCE_DATE_EPOCH="1767225600", SOUNDTAP_COMMIT="0" * 40,
                   BR2_EXTERNAL_SOUNDTAP_PATH=str(ROOT / "os"))
        for script in scripts.split():
            path = script.replace("$(BR2_EXTERNAL_SOUNDTAP_PATH)", str(ROOT / "os"))
            done = subprocess.run(["sh", path, str(target)], env=env, capture_output=True, text=True)
            if done.returncode:
                raise AssertionError(f"{path} failed:\n{done.stdout}{done.stderr}")
        cls.boot = target / "boot"

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def test_each_variant_finds_its_tree_and_kernel(self):
        for name, v in BOOTED.items():
            with self.subTest(variant=name):
                self.assertEqual((self.boot / v["dtb"]).read_text(), v["dts"])
                self.assertEqual((self.boot / v["kernel"]).read_text(),
                                 f"Linux-6.12 SoundTap {MODELS[v['model']]['kernel_name']}")

    def test_trees_boot_as_their_variant(self):
        # Text: a device tree is data, and these bootargs are what a stick or RAM boot passes.
        for name, v in BOOTED.items():
            dts = (DTS / f"{v['dts']}.dts").read_text()
            args = re.search(r'bootargs = "([^"]*)"', dts).group(1)
            variant = re.search(r"\bvariant=(\S+)", args).group(1)
            with self.subTest(variant=name, tree=v["dts"]):
                self.assertEqual(VARIANTS[variant]["dts"], v["dts"])
                self.assertIn(f" module_type={v['module_type']} ", args)

    def test_post_image_names_each_kernel(self):
        # Text: post-image.sh needs a whole image build to run.
        post_image = (ROOT / "os/board/image/post-image.sh").read_text()
        loop = re.search(r"^for model in ([a-z0-9 ]+); do$", post_image, re.M).group(1).split()
        self.assertEqual(sorted(loop), sorted({m["kernel_name"] for m in MODELS.values()}))
        for kind in ("spl", "uboot"):
            for path in TABLE["boot_images"][kind].values():
                self.assertIn(f" {path}", post_image, f"SHA256SUMS lacks {path}")


class PackageTable(unittest.TestCase):
    """The STU builder and checker, and the tools that name models."""

    build_stu = load("build_stu", "os/installer/build-stu.py")

    def test_rootfs_ceilings(self):
        for name, m in MODELS.items():
            with self.subTest(model=name):
                for where, limits in (("build-stu.py", self.build_stu.MODEL_ROOTFS_LIMITS),
                                      ("stu_check.py", stu_check.MODEL_ROOTFS_LIMITS)):
                    self.assertEqual(limits[m["module_suffix"]], m["stock_rootfs_limit"], where)
                self.assertEqual(stu_check.PROFILES[name][1], m["stock_rootfs_limit"], "stu_check.py")
                if name in self.build_stu.PROFILES:
                    self.assertEqual(self.build_stu.PROFILES[name]["stock_ubi_module_size"],
                                     m["stock_rootfs_limit"], "build-stu.py")
        self.assertEqual(sorted(self.build_stu.MODEL_ROOTFS_LIMITS), sorted(stu_check.MODEL_ROOTFS_LIMITS))

    def test_universal_package_carries_every_models_modules(self):
        for where, modules in (("build-stu.py", self.build_stu.SOUNDTAP_TRI_MODULES),
                               ("stu_check.py", stu_check.SOUNDTAP_TRI_MODULES)):
            self.assertEqual({n for n in modules if "." in n}, package_modules(), where)

    def test_suffixes(self):
        for where, base in (("build-stu.py", self.build_stu.module_base_name),
                            ("stu_check.py", stu_check.module_base_name)):
            for suffix in SUFFIXES:
                self.assertEqual(base(f"soundtap-kernel.{suffix}"), "soundtap-kernel", where)
            for other in sorted(set(VARIANTS) - set(SUFFIXES)):
                self.assertEqual(base(f"soundtap-kernel.{other}"), f"soundtap-kernel.{other}", where)

    def test_builder_holds_each_kernel_to_its_model(self):
        # Text: build_tri_model_stu needs a built installer, kernel, rootfs, SPL and U-Boot.
        source = (ROOT / "os/installer/build-stu.py").read_text()
        calls = dict(re.findall(r'require_kernel_model\(args\.(\w+)_kernel, "(\w+)"\)', source))
        self.assertTrue(calls)
        for suffix, kernel in calls.items():
            self.assertEqual({m["kernel_name"] for m in MODELS.values() if m["module_suffix"] == suffix}, {kernel})

    def test_env_tool_recipes(self):
        tool = load("wla_env", "os/support/uboot_recipes.py")
        for variant, files in tool.MODELS.items():
            v = BOOTED[variant.decode()]
            with self.subTest(variant=variant.decode()):
                self.assertEqual({k.decode(): files[k].decode() for k in files},
                                 {"dtb": v["dtb"], "kernel": v["kernel"], "module_type": v["module_type"]})

    def test_rom_usb_restore(self):
        tool = load("rom_usb_restore", "tools/recovery/rom-usb-restore.py")
        self.assertEqual(sorted(tool.MODELS), sorted(MODELS))
        for name, m in MODELS.items():
            entry = tool.MODELS[name]
            with self.subTest(model=name):
                self.assertEqual((entry.variant, entry.title, entry.spl),
                                 (m["variant"], own_variant(name)["product"], m["spl"]))
                self.assertEqual(tool.IMAGE_PATHS[m["spl"]], TABLE["boot_images"]["spl"][m["spl"]])
                self.assertEqual(tool.IMAGE_PATHS["uboot"], TABLE["boot_images"]["uboot"][m["uboot"]])

    def test_release_approval(self):
        import release_approval
        self.assertEqual(sorted(release_approval.MODELS), sorted(MODELS))
        for name in MODELS:
            v = own_variant(name)
            self.assertEqual(release_approval.MODELS[name]["device"],
                             {"model": v["product"], "variant": MODELS[name]["variant"],
                              "module_type": v["module_type"]}, name)


if __name__ == "__main__":
    unittest.main()
