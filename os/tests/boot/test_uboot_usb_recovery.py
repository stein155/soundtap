# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Hold the U-Boot USB emergency update to the sources it copies its numbers from, and run it.

os/bootloader/uboot/uboot-2026.07-usb-recovery.patch is C inside U-Boot and cannot import
anything from this tree, so every number in it is a copy: the .stu layout and Bose's module
chains from app/internal/stu, the key indices from soundtapd's defaultKeymap, the keypad GPIOs
from app/internal/hw/keypad.go, the slot offsets from the partition table, and the way back to
stock (its recipes, its boot chain order) from app/internal/flash and app/internal/install. A
copy that drifts installs to the wrong place or never fires, and nobody holds those buttons
often enough to notice. The probes compile the patch's own functions on the host.
"""
import hashlib
import importlib.util
import re
import shutil
import subprocess
import tempfile
import unittest
import zlib
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import ClassVar, TypedDict, Unpack

ROOT = Path(__file__).resolve().parents[3]
PATCH = (ROOT / "os/bootloader/uboot/uboot-2026.07-usb-recovery.patch").read_text()
STU = (ROOT / "app/internal/stu/stu.go").read_text()
STU_PROFILES = (ROOT / "app/internal/stu/profiles.go").read_text()
BUTTONS = (ROOT / "app/internal/hw/keymap.go").read_text()
KEYPAD = (ROOT / "app/internal/hw/keypad.go").read_text()
ENV = (ROOT / "os/support/uboot_recipes.py").read_text()
_spec = importlib.util.spec_from_file_location("wla_env", ROOT / "os/support/uboot_recipes.py")
WLA_ENV = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(WLA_ENV)
BOOTSELECT = (ROOT / "app/internal/flash/bootselect.go").read_text()
FLASH = (ROOT / "app/internal/flash/flash.go").read_text()
WLA_DTS = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-wla.dts").read_text()
UBOOT_CONFIG = (ROOT / "os/package/soundtap-bootloaders/configs/wla-uboot.config").read_text()
NAND_PARTITIONS = 13


def define(name: str) -> int:
    """Return the value of the patch's #define name."""
    m = re.search(rf"^\+#define {name}\s+(\S+)", PATCH, re.MULTILINE)
    if not m:
        msg = f"{name} missing from the patch"
        raise AssertionError(msg)
    return int(m.group(1), 0)


def go_const(name: str) -> int:
    """Return the hex value of a constant in stu.go."""
    return int(re.search(rf"^\s+{name}\s+= (0x[0-9a-f]+)", STU, re.MULTILINE).group(1), 16)


def go_list(name: str) -> list[str]:
    """Return the elements of profiles.go's []string ``name``, in its var block or alone."""
    match = re.search(rf"^\s*(?:var\s+)?{name}\s*=\s*\[\]string\{{(.*?)^\s*\}}", STU_PROFILES,
                      re.MULTILINE | re.DOTALL)
    if not match:
        msg = f"profiles.go no longer declares {name}"
        raise SystemExit(msg)
    return go_strings(match.group(1))


def go_strings(body: str) -> list[str]:
    """Return a Go []string literal's elements, with profiles.go's named constants resolved."""
    consts = dict(re.findall(r'^\s+(mod\w+)\s+= "([^"]*)"', STU_PROFILES, re.MULTILINE))
    return [consts[tok] if tok in consts else tok.strip('"')
            for tok in (t.strip() for t in re.findall(r'"[^"]*"|\w+', body))]


def c_strings(block: str) -> list[str]:
    """Return a C string array's elements from patch lines, adjacent literals joined."""
    text = "\n".join(line.removeprefix("+") for line in block.split("\n"))
    out, cur = [], None
    for tok in re.findall(r'"(?:[^"\\]|\\.)*"|,', text):
        if tok == ",":
            if cur is not None:
                out.append(cur)
            cur = None
        else:
            cur = (cur or "") + tok[1:-1]
    if cur is not None:
        out.append(cur)
    return out


# Bose's module chains, as profiles.go has three of them and Bose's 27.0.6 packages the rest
# (recorded from the packages' headers: names only, no bytes).
_SM2 = ["SoftwareUpdateInstaller", "ARMUpdate", "UbootUpdate"]
_SCM = ["SoftwareUpdateInstaller", "ARMUpdate", "CC2540_Update", "SMSC_Update", "UbootUpdate"]
_TAIL = ["linuxPatched", "ubi.img", "MLO", "u-boot.img"]
_MID = ["linuxPatched", "ubi.img", "SCMBLC_CC2540_6.0.18.738.bin", "Bootloader_CE2_Release_8MB_3.x-3_8_0-942"]
BOSE_CHAINS = {
    "bose-sm2": go_list("Expected"),
    "bose-st20": go_list("ExpectedST20"),
    "bose-st300": go_list("ExpectedST300"),
    "bose-burns": [*_SM2, "STM32SoftwareUpdate", "SCMReset_Update", *_TAIL, "STM32_Burns_v1.0.2.182.hex"],
    "bose-nelson-sm2": [*_SM2, "ABL_Update", *_TAIL, "SEN_FW_update.bos"],
    "bose-triode-sm2": [*_SM2, "ABL_Update", *_TAIL, "TRI_shelby_update.tri"],
    "bose-marconi-sm2": [*_SM2, "MarconiUpdate", *_TAIL, "DEF_CON_02.21.00.r13184.bos"],
    "bose-bardeen": [*_SM2, "ASOCUpdate", "STP_Update", *_TAIL, "bardeen_1.16.7.5043495.bos"],
    "bose-wst-scm": [*_SCM, "ABL_Update", *_MID, "App_BD_DMP 3.x-3_8_0-9423_ER.bcd", "MLO", "u-boot.img", "SEN_FW_update.bos"],
    "bose-triode-scm": [*_SCM, "ABL_Update", *_MID, "App_BD_DMP 3.x-3_8_0-9423_ER_NoAP.bcd", "MLO", "u-boot.img", "TRI_shelby_update.tri"],
    "bose-marconi-scm": [*_SCM, "MarconiUpdate", *_MID, "App_BD_DMP 3.x-3_8_0-9423_ER_NoAP.bcd", "MLO", "u-boot.img", "DEF_CON_02.21.00.r13184.bos"],
}
# The variant each board's stock ubi.img tag names it by (@<module>/<variants>): the Series III
# under their own names, the sm2 adapter as lisa, the ST10 in the package it shares with the
# Wireless Link (@sm2/spotty+mojo+binky).
STOCK_NAMES = {"rhino": "binky", "spotty3": "spotty", "mojo3": "mojo", "lisa2": "lisa", "spotty": "spotty",
               "mojo": "mojo", "taigan": "taigan", "lisa": "lisa", "burns": "burns", "binky": "binky",
               "ginger": "ginger", "bardeen": "bardeen"}


def c_product(expr: str) -> int:
    """Return the value of a C product of numbers and patch macros, such as `2 * RT_SPL_SIZE`."""
    value = 1
    for factor in (f.strip() for f in expr.split("*")):
        value *= int(factor, 0) if factor[0].isdigit() else define(factor)
    return value


def patch_bose_chains() -> list[list[str]]:
    """Return rt_bose_chains as the compiler sees it: the macros expanded, by the host's cc -E."""
    block = re.search(r"^\+static const char \*const rt_bose_chains\[\]\[RT_STU_MODULES\] = \{.*?^\+\};", PATCH, re.MULTILINE | re.DOTALL).group(0)
    src = patch_defines() + "\n" + "\n".join(line[1:] for line in block.split("\n"))
    out = subprocess.run(["cc", "-E", "-P", "-"], input=src, capture_output=True, text=True, check=True).stdout
    body = out[out.index("= {") + 3:]
    return [c_strings(row) for row in re.findall(r"\{([^{}]*)\}", body)]


class UsbRecoveryContract(unittest.TestCase):
    """The U-Boot patch's copied constants match their sources.

    Constants the U-Boot patch shares with soundtapd, the device trees, the recovery tools and
    the build scripts. Read as text: each pair is two spellings of one value in two languages,
    and only a cross build of U-Boot would run the patch's side; the probes below run its code.
    """

    def test_stu_layout(self) -> None:
        """The .stu header offsets and module limit match internal/stu."""
        self.assertEqual(define("RT_STU_HEADER"), go_const("headerSize"))
        self.assertEqual(define("RT_STU_OVERLAP"), go_const("overlap"))
        # With no signature the stick checks every CRC the scanner does: the package size and
        # its CRC in the first header, each header's CRC and each module's image CRC.
        for off, name in ((0x98, "sizeOff"), (0x134, "headerCRCOff"), (0x12c, "imageCRCOff"),
                          (0x08, "pkgSizeOff"), (0x10, "pkgCRCOff"), (0x18, "nameOff"), (0x28, "nameLen")):
            self.assertEqual(go_const(name), off, name)
            self.assertIn(f"0x{off:x}", PATCH.replace("0x08", "0x8"), name)
        self.assertIn('"soundtap-rootfs"', STU_PROFILES)
        self.assertIn('rt_module_named(pkg, "soundtap-rootfs")', PATCH)
        self.assertLessEqual(len(go_list("ExpectedST20")), define("RT_STU_MODULES"))

    def test_no_signature(self) -> None:
        """The stick reads no signature and compiles in no verifier or key.

        The owner's decision (2026-11-24): the keys held at power-on are the authorization.
        No statement is read, and no verifier or key is compiled into U-Boot, so a key
        change no longer changes u-boot.img.
        """
        for gone in ("soundtap-sig.h", "soundtap-keys.h", "soundtap-profile.h", "Update.stu.sig",
                     "st_verify", "obj-$(CONFIG_USB_STORAGE)", "board/ti/am335x/Makefile"):
            self.assertNotIn(gone, PATCH, gone)
        # U-Boot carries no profile word any more, so the image build must not look for one:
        # post-image.sh failed every build on a marker the patch no longer prints.
        self.assertNotIn("soundtap-uboot-profile",
                         (ROOT / "os/board/image/post-image.sh").read_text())

    def test_tweetnacl_is_upstream(self) -> None:
        """The vendored TweetNaCl files match the hashes ADR 0124 records.

        Vendored unchanged (ADR 0124): the hashes recorded there are the files'.
        """
        adr = (ROOT / "docs/decisions/0124-third-party-code.md").read_text()
        for name in ("tweetnacl.c", "tweetnacl.h"):
            digest = hashlib.sha256((ROOT / "os/bootloader/uboot" / name).read_bytes()).hexdigest()
            self.assertIn(f"`{name}` sha256 `{digest}`", adr)

    def test_bose_chains(self) -> None:
        """The patch knows exactly Bose's module chains, each with one kernel, rootfs, SPL and U-Boot.

        Every stock family the stick takes Bose's firmware back from: profiles.go's three and
        the ones Bose's 27.0.6 packages for the SA-5, the adapters and the Lifestyle 650 use.
        """
        self.assertEqual(sorted(map(tuple, patch_bose_chains())), sorted(map(tuple, BOSE_CHAINS.values())))
        for chain in patch_bose_chains():
            for name in ("linuxPatched", "ubi.img", "MLO", "u-boot.img"):
                self.assertEqual(chain.count(name), 1, chain)
            self.assertLessEqual(len(chain), define("RT_STU_MODULES"))

    def test_bose_boards(self) -> None:
        """Every board our U-Boot knows maps to the stock variant its ubi.img tag names.

        Every board our U-Boot knows (rt_boards in the scm patch) takes Bose's firmware back,
        by the stock variant its ubi.img tag names. No board holds ubi.img to models.json's
        stock_rootfs_limit: that is Bose's timed streamer's, and the stick writes the slot.
        """
        rows = dict(re.findall(r'^\+\t\{ "(\w+)", "(\w+)" \},$', PATCH, re.MULTILINE))
        scm = re.findall(r'^\+\t\{ "(\w+)", "\w+", "(sm2|scm)",', (ROOT / "os/bootloader/uboot/uboot-2026.07-scm-nand-wp.patch").read_text(), re.MULTILINE)
        self.assertEqual(set(rows), {b for b, _ in scm})
        self.assertEqual(rows, STOCK_NAMES)
        self.assertNotIn("rootfs_max", PATCH)
        self.assertIn("pkg->rootfs->size > RT_SLOT_SIZE + RT_STU_OVERLAP", PATCH)

    def test_bootdelay(self) -> None:
        """The way back puts bootdelay -2 back to Bose's 1, as InstallStockRecipes does.

        InstallStockRecipes puts bootdelay back from our -2 to Bose's 1; the boards whose
        UART0 is a link get -2 from rt_board_setup, so the way back does the same for them.
        """
        recipes = (ROOT / "app/internal/flash/recipes.go").read_text()
        self.assertIn('if v, ok := env.Get("bootdelay"); ok && v == "-2" {\n\t\t\tenv.Set("bootdelay", "1")', recipes)
        self.assertIn('if (env_get("bootdelay") && !strcmp(env_get("bootdelay"), "-2"))\n+\t\tenv_set("bootdelay", "1");', PATCH)

    def test_layout_constants(self) -> None:
        """The kernel, SPL, U-Boot and environment offsets match the partition table and ubootenv."""
        parts = {name: (int(size, 16), int(off, 16))
                 for size, off, name in re.findall(r"(0x[0-9A-F]+)@(0x[0-9A-F]+)\(([^)]+)\)", ENV)}
        self.assertEqual(parts["NAND.Kernel-Ping"], (define("RT_KERNEL_SIZE"), define("RT_PING_KERNEL")))
        self.assertEqual(parts["NAND.Kernel-Pong"], (define("RT_KERNEL_SIZE"), define("RT_PONG_KERNEL")))
        for i, name in enumerate(("NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3")):
            self.assertEqual(parts[name], (define("RT_SPL_SIZE"), i * define("RT_SPL_SIZE")), name)
        self.assertEqual(parts["NAND.U-Boot"][1], define("RT_UBOOT"))
        self.assertEqual(parts["NAND.U-Boot-Env"][1], define("RT_ENV_OFFSET"))
        bootloaders = (ROOT / "app/internal/install/bootloaders.go").read_text()
        second = int(re.search(r"^const SecondUBootOffset = (0x[0-9a-f]+)$", bootloaders, re.MULTILINE).group(1), 16)
        ubootenv = (ROOT / "app/internal/ubootenv/ubootenv.go").read_text()
        redundant = int(re.search(r"RedundantOffset = (0x[0-9a-f]+)", ubootenv).group(1), 16)
        self.assertEqual(define("RT_UBOOT_SPARE"), define("RT_UBOOT") + second)
        self.assertEqual(define("RT_ENV_REDUND"), define("RT_UBOOT") + redundant)
        self.assertEqual(define("RT_UBOOT_MAX"), redundant - second)  # maxUBootSize
        self.assertEqual(define("RT_ENV_LEGACY"), int(re.search(r"LegacySize = (0x[0-9a-f]+)", ubootenv).group(1), 16))
        self.assertEqual(define("RT_ENV_SIZE"), int(re.search(r"\tSize = (0x[0-9a-f]+)", ubootenv).group(1), 16))
        for key, value in (("ENV_OFFSET", "RT_ENV_OFFSET"), ("ENV_OFFSET_REDUND", "RT_ENV_REDUND"), ("ENV_SIZE", "RT_ENV_SIZE")):
            self.assertEqual(int(re.search(rf"^CONFIG_{key}=(0x[0-9a-f]+)$", UBOOT_CONFIG, re.MULTILINE).group(1), 16), define(value))

    def test_stock_copy_order(self) -> None:
        """The patch writes Bose's boot chain copies in stockBootSlots order.

        stockBootSlots: the first U-Boot copy, SPL 1, 2, 3, 0, the spare.
        """
        bootloaders = (ROOT / "app/internal/install/bootloaders.go").read_text()
        slots = re.findall(r"\{(\d), (0|SecondUBootOffset), staged(\w+)\}",
                           re.search(r"func stockBootSlots\(\) \[\]bootSlot \{\n\treturn \[\]bootSlot\{(.*?)\n\t\}", bootloaders,
                                     re.DOTALL).group(1))
        go = [f"{'U-Boot' if kind == 'UBoot' else 'SPL'}:{mtd}:{off}" for mtd, off, kind in slots]
        rows = re.findall(r"^\+\t\{ ([^,]+), ([^,]+), (true|false), \"([^\"]+)\" \},$",
                          re.search(r"rt_stock_copies\[\] = \{(.*?)^\+\};", PATCH, re.MULTILINE | re.DOTALL).group(1), re.MULTILINE)
        spl = define("RT_SPL_SIZE")
        def where(expr: str) -> str:
            start = c_product(expr)
            if start < define("RT_UBOOT"):
                return f"{start // spl}:0"
            return "4:0" if start == define("RT_UBOOT") else "4:SecondUBootOffset"
        self.assertEqual([f"{'U-Boot' if u == 'true' else 'SPL'}:{where(s)}" for s, _, u, _ in rows], go)

    def test_stock_recipes(self) -> None:
        """The patch's stock recipes and the variables they need match flash.stockRecipes.

        Bose's slot recipes, byte for byte as flash.stockRecipes formats them.
        """
        recipes = (ROOT / "app/internal/flash/recipes.go").read_text()
        tmpl = "".join(re.findall(r'"([^"]*)"', re.search(r"^const stockSlotRecipe = (.*?)\n\n", recipes, re.MULTILINE | re.DOTALL).group(1)))
        want = []
        for key, slot, args, addr in (("nand_boot", "Ping", "nand_args", "kernel0_nand_address"),
                                      ("nand_boot2", "Pong", "nand_args2", "kernel1_nand_address")):
            want.append(key + "=" + tmpl.replace("%[1]s", slot).replace("%[2]s", args).replace("%[3]s", addr))
        got = c_strings(re.search(r"^\+static const char \*const rt_stock_recipes\[\] = \{(.*?)^\+\};", PATCH, re.MULTILINE | re.DOTALL).group(1))
        self.assertEqual(got, want)
        # Every entry those recipes read that only Bose's environment has is required first.
        needed = set(re.findall(r"\$\{(\w+)\}", tmpl.replace("%[3]s", "kernel0_nand_address"))) | {"nand_args", "nand_args2", "kernel1_nand_address"}
        vars_ = set(c_strings(re.search(r"^\+static const char \*const rt_stock_vars\[\] = \{(.*?)^\+\};", PATCH, re.MULTILINE | re.DOTALL).group(1)))
        self.assertEqual(needed - vars_, {"variant", "module_type"})  # Bose's U-Boot sets those itself

    def test_keys(self) -> None:
        """The recovery chord is Volume Down + Preset 4 by defaultKeymap's indices."""
        keymap = {v: int(k) for k, v in re.findall(r'(\d+): "([A-Z_0-9]+)"',
                                                         BUTTONS.split("func defaultKeymap")[1].split("\n}")[0])}
        self.assertIn(f"RT_KEY({keymap['VOLUME_DOWN']}) | RT_KEY({keymap['PRESET_4']})", PATCH)

    def test_keypad_gpios(self) -> None:
        """The patch scans the ST10 keypad on keypadGeometryFor's strobe and switch-in GPIOs."""
        # keypadGeometryFor's ST10 entry: one field per line (the ST20's is on one line).
        strobes = re.search(r"^\s*strobeOffsets: \[\]int\{([^}]*)\},\s*//", KEYPAD, re.MULTILINE).group(1)
        ins = re.search(r"^\s*switchOffsets: \[\]int\{([^}]*)\},\s*//", KEYPAD, re.MULTILINE).group(1)
        want_s = ", ".join(f'"gpio@4804c000_{n.strip()}"' for n in strobes.split(","))
        want_i = ", ".join(f'"gpio@44e07000_{n.strip()}"' for n in ins.split(","))
        self.assertIn(want_s, PATCH)
        self.assertIn(want_i, PATCH)

    def test_st20_keypad_gpios(self) -> None:
        """The patch scans the ST20 keypad on keypad.go's GPIOs."""
        spotty = re.search(r"strobeOffsets: \[\]int\{([^}]*)\}, switchOffsets: \[\]int\{([^}]*)\}", KEYPAD)
        for offsets in spotty.groups():
            self.assertIn(", ".join(f'"gpio@44e07000_{n.strip()}"' for n in offsets.split(",")), PATCH)

    def test_wla_buttons(self) -> None:
        """The patch reads the WLA's two buttons on the device tree's GPIOs and pads."""
        rows = re.search(r"switchins = <(\d+) (\d+)>", WLA_DTS).groups()
        self.assertEqual(rows, ("23", "27"))
        self.assertIn(", ".join(f'"gpio@44e07000_{n}"' for n in rows), PATCH)
        for pad in ("gpmc_ad9", "gpmc_ad11"):
            self.assertIn(f"{{OFFSET({pad}), MODE(7) | RXACTIVE | PULLUDDIS}}", PATCH)
        self.assertIn('!strcmp(v, "binky")', PATCH)

    def test_ginger_combo(self) -> None:
        """The ST300 combo reads the pin the soundbar processor raises, per board revision.

        Stock U-Boot's ginger route: the LPM raises gpio0_26 (this SM2 revision) or
        gpio0_23 (the other) at power-on; gpmc_ad10 and gpmc_ad9 are those pads.
        """
        self.assertIn('rev2 ? "gpio@44e07000_26" : "gpio@44e07000_23"', PATCH)
        self.assertIn("rev2 ? OFFSET(gpmc_ad10) : OFFSET(gpmc_ad9)", PATCH)
        self.assertIn("dm_gpio_get_value(&in) == 1", PATCH)
        self.assertIn('!strcmp(v, "ginger")', PATCH)

    def test_hunk_counts(self) -> None:
        """Every hunk header of the patch counts its lines right."""
        lines = PATCH.split("\n")
        for i, line in enumerate(lines):
            m = re.match(r"@@ -\d+,(\d+) \+\d+,(\d+) @@", line)
            if not m:
                continue
            old = new = 0
            for body in lines[i + 1:]:
                if body.startswith(" "):
                    old, new = old + 1, new + 1
                elif body.startswith("+"):
                    new += 1
                elif body.startswith("-"):
                    old += 1
                else:
                    break
            self.assertEqual((old, new), (int(m.group(1)), int(m.group(2))), line)

    def test_slots(self) -> None:
        """The rootfs slots match the partition table, and the stick writes the slot not booting."""
        parts = {name: (int(size, 16), int(off, 16))
                     for size, off, name in re.findall(r"(0x[0-9A-F]+)@(0x[0-9A-F]+)\(([^)]+)\)", ENV)}
        self.assertEqual(parts["NAND.File-System-Ping"], (define("RT_SLOT_SIZE"), define("RT_PING_ROOTFS")))
        self.assertEqual(parts["NAND.File-System-Pong"], (define("RT_SLOT_SIZE"), define("RT_PONG_ROOTFS")))
        # nand_boot is Ping, nand_boot2 Pong (os/support/uboot_recipes.py, bootselect.go).
        self.assertIn('pong ? "run nand_boot;" : "run nand_boot2;"', PATCH)
        self.assertIn("pong ? RT_PING_ROOTFS : RT_PONG_ROOTFS", PATCH)

    def test_env_marker(self) -> None:
        """The patch's environment marker is the one soundtapd looks for.

        soundtapd writes the redundant environment only under a U-Boot that says it reads
        it on the kernel command line (ADR 0010); the two spellings are one.
        """
        go = re.search(r'^const EnvRedundantMarker = "([^"]+)"$', FLASH, re.MULTILINE).group(1)
        self.assertEqual(re.search(r'^\+#ifdef CONFIG_ENV_REDUNDANT\n\+#define RT_ENV_MARKER\s+" ([^"]+)"$',
                                   PATCH, re.MULTILINE).group(1), go)

    def test_st10_partition_table_matches_shared(self) -> None:
        """The ST10 device tree's partition table matches the shared one.

        The ST10 device tree carries its own copy of the table the others include.
        """
        dts = ROOT / "os/board/common/dts/ti/omap"
        def table(name: str) -> list[tuple[str, str]]:
            return re.findall(r'label = "(NAND\.[^"]+)";\s*reg = <(0x\w+ 0x\w+)>',
                              (dts / name).read_text())
        shared = table("am335x-soundtap-nand.dtsi")
        self.assertEqual(len(shared), NAND_PARTITIONS)
        self.assertEqual(table("am335x-soundtap-st10.dts"), shared)


# The power-on path, compiled on the host from the patches with the hardware stubbed out:
# every EVT_LAST_STAGE_INIT spy in U-Boot's order (rt_board_setup reads the variant,
# rt_usb_recovery reads the gesture and runs rt_usb_update), over an environment table that
# holds only what was set. An erased or torn mtd5 leaves U-Boot its built-in bootcmd plus the
# variant_raw board_late_init sampled; an installed one carries the slot recipes.
# PKG is what the stick holds (CHAIN its module names), FAIL_AT a stub that fails, SAME the
# boot chain copies that hold Bose's bytes already. Every NAND write and environment save
# lands in the event log, in order, for the power-cut walk below.
PROBE = r"""
#include <errno.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define loff_t long long	/* glibc has it, macOS does not */
struct fdt_property;
typedef unsigned char u8;
typedef unsigned int u32;
typedef unsigned long ulong;
#define min_t(t, a, b) ((t)(a) < (t)(b) ? (t)(a) : (t)(b))
#define roundup(x, y) ((((x) + ((y) - 1)) / (y)) * (y))
static const char *board = BOARD;	/* st10, st20, wla or st300 */
static const int held = HELD;		/* that model's recovery gesture, held at power-on */
static const char *default_bootcmd = DEFAULT_BOOTCMD;
static const char *pkg_kind = PKG;
static const char *same = SAME;
static int fail(const char *what) { return !strcmp(FAIL_AT, what) ? -EIO : 0; }

/* U-Boot's numbers, whatever the host's errno.h says. */
#undef EBADMSG
#define EBADMSG 74

static char events[8192];
static void ev(const char *fmt, ...)
{
	size_t n = strlen(events);
	va_list ap;

	va_start(ap, fmt);
	vsnprintf(events + n, sizeof(events) - n, fmt, ap);
	va_end(ap);
	strncat(events, ";", sizeof(events) - strlen(events) - 1);
}

/* CONTROL_STATUS: SYSBOOT 10010b on the ST20 only. Everything else reads 0. */
static u32 readl(unsigned long addr) { return addr == 0x44e10040 ? (!strcmp(board, "st20") ? 0x12 : 0x18) : 0; }
static void mdelay(int ms) { (void)ms; }
static void configure_module_pin_mux(void *mux) { (void)mux; }
static int scm_nand_wp_pin_mux[1];
static unsigned long hextoul(const char *s, char **end) { return strtoul(s, end, 16); }
static unsigned long dectoul(const char *s, char **end) { return strtoul(s, end, 10); }

/* The environment: a key is there only once set, as in U-Boot; set to NULL or "" it goes. */
#define ENV_MAX 64
static struct { char key[32]; char *val; } env[ENV_MAX];
static const char *env_get(const char *k)
{
	for (int i = 0; i < ENV_MAX; i++)
		if (env[i].val && !strcmp(env[i].key, k))
			return env[i].val;
	return NULL;
}
static int env_set(const char *k, const char *v)
{
	int i, free_slot = -1;

	if (strlen(k) >= sizeof(env[0].key))
		abort();
	for (i = 0; i < ENV_MAX; i++) {
		if (env[i].val && !strcmp(env[i].key, k)) {
			free(env[i].val);
			env[i].val = v && *v ? strdup(v) : NULL;
			return 0;
		}
		if (!env[i].val && free_slot < 0)
			free_slot = i;
	}
	if (!v || !*v)
		return 0;
	if (free_slot < 0)
		abort();
	strcpy(env[free_slot].key, k);
	env[free_slot].val = strdup(v);
	return 0;
}
/* What a saved environment boots: the slot bootcmd names, and whose recipes run it. */
static const char *sel(void)
{
	const char *b = env_get("bootcmd");

	return !b ? "none" : !strcmp(b, "run nand_boot;") ? "Ping" : !strcmp(b, "run nand_boot2;") ? "Pong" : "default";
}
static const char *kind(void)
{
	const char *r = env_get("nand_boot");

	return !r ? "none" : strstr(r, "nboot.e") ? "stock" : strstr(r, "slot_boot") ? "ours" : "other";
}
static int saves, save_calls;
static int env_save(void)
{
	char what[16];
	int ret;

	snprintf(what, sizeof(what), "save%d", ++save_calls);
	ret = fail("env_save") || fail(what) ? -EIO : 0;
	if (!ret && saves == 1)
		ret = fail("second_env_save");
	if (ret) {
		ev("save:torn");
		return ret;
	}
	saves++;
	ev("save:%s:%s", sel(), kind());
	return 0;
}
static unsigned long env_get_ulong(const char *k, int base, unsigned long dflt)
{
	const char *v = env_get(k);

	return v ? strtoul(v, NULL, base) : dflt;
}
static unsigned long bootcount = 7; /* the old slot kept failing: why recovery runs */
static void bootcount_store(unsigned long n) { bootcount = n; }
static unsigned long bootcount_load(void) { return bootcount; }
static int resets;
static int do_reset(void *cmd, int flag, int argc, char *const argv[])
{
	(void)cmd; (void)flag; (void)argc; (void)argv;
	ev("reset");
	return ++resets;
}

/* Each model reads its own gesture; on the others those pins are not the gesture. */
static bool rt_wla_both_held(void) { return held && !strcmp(board, "wla"); }
static bool rt_single_button_held(void) { return held; }
static bool rt_ginger_combo(void) { return held && !strcmp(board, "st300"); }
static void rt_lamp(bool on) { (void)on; }
static const char *lamp = "none";	/* which failure the lamps showed */
static void rt_lamp_fail(void) { lamp = "fail"; }
static void rt_lamp_refused(void) { lamp = "refused"; }

/* Two SHA-256 passes; "digest2" makes the second one come out otherwise. */
#define SHA256_SUM_LEN 32
typedef struct { int n; } sha256_context;
static int finishes;
static void sha256_starts(sha256_context *c) { c->n = 0; }
static void sha256_update(sha256_context *c, const u8 *b, u32 n) { (void)c; (void)b; (void)n; }
static void sha256_finish(sha256_context *c, u8 *d)
{
	(void)c;
	memset(d, 0, SHA256_SUM_LEN);
	d[0] = ++finishes == 2 && !strcmp(FAIL_AT, "digest2");
}
static int rt_hash(u8 *buf, sha256_context *ctx, loff_t off, loff_t len) { (void)buf; (void)ctx; (void)off; (void)len; return 0; }
/* Reads zeroes, as the headers rt_scan kept; "hdr" reads one back otherwise. */
static int rt_read(void *buf, loff_t off, loff_t len)
{
	(void)off;
	memset(buf, 0, len);
	((u8 *)buf)[0] = !strcmp(FAIL_AT, "hdr");
	return 0;
}
static u32 crc32(u32 c, const u8 *p, u32 n) { (void)c; (void)n; return p[0]; }

struct mtd_info { u32 erasesize, writesize; };
static struct mtd_info nand = { 0x20000, 0x800 };
static long long written = -1;
static struct mtd_info *get_nand_dev_by_index(int i) { (void)i; return &nand; }
static void *malloc_cache_aligned(size_t n) { return malloc(n); }
static int rt_usb_host_start(void) { return 0; }
static void rt_st20_microbsel(int high) { (void)high; }
static int rt_find_stick(void) { return 0; }
static void usb_stop(void) {}
static int usb_init(void) { return 0; }
static int fs_size(const char *name, loff_t *size) { (void)name; *size = 64 << 20; return 0; }
/* The manufacturing partition: MFG_NAME is the product name it holds ("" for an empty field). */
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
static int nand_read_skip_bad(struct mtd_info *mtd, loff_t off, size_t *len, size_t *actual, loff_t lim, u8 *buf)
{
	(void)mtd; (void)actual; (void)lim;
	memset(buf, 0xff, *len);
	if (off == 0x0fe00000 && strcmp(MFG_NAME, ""))
		strcpy((char *)buf + 0x170, MFG_NAME);
	return fail("mfg");
}
#define CONFIG_BOOTCOUNT_LIMIT
DEFINES
STRUCT
static int mtd_block_isbad(struct mtd_info *m, loff_t ofs) { (void)m; return ofs == RT_ENV_OFFSET && !strcmp(FAIL_AT, "env_bad"); }

/* The package the stick holds, as its header walk found it (rt_scan is held to real files
 * in UsbRecoveryImage): CHAIN's modules, a 128 KiB module every 4 MiB. */
static const char *const chain[] = { CHAIN };
static int rt_scan(u8 *hdr, loff_t file, struct rt_pkg *pkg)
{
	(void)hdr; (void)file;
	if (!strcmp(pkg_kind, "notstu"))
		return -ENOEXEC;
	if (fail("hdrcrc"))
		return -EBADMSG;
	for (pkg->n = 0; pkg->n < (int)ARRAY_SIZE(chain); pkg->n++) {
		struct rt_module *m = &pkg->m[pkg->n];

		m->off = RT_STU_HEADER + pkg->n * 0x400000LL;
		m->size = !strcmp(chain[pkg->n], "ubi.img") && fail("big") ? 0x10000000 : 0x20000;
		m->body = m->size;
		snprintf(m->name, sizeof(m->name), "%s", chain[pkg->n]);
		snprintf(m->tag, sizeof(m->tag), "%s", !strcmp(m->name, "ubi.img") ? @TAG@ : "27.0.6.46330.5043500");
	}
	return 0;
}
static int rt_check(u8 *buf, const struct rt_pkg *pkg, sha256_context *ctx, loff_t file)
{
	(void)buf; (void)pkg; (void)ctx; (void)file;
	return fail("crc") ? -EBADMSG : 0;
}
static int rt_kernel_ok(u8 *buf, const struct rt_module *m) { (void)buf; (void)m; return fail("kernel_hdr") ? -ENOEXEC : 0; }
static int rt_boot_ok(const u8 *a, u32 al, const u8 *b, u32 bl) { (void)a; (void)al; (void)b; (void)bl; return fail("boot_ok") ? -ENOEXEC : 0; }
static int rt_env_export(int margin) { (void)margin; return fail("env_fit") ? -EFBIG : 0; }
/* NAND writes, logged: a stub that fails leaves its range torn. */
static int rt_write(struct mtd_info *mtd, u8 *buf, const struct rt_module *m, loff_t part, loff_t len, sha256_context *ctx)
{
	bool kernel = part == RT_PING_KERNEL || part == RT_PONG_KERNEL;
	const char *slot = part == RT_PING_ROOTFS || part == RT_PING_KERNEL ? "Ping" : "Pong";

	(void)mtd; (void)buf; (void)m; (void)ctx;
	if (len != (kernel ? RT_KERNEL_SIZE : RT_SLOT_SIZE))
		abort();
	if (!kernel)
		written = part;
	if (fail(kernel ? "kernel_write" : "rt_write")) {
		ev("%s:%s:torn", kernel ? "kernel" : "rootfs", slot);
		return -EIO;
	}
	ev("%s:%s", kernel ? "kernel" : "rootfs", slot);
	return 0;
}
static int rt_capture(u8 *dst, const struct rt_module *m, sha256_context *ctx)
{
	(void)ctx;
	memset(dst, 0x5a, m->body);
	return fail("capture") ? -EIO : 0;
}
static int legacy_calls;
static int rt_env_legacy(struct mtd_info *mtd, bool drop_redundant)
{
	char what[16];

	(void)mtd;
	snprintf(what, sizeof(what), "legacy%d", ++legacy_calls);
	if (fail(what)) {
		ev("mtd5:torn");
		return -EIO;
	}
	ev("mtd5:%s:%s", sel(), kind());
	if (drop_redundant) {
		if (fail("redund_erase")) {
			ev("redund:torn");
			return -EIO;
		}
		ev("redund:erased");
	}
	return 0;
}
COPIES
static const char *copy_name(const struct rt_copy *c)
{
	return c->start == RT_UBOOT ? "uboot" : c->start == RT_UBOOT_SPARE ? "spare" :
		c->start == 0 ? "spl0" : c->start == RT_SPL_SIZE ? "spl1" : c->start == 2 * RT_SPL_SIZE ? "spl2" : "spl3";
}
static bool rt_copy_holds(struct mtd_info *mtd, const struct rt_copy *c, const u8 *d, u32 len, bool strict)
{
	char key[16];

	(void)mtd; (void)d; (void)len;
	if (!strict)
		return !fail("verify");
	snprintf(key, sizeof(key), ",%s,", copy_name(c));
	return strstr(same, key) != NULL;
}
static bool rt_copy_fits(struct mtd_info *mtd, const struct rt_copy *c, u32 len) { (void)mtd; (void)c; (void)len; return !fail("fits"); }
static bool rt_spl_readable(struct mtd_info *mtd) { (void)mtd; return !fail("spl_read"); }
static int rt_put(struct mtd_info *mtd, const struct rt_copy *c, const u8 *d, u32 len)
{
	char what[16];

	(void)mtd; (void)d; (void)len;
	snprintf(what, sizeof(what), "put:%s", copy_name(c));
	if (fail(what)) {
		ev("%s:torn", copy_name(c));
		return -EIO;
	}
	ev("%s", copy_name(c));
	return 0;
}
static int rt_keypad_scan(void)
{
	return held && (!strcmp(board, "st10") || !strcmp(board, "st20")) ? RT_KEYS_PRESERVE : 0;
}
FUNCTIONS
static const char *show(const char *k) { return env_get(k) ? env_get(k) : "unset"; }
int main(void)
{
	env_set("bootcmd", default_bootcmd);
	if (strcmp(VARIANT_RAW, ""))
		env_set("variant_raw", VARIANT_RAW);
	if (strcmp(INSTALLED_BOOTCMD, "")) {
		env_set("bootcmd", INSTALLED_BOOTCMD);
		env_set("soundtap_args", "setenv bootargs variant=" INSTALLED_VARIANT);
		if (strcmp(INSTALLED_ALT, ""))
			env_set("altbootcmd", INSTALLED_ALT);
		if (strcmp(INSTALLED_LIMIT, ""))
			env_set("bootlimit", INSTALLED_LIMIT);
		/* The rest of the recipes its installer wrote; Bose's environment has none of them. */
		if (!BOSE_RECIPES) {
			env_set("nand_boot", "boot Ping; run slot_boot");
			env_set("nand_boot2", "boot Pong; run slot_boot");
			env_set("slot_boot", "installed");
			env_set("stock_args", "installed");
			env_set("mtdids", "installed");
			env_set("mtdparts", "installed");
		} else {
			env_set("nand_boot", "boot Ping with nboot.e");
			env_set("nand_boot2", "boot Pong with nboot.e");
		}
		/* What only Bose's own environment holds, and what a SoundTap install added to it. */
		if (BOSE_VARS) {
			env_set("loadaddr", "0x82000000");
			env_set("kloadaddr", "0x80007fc0");
			env_set("kernel0_nand_address", "0x280000");
			env_set("kernel1_nand_address", "0x780000");
			env_set("nand_args", "setenv bootargs console=${console} ubi.mtd=8,2048  soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048");
			env_set("nand_args2", "setenv bootargs console=${console} ubi.mtd=9,2048");
			env_set("upgrade_available", "1");
		}
	}
	SPIES
	/* The boot recipe runs soundtap_args, which sets bootargs; */
	/* bootm hands the device tree what board_fdt_chosen_bootargs makes of it. */
	{
		const char *a = env_get("soundtap_args");

		env_set("bootargs", a && !strncmp(a, "setenv bootargs ", 16) ? a + 16 : "console=ttyGS0");
	}
	for (int i = 0; i < ENV_MAX; i++)
		if (env[i].val)
			printf("env:%s=%s\n", env[i].key, env[i].val);
	printf("cmdline=%s|", board_fdt_chosen_bootargs(NULL));
	printf("lamp=%s|", lamp);
	printf("resets=%d|", resets);
	printf("events=%s|", events);
	printf("rt_v=%s|written=%s|saves=%d|bootcmd=%s|altbootcmd=%s|bootlimit=%s|bootcount=%lu|nand_boot=%s|nand_boot2=%s|args=%s|bootdelay=%s|upgrade_available=%s|env_bootcount=%s\n",
               show("rt_v"),
               written < 0 ? "none" : written == RT_PING_ROOTFS ? "Ping" : written == RT_PONG_ROOTFS ? "Pong" : "?",
               saves, !strcmp(show("bootcmd"), default_bootcmd) ? "default" : show("bootcmd"),
               show("altbootcmd"), show("bootlimit"), bootcount,
               env_get("nand_boot") ? "set" : "unset", env_get("nand_boot2") ? "set" : "unset", show("soundtap_args"), show("bootdelay"),
               show("upgrade_available"), show("bootcount"));
	return 0;
}
"""

SCM_PATCH = (ROOT / "os/bootloader/uboot/uboot-2026.07-scm-nand-wp.patch").read_text()
# Applied in this order by os/bootloader/build-wla-uboot.sh.
SPY_PATCHES = (SCM_PATCH, PATCH)

# The module chains a stick can hold, as profiles.go names them.
CHAINS = {
    "soundtap": go_list("ExpectedSoundTap"),
    **BOSE_CHAINS,
    "bose-unknown": [*BOSE_CHAINS["bose-sm2"][:4], "SomethingNew", *BOSE_CHAINS["bose-sm2"][4:]],
    "notstu": [],
}
# The version tag each stock package's ubi.img carries (@<module>/<variants>); the ST20's and the
# ST300's packages are not recorded here, theirs follow the same form.
TAGS = {"bose-sm2": "@sm2/spotty+mojo+binky", "bose-st20": "@scm/spotty+mojo+taigan", "bose-st300": "@sm2/ginger",
        "bose-burns": "@sm2/burns", "bose-nelson-sm2": "@sm2/lisa", "bose-triode-sm2": "@sm2/lisa",
        "bose-marconi-sm2": "@sm2/lisa", "bose-bardeen": "@sm2/bardeen", "bose-wst-scm": "@scm/lisa",
        "bose-triode-scm": "@scm/lisa", "bose-marconi-scm": "@scm/lisa", "bose-unknown": "@sm2/spotty+mojo+binky"}


def patch_function(patch: str, signature: str) -> str:
    """Return the C function of the patch that starts with signature, without the diff's +."""
    body = patch.split("+" + signature)[1].split("\n+}\n")[0]
    return "\n".join(line[1:] for line in ("+" + signature + body + "\n+}").split("\n"))


def patch_block(patch: str, start: str) -> str:
    """Return a top-level declaration of the patch from the line that starts with start to its '};'."""
    return "\n".join(line[1:] for line in re.search(rf"^\+{re.escape(start)}.*?^\+\}};", patch, re.MULTILINE | re.DOTALL).group(0).split("\n"))


def patch_defines(patch: str | None = None) -> str:
    """Return every RT_ macro of the patch, continuation lines and all."""
    patch = (patch or PATCH).replace("\\\n+", "\\\n")
    # RT_ENV_MARKER as the product build has it: CONFIG_ENV_REDUNDANT is on there.
    defines = "\n".join(line[1:] for line in re.findall(r"^\+#define RT_\S+\s+(?:.*\\\n)*.*\S.*$", patch, re.MULTILINE)
                        if "RT_ENV_MARKER" not in line)
    return defines + "\n" + re.search(r"^\+#ifdef CONFIG_ENV_REDUNDANT\n\+(#define RT_ENV_MARKER.*)$", patch, re.MULTILINE).group(1)


def patch_structs() -> str:
    """Return the patch's struct rt_module and struct rt_pkg."""
    return "\n".join(patch_block(PATCH, f"struct {name} {{") for name in ("rt_module", "rt_pkg"))


def last_stage_spies() -> list[str]:
    """Return the EVT_LAST_STAGE_INIT spies the patches add, in the order U-Boot runs them.

    U-Boot's linker script keeps the u_boot_list entries SORTed by name, and a simple spy's entry is
    named after the event and then the function, so one event's spies run by function name.
    """
    names = [fn for patch in SPY_PATCHES
             for fn in re.findall(r"^\+EVENT_SPY_SIMPLE\(EVT_LAST_STAGE_INIT, (\w+)\);", patch, re.MULTILINE)]
    return sorted(names)


def spy_function(name: str) -> str:
    """Return the body of the spy function name from whichever patch defines it."""
    for patch in SPY_PATCHES:
        m = re.search(rf"^\+static int {name}\(void\)$", patch, re.MULTILINE)
        if m:
            return patch_function(patch, f"static int {name}(void)")
    msg = f"spy {name} has no body in the patches"
    raise AssertionError(msg)


def cc(src: str, *extra: str, opt: str = "-O0") -> str:
    """Compile src on the host, as strictly as the patch is built.

    Return the directory that holds the binary `probe`; the caller removes it.
    """
    d = tempfile.mkdtemp()
    Path(d, "probe.c").write_text(src)
    subprocess.run(["cc", "-std=gnu11", opt, "-Wall", "-Werror", "-Wno-unused-function", *extra, "-o", f"{d}/probe",
                    f"{d}/probe.c"], check=True)
    return d


# What a board boots after a power cut, from the probe's event log (simulate): which SPL the ROM
# runs (the first copy that is not torn), which U-Boot that SPL loads (ours prefers our spare
# over a first copy that is not ours, ADR 0009; Bose's reads the first copy only), which
# environment that U-Boot reads (ours: the newer valid redundant copy, else the 8 KiB copy,
# CONFIG_ENV_REDUNDANT_UPGRADE, else its built-in bootcmd; Bose's: the 8 KiB copy only), and
# whether the slot it selects is whole. The pairings nobody has proven, our SPL with Bose's
# U-Boot and Bose's SPL with ours, and our U-Boot running Bose's nboot.e recipes, count as no boot.
@dataclass(frozen=True)
class Before:
    """The board before the events: the slot booting, the newer redundant copy (P or R), the package."""

    booting: str = "Ping"
    current: str = "P"
    package: str = "bose"


def _apply_events(done: list[list[str]], before: Before) -> tuple[list[str], dict, dict, dict]:
    """Return NAND after the events in done: SPL copies, U-Boot copies, environment copies, slots."""
    booting, current, package = before.booting, before.current, before.package
    other = {"Ping": "Pong", "Pong": "Ping"}[booting]
    spl, uboot = ["ours"] * 4, {"uboot": "ours", "spare": "ours"}
    serial = {"P": 2, "R": 1} if current == "P" else {"P": 1, "R": 2}
    env = {c: {"type": "redundant", "serial": serial[c], "sel": booting, "kind": "ours"} for c in "PR"}
    slots = {booting: {"kernel": "soundtap", "rootfs": "soundtap"}, other: {"kernel": "old", "rootfs": "old"}}
    for e in done:
        cut_short = e[-1] == "torn"
        name = e[0]
        if name == "save":
            target = min("PR", key=lambda c: env[c].get("serial", -1) if env[c] else -1)
            top = max((env[c]["serial"] for c in "PR" if env[c] and env[c]["type"] == "redundant"), default=0)
            env[target] = None if cut_short else {"type": "redundant", "serial": top + 1, "sel": e[1], "kind": e[2]}
        elif name in ("kernel", "rootfs"):
            # A SoundTap rootfs carries its kernel; Bose's kernel has a partition of its own.
            for part in ("kernel", "rootfs") if package == "soundtap" else (name,):
                slots[e[1]][part] = "torn" if cut_short else package
        elif name == "mtd5":
            env["P"] = None if cut_short else {"type": "legacy", "sel": e[1], "kind": e[2]}
        elif name == "redund":
            env["R"] = None
        elif name.startswith("spl"):
            spl[int(name[3])] = "torn" if cut_short else "bose"
        elif name in uboot:
            uboot[name] = "torn" if cut_short else "bose"
        elif name != "reset":
            msg = f"unknown event {e}"
            raise AssertionError(msg)
    return spl, uboot, env, slots


def _loader(spl: list[str], uboot: dict[str, str]) -> tuple[str | None, str | None]:
    """Return which U-Boot runs ("ours" or "bose"), or why nothing does."""
    rom = next((s for s in spl if s != "torn"), None)
    if rom is None:
        return None, "no SPL"
    if rom == "ours":
        if "ours" in (uboot["uboot"], uboot["spare"]):
            return "ours", None
        return None, "our SPL with Bose's U-Boot" if uboot["uboot"] == "bose" else "no U-Boot"
    if uboot["uboot"] != "bose":
        return None, "Bose's SPL with our U-Boot" if uboot["uboot"] == "ours" else "no U-Boot"
    return "bose", None


def _environment(loader: str, env: dict, booting: str) -> tuple[dict | None, str | None]:
    """Return the environment copy the running U-Boot boots by, or the outcome when it has none of ours."""
    redundant = [env[c] for c in "PR" if env[c] and env[c]["type"] == "redundant"]
    legacy = env["P"] if env["P"] and env["P"]["type"] == "legacy" else None
    if loader == "ours":
        e = max(redundant, key=lambda c: c["serial"]) if redundant else legacy
        if e is None:
            return None, f"{loader}:default:{booting}"  # the built-in bootcmd boots the SoundTap slot
        if e["kind"] == "stock":
            return None, "our U-Boot with Bose's recipes"
        return e, None
    if legacy is None:
        return None, "Bose's U-Boot on its built-in environment"
    return legacy, None


def simulate(events: list[str], cut: int, *, torn: bool, before: Before | None = None) -> str:
    """Return the boot after events[:cut] completed, with events[cut] cut short when torn."""
    before = before or Before()
    done = [e.split(":") for e in events[:cut]] + ([[*events[cut].split(":"), "torn"]] if torn else [])
    spl, uboot, env, slots = _apply_events(done, before)
    loader, why = _loader(spl, uboot)
    if loader is None:
        return why
    e, outcome = _environment(loader, env, before.booting)
    if e is None:
        return outcome
    slot = e["sel"]
    if slot not in slots:
        return f"bootcmd {slot}"
    content = (slots[slot]["kernel"], slots[slot]["rootfs"])
    if content not in (("soundtap", "soundtap"), ("bose", "bose")):
        return f"a half-written slot {slot} {slots[slot]}"
    return f"{loader}:{content[0]}:{slot}"

class ProbeOptions(TypedDict, total=False):
    """What run_probe's power-on finds besides the board, its gesture and its variant_raw."""

    installed: str | None   # the saved bootcmd ("run nand_boot;"), or None for an erased mtd5
    variant: str
    fail_at: str
    spies: list[str] | None
    mfg: str | None
    bose_recipes: bool
    alt: str | None
    limit: str | None
    pkg: str
    same: tuple[str, ...]
    bose_vars: bool
    tag: str | None


@unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
class UsbRecoveryProbe(unittest.TestCase):
    """The power-on path compiled from the patches: the gesture, the install and the way back to Bose."""

    # variant_raw as board_late_init publishes it (hex; build-wla-uboot.sh: WLA 2518-2618,
    # ST10 3630). The ST20 is told apart by SYSBOOT, whatever its ADC reads.
    BOARDS: ClassVar[dict[str, tuple[str, str]]] = {"st10": ("3630", "rhino"), "wla": ("2518", "binky"), "st20": ("", "spotty"),
                                                     "st300": ("3669", "ginger")}

    MFG: ClassVar[dict[str, str]] = {"st10": "rhino", "wla": "binky", "st20": "", "st300": "ginger"}

    def run_probe(self, board: str, variant_raw: str, *, held: bool, **options: Unpack[ProbeOptions]) -> dict:
        """Run one power-on of board and return what it printed.

        options.installed is the saved bootcmd ("run nand_boot;"), or None for an erased mtd5.
        """
        installed, variant = options.get("installed"), options.get("variant", "")
        fail_at, spies, mfg = options.get("fail_at", ""), options.get("spies"), options.get("mfg")
        bose_recipes, alt, limit = options.get("bose_recipes", False), options.get("alt"), options.get("limit")
        pkg, same = options.get("pkg", "soundtap"), options.get("same", ())
        bose_vars, tag = options.get("bose_vars", False), options.get("tag")
        default = re.search(r'^CONFIG_BOOTCOMMAND=(".*")$', UBOOT_CONFIG, re.MULTILINE).group(1)
        spies = last_stage_spies() if spies is None else spies
        functions = "\n".join([
            "\n".join(line[1:] for line in re.findall(r"^\+#define RT_MFG_\S+\s+\S.*$", SCM_PATCH, re.MULTILINE)),
            patch_block(SCM_PATCH, "struct rt_board {"),
            patch_block(SCM_PATCH, "static const struct rt_board rt_boards[] = {"),
            patch_function(SCM_PATCH, "static const struct rt_board *rt_board_named(const char *v)"),
            patch_function(SCM_PATCH, "static const struct rt_board *rt_mfg_variant(void)"),
            patch_function(SCM_PATCH, "static const struct rt_board *rt_scm_saved(void)"),
            patch_function(SCM_PATCH, "static const struct rt_board *rt_scm_variant(const char *raw)"),
            patch_function(PATCH, "static bool rt_st20(void)"),
            patch_block(PATCH, "static const char *const rt_bose_chains[][RT_STU_MODULES] = {"),
            patch_block(PATCH, "static const struct rt_bose {"),
            patch_function(PATCH, "static struct rt_module *rt_module_named(struct rt_pkg *pkg, const char *name)"),
            patch_function(PATCH, "static bool rt_tag_names("),
            patch_function(PATCH, "static int rt_classify(struct rt_pkg *pkg, u32 erasesize)"),
            patch_block(PATCH, "static const char *const rt_env_templates[] = {"),
            patch_function(PATCH, "static int rt_set_recipes(void)"),
            patch_function(PATCH, "static const char *rt_booting_cmd(void)"),
            patch_block(PATCH, "static const char *const rt_stock_recipes[] = {"),
            patch_block(PATCH, "static const char *const rt_stock_vars[] = {"),
            patch_function(PATCH, "static void rt_strip_markers(const char *key)"),
            patch_function(PATCH, "static void rt_stock_env(void)"),
            patch_function(PATCH, "static int rt_select(const char *next, const char *prev)"),
            patch_function(PATCH, "static int rt_boot_chain("),
            patch_function(PATCH, "static int rt_bose_preflight("),
            patch_function(PATCH, "static int rt_bose_finish("),
            patch_function(PATCH, "static int rt_usb_update(void)"),
            "\n".join(line[1:] for line in re.findall(r"^\+static int rt_recovery_result = .*$", PATCH, re.MULTILINE)),
            patch_function(PATCH, "static void rt_mark_cmdline(int ret)"),
            patch_function(PATCH, "const char *board_fdt_chosen_bootargs(const struct fdt_property *fdt_ba)"),
        ] + [spy_function(name) for name in last_stage_spies()])
        src = (PROBE.replace("FUNCTIONS", functions).replace("STRUCT", patch_structs())
               .replace("DEFINES", patch_defines()).replace("DEFAULT_BOOTCMD", default)
               .replace("COPIES", patch_block(PATCH, "static const struct rt_copy {"))
               .replace("CHAIN", ", ".join(f'"{n}"' for n in CHAINS[pkg]) or '""')
               .replace("SPIES", " ".join(f"{name}();" for name in spies))
               .replace("BOARD", f'"{board}"').replace("HELD", str(int(held)))
               .replace("VARIANT_RAW", f'"{variant_raw}"').replace("FAIL_AT", f'"{fail_at}"')
               .replace("INSTALLED_BOOTCMD", f'"{installed or ""}"')
               .replace("INSTALLED_ALT", f'"{alt or ""}"').replace("INSTALLED_LIMIT", f'"{limit or ""}"')
               .replace("INSTALLED_VARIANT", f'"{variant}"').replace("BOSE_RECIPES", str(int(bose_recipes)))
               .replace("BOSE_VARS", str(int(bose_vars)))
               .replace("@TAG@", '"27.0.6.46330.5043500' + (tag if tag is not None else TAGS.get(pkg, "")) + '"')
               .replace("PKG", f'"{pkg}"').replace("SAME", '",' + ",".join(same) + ',"')
               .replace("MFG_NAME", f'"{self.MFG[board] if mfg is None else mfg}"'))
        d = cc(src)
        try:
            out = subprocess.run([f"{d}/probe"], check=True, capture_output=True, text=True).stdout
        finally:
            shutil.rmtree(d)
        r = dict(kv.split("=", 1) for kv in out[out.index("cmdline="):].strip().split("|"))
        # Every entry the environment holds at the end, as env_save would store it.
        r["env"] = dict(line[4:].split("=", 1) for line in out.splitlines() if line.startswith("env:"))
        r["events"] = [e for e in r["events"].split(";") if e]
        return r

    def booted_slot(self, r: dict, bootcmd: str | None = None) -> str | None:
        """Return the slot a boot lands in, by the bootcmd it runs."""
        bootcmd = bootcmd or r["bootcmd"]
        if bootcmd == "default":
            # The built-in bootcmd tries the slots in the order it names them.
            return re.findall(r"ubi part NAND\.File-System-(\w+)", UBOOT_CONFIG)[0]
        recipe = {"run nand_boot;": "nand_boot", "run nand_boot2;": "nand_boot2"}.get(bootcmd)
        if not recipe or r[recipe] != "set":
            return None  # a bootcmd naming a recipe that is not there boots nothing
        return {"nand_boot": "Ping", "nand_boot2": "Pong"}[recipe]

    @staticmethod
    def boots(r: dict, n: int = 5) -> list[str]:
        """Return the bootcmd U-Boot runs for each of n boots that never confirm.

        U-Boot's bootcount choice over the environment left (bootdelay_process: altbootcmd
        once the count passes bootlimit).
        """
        if r["bootlimit"] == "unset":
            return [r["bootcmd"]] * n
        count, limit = int(r["bootcount"]), int(r["bootlimit"])
        return [r["altbootcmd"] if count + i > limit else r["bootcmd"] for i in range(1, n + 1)]

    def test_spies_run_board_setup_first(self) -> None:
        """rt_board_setup runs before rt_usb_recovery, and the gesture depends on that order.

        The variant has to be set before the gesture asks for it on a torn environment.
        """
        self.assertEqual(last_stage_spies(), ["rt_board_setup", "rt_usb_recovery"])
        # And the order is what makes it work: the other way round the ST10 gesture is lost.
        r = self.run_probe("st10", "3630", held=True, spies=["rt_usb_recovery", "rt_board_setup"])
        self.assertEqual(r["written"], "none")

    def test_gesture_installs_and_boots_with_an_erased_environment(self) -> None:
        """On an erased environment the gesture installs, saves every recipe and boots the new slot."""
        for board, (raw, variant) in self.BOARDS.items():
            with self.subTest(board=board):
                r = self.run_probe(board, raw, held=True)
                self.assertEqual(r["rt_v"], variant)
                self.assertNotEqual(r["written"], "none", "the gesture was ignored")
                # Saved, with every recipe the saved bootcmd names: the built-in
                # environment had none of them.
                self.assertEqual(r["saves"], "1")
                self.assertEqual((r["bootcmd"], r["altbootcmd"], r["upgrade_available"]),
                                 ("run nand_boot2;", "run nand_boot;", "1"))
                self.assertEqual(self.booted_slot(r), r["written"])
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=0"), r["cmdline"])

    def test_the_saved_environment_is_complete(self) -> None:
        """After a torn environment the save holds the board's full recipe set.

        After a torn environment the save holds this board's recipes, the same ones every
        installer writes for it, and the nand_args pair soundtapd selects slots through.
        """
        rebuilt = BOOTSELECT[BOOTSELECT.index("func (in *Installer) rebuiltEnv("):]
        for board, (raw, variant) in self.BOARDS.items():
            with self.subTest(board=board):
                env = self.run_probe(board, raw, held=True)["env"]
                for entry in WLA_ENV.recipes(variant.encode()):
                    key, value = entry.decode().split("=", 1)
                    self.assertEqual(env.get(key), value, key)
                for key in ("nand_args", "nand_args2"):
                    self.assertIn(f'env.Set("{key}", "{env[key]}")', rebuilt)

    def test_the_model_comes_from_the_board(self) -> None:
        """The board, not the environment's variant, decides which gesture is read.

        An environment that names another model (a board moved between speakers, a wrong
        restore) does not decide which gesture is looked for: the board does.
        """
        for board, other in (("st10", "binky"), ("wla", "rhino"), ("st300", "rhino"), ("st20", "binky")):
            with self.subTest(board=board):
                raw, _ = self.BOARDS[board]
                r = self.run_probe(board, raw, held=True, installed="run nand_boot;", variant=other)
                self.assertEqual(r["written"], "Pong")
                # Its recipes are all there, so they stay: its installer chose them.
                self.assertTrue(r["args"].endswith(f"variant={other}"), r["args"])

    def test_an_environment_without_our_recipes_gets_them(self) -> None:
        """A Bose environment without our recipes gets the whole set on save.

        Bose's environment names nand_boot (nboot.e) but has no slot_boot: the save holds
        the whole set for the board, so the bootcmd it writes runs.
        """
        r = self.run_probe("wla", "2518", held=True, installed="run nand_boot;", variant="binky", bose_recipes=True)
        self.assertEqual((r["written"], r["saves"], r["bootcmd"]), ("Pong", "1", "run nand_boot2;"))
        for entry in WLA_ENV.recipes(b"binky"):
            key, value = entry.decode().split("=", 1)
            self.assertEqual(r["env"].get(key), value, key)

    def test_no_gesture_with_an_erased_environment_changes_nothing(self) -> None:
        """Without the gesture an erased environment is left as it is."""
        for board, (raw, _) in self.BOARDS.items():
            with self.subTest(board=board):
                r = self.run_probe(board, raw, held=False)
                self.assertEqual((r["written"], r["saves"], r["bootcmd"]), ("none", "0", "default"))

    def test_wla_variant_range(self) -> None:
        """Both ends of the WLA's variant_raw range read as binky."""
        for raw in ("2518", "2618"):
            self.assertEqual(self.run_probe("wla", raw, held=True)["rt_v"], "binky")

    def test_gesture_with_the_installed_environment(self) -> None:
        """With the installed environment the gesture moves bootcmd to the slot it wrote.

        The normal case moves the saved bootcmd to the slot it wrote and tells Linux.
        """
        for board, (raw, variant) in self.BOARDS.items():
            with self.subTest(board=board):
                r = self.run_probe(board, raw, held=True, installed="run nand_boot;", variant=variant)
                self.assertEqual((r["written"], r["saves"], r["bootcmd"]), ("Pong", "1", "run nand_boot2;"))
                self.assertEqual(self.booted_slot(r), "Pong")
                self.assertTrue(r["cmdline"].endswith(" soundtap.env=redundant soundtap.recovery=0"), r["cmdline"])
                # The mark is for this boot only: the environment, which the boot count of
                # the unconfirmed slot saves again, never carries it.
                self.assertNotIn("soundtap.recovery", r["args"])

    def test_recovery_leaves_the_normal_install_env(self) -> None:
        """A recovery leaves bootcmd, altbootcmd and the boot count as a normal install does.

        Bootcmd moved to the new slot while altbootcmd was left alone, so a recovered
        slot that never confirmed kept being retried instead of the preserved one.
        """
        limit = re.search(r"const BootLimit = (\d+)", BOOTSELECT).group(1)
        for running, other in (("run nand_boot;", "run nand_boot2;"), ("run nand_boot2;", "run nand_boot;")):
            with self.subTest(running=running):
                r = self.run_probe("st10", "3630", held=True, installed=running, variant="rhino")
                self.assertEqual((r["bootcmd"], r["altbootcmd"], r["bootlimit"], r["bootcount"]),
                                 (other, running, limit, "0"))
                # Unconfirmed: the count is kept in the environment, saved with the slot.
                self.assertEqual((r["upgrade_available"], r["env_bootcount"]), ("1", "0"))
                # The count starts over: the new slot gets bootlimit tries, then the old one boots.
                self.assertEqual(self.boots(r), [other if n <= int(limit) else running for n in range(1, 6)])

    def test_failed_recovery_selects_nothing(self) -> None:
        """A failed write or save selects nothing and keeps the boot count."""
        for step in ("rt_write", "env_save"):
            with self.subTest(step=step):
                r = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino", fail_at=step)
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=-5"), r["cmdline"])
                # Nothing was installed or saved, so the count stays as it was.
                self.assertEqual((r["saves"], r["bootcount"]), ("0", "7"))
                if step == "rt_write":
                    self.assertEqual((r["bootcmd"], r["altbootcmd"], r["bootlimit"]),
                                     ("run nand_boot;", "unset", "unset"))

    def test_after_a_failed_update_the_slot_that_boots_is_kept(self) -> None:
        """Past bootlimit the stick keeps the booting altbootcmd slot and writes the other.

        Past bootlimit autoboot runs altbootcmd: that slot is the one booting, so the stick
        writes the other one, the slot that kept failing (alpha review, 2026-11-23). Nothing
        names that slot while it is written: both commands point at the booting slot first.
        """
        limit = 4
        r = self.run_probe("st10", "3630", held=True, installed="run nand_boot2;", variant="rhino",
                           alt="run nand_boot;", limit=str(limit))
        self.assertEqual((r["written"], r["saves"]), ("Pong", "2"), r)
        self.assertEqual((r["bootcmd"], r["altbootcmd"], r["bootcount"]),
                         ("run nand_boot2;", "run nand_boot;", "0"))
        self.assertEqual(self.boots(r), ["run nand_boot2;" if n <= limit else "run nand_boot;" for n in range(1, 6)])

    def test_altbootcmd_never_names_the_slot_being_written(self) -> None:
        """The altbootcmd is pointed at the booting slot before the other slot is written.

        Within bootlimit bootcmd's slot boots; altbootcmd named the other one, about to be
        erased, so it is pointed at the booting slot and saved before anything is written.
        """
        r = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino",
                           alt="run nand_boot2;", limit="10", fail_at="rt_write")  # count 7: within
        self.assertEqual((r["written"], r["saves"], r["bootcmd"], r["altbootcmd"]),
                         ("Pong", "1", "run nand_boot;", "run nand_boot;"))

    def test_a_failed_first_save_writes_nothing(self) -> None:
        """A failed first save stops the stick before anything is written."""
        r = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino",
                           alt="run nand_boot2;", limit="10", fail_at="env_save")
        self.assertEqual((r["written"], r["saves"]), ("none", "0"))

    def test_a_failed_final_save_resets(self) -> None:
        """A failed final save resets the board so it boots what was saved.

        The environment in RAM names the new slot, the saved one does not: reset, so the
        boot reads what was saved.
        """
        r = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino",
                           alt="run nand_boot2;", limit="10", fail_at="second_env_save")
        self.assertEqual((r["written"], r["saves"], r["resets"]), ("Pong", "1", "1"))

    def test_an_unsigned_soundtap_package_installs(self) -> None:
        """An unsigned SoundTap package installs on the held keys alone.

        No Update.stu.sig, no statement: the keys held at power-on are the authorization.
        """
        r = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino")
        self.assertEqual((r["written"], r["saves"], r["bootcmd"], r["lamp"]), ("Pong", "1", "run nand_boot2;", "none"))
        self.assertEqual(r["events"], ["rootfs:Pong", "save:Pong:ours"])
        self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=0"), r["cmdline"])

    def test_a_package_that_does_not_check_out_selects_nothing(self) -> None:
        """A package whose CRCs do not check out writes and selects nothing.

        A header or image CRC that does not match: nothing is erased, nothing is saved.
        """
        for step in ("hdrcrc", "crc"):
            with self.subTest(step=step):
                r = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino", fail_at=step)
                self.assertEqual((r["written"], r["saves"], r["bootcmd"], r["bootcount"], r["events"]),
                                 ("none", "0", "run nand_boot;", "7", []))
                self.assertEqual(r["lamp"], "fail")
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=-74"), r["cmdline"])

    def test_a_file_that_is_not_a_stu_is_refused(self) -> None:
        """A file that is not a .stu is refused without a write."""
        r = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino", pkg="notstu")
        self.assertEqual((r["written"], r["saves"], r["events"], r["lamp"]), ("none", "0", [], "refused"))
        self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=-8"), r["cmdline"])

    def test_a_stick_that_reads_back_otherwise_selects_nothing(self) -> None:
        """A stick that reads back otherwise in pass two selects nothing.

        Pass two hashes the file again; a stick that changed under it, or a module header
        that is not the one that placed the write, leaves the old slot booting.
        """
        for step, written in (("digest2", "Pong"), ("hdr", "none")):
            with self.subTest(step=step):
                r = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino", fail_at=step)
                self.assertEqual((r["written"], r["saves"], r["bootcmd"], r["altbootcmd"]),
                                 (written, "0", "run nand_boot;", "unset"))
                self.assertEqual(r["lamp"], "fail")
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=-74"), r["cmdline"])

    def test_a_soundtap_stick_installs_on_the_st20(self) -> None:
        """A SoundTap stick installs on the ST20."""
        r = self.run_probe("st20", "", held=True, installed="run nand_boot2;", variant="spotty")
        self.assertEqual((r["written"], r["saves"], r["bootcmd"], r["lamp"]), ("Ping", "1", "run nand_boot;", "none"))

    # Bose's own package: the way back to stock (POST /api/firmware's route, from the stick).
    BOSE: ClassVar[dict[str, tuple[str, str, str]]] = {
        "st10": ("3630", "rhino", "bose-sm2"), "wla": ("2518", "binky", "bose-sm2"), "st20": ("", "spotty", "bose-st20")}
    BOSE_ORDER: ClassVar[list[str]] = [
        "kernel:Pong", "rootfs:Pong",                    # the slot that is not booting, read back
        "save:Pong:ours", "save:Pong:ours",              # selected, in both redundant copies
        "mtd5:Pong:ours", "redund:erased",               # the 8 KiB copy Bose's U-Boot reads
        "uboot", "spl1", "spl2", "spl3", "spl0", "spare",  # stockBootSlots
        "mtd5:Pong:stock",                               # Bose's recipes
        "reset"]                                         # into Bose's boot chain

    def bose(self, board: str, **kw: Unpack[ProbeOptions]) -> dict:
        """Run the way back to Bose on board with its own stock package, the gesture held."""
        raw, variant, pkg = self.BOSE[board]
        kw.setdefault("installed", "run nand_boot;")
        kw.setdefault("bose_vars", True)
        return self.run_probe(board, raw, held=True, variant=variant, pkg=kw.pop("pkg", pkg), **kw)

    def test_bose_for_this_model_goes_back_to_stock(self) -> None:
        """Bose's package for the model goes back to stock in order, with Bose's recipes."""
        recipes = (ROOT / "app/internal/flash/recipes.go").read_text()
        tmpl = "".join(re.findall(r'"([^"]*)"', re.search(r"^const stockSlotRecipe = (.*?)\n\n", recipes, re.MULTILINE | re.DOTALL).group(1)))
        for board in self.BOSE:
            with self.subTest(board=board):
                r = self.bose(board)
                self.assertEqual(r["events"], self.BOSE_ORDER)
                self.assertEqual((r["lamp"], r["resets"], r["bootcmd"]), ("none", "1", "run nand_boot2;"))
                env = r["env"]
                # InstallStockRecipes: Bose's recipes, and what only our U-Boot reads gone.
                self.assertEqual(env["nand_boot"], tmpl.replace("%[1]s", "Ping").replace("%[2]s", "nand_args")
                                 .replace("%[3]s", "kernel0_nand_address"))
                self.assertEqual(env["nand_boot2"], tmpl.replace("%[1]s", "Pong").replace("%[2]s", "nand_args2")
                                 .replace("%[3]s", "kernel1_nand_address"))
                for key in ("slot_boot", "soundtap_args", "stock_args", "mtdids", "mtdparts",
                            # SelectBootSlot without markers: no rollback, no count.
                            "altbootcmd", "bootlimit", "upgrade_available", "bootcount"):
                    self.assertNotIn(key, env, key)
                # The SoundTap markers off the args entries; stock's ubi.mtd=8,2048 stays.
                self.assertEqual(env["nand_args"], "setenv bootargs console=${console} ubi.mtd=8,2048")
                self.assertEqual(env["nand_args2"], "setenv bootargs console=${console} ubi.mtd=9,2048")

    def test_bose_from_either_slot(self) -> None:
        """The way back to Bose works from either booting slot."""
        r = self.bose("st10", installed="run nand_boot2;")
        self.assertEqual(r["events"], [e.replace("Pong", "Ping") for e in self.BOSE_ORDER])
        self.assertEqual(r["bootcmd"], "run nand_boot;")

    def test_every_power_cut_leaves_a_bootable_board(self) -> None:
        """Every power cut on the way back leaves a bootable board.

        Every event of the way back cut short, and every point between two events: the
        board boots a whole slot through a chain of one maker. The one exception is the last
        write, Bose's recipes into the 8 KiB copy, which /api/firmware has as well: until that
        block is written again Bose's U-Boot runs on its built-in environment.
        """
        for booting, installed in (("Ping", "run nand_boot;"), ("Pong", "run nand_boot2;")):
            other = {"Ping": "Pong", "Pong": "Ping"}[booting]
            events = self.bose("st10", installed=installed)["events"]
            for current in "PR":
                for cut in range(len(events) + 1):
                    for torn in (False, True) if cut < len(events) else (False,):
                        with self.subTest(booting=booting, current=current, cut=cut, torn=torn):
                            got = simulate(events, cut, torn=torn, before=Before(booting, current))
                            if torn and events[cut] == f"mtd5:{other}:stock":
                                self.assertEqual(got, "Bose's U-Boot on its built-in environment")
                                continue
                            self.assertRegex(got, rf"^(ours:soundtap:{booting}|ours:default:{booting}|"
                                                  rf"ours:bose:{other}|bose:bose:{other})$", events[:cut + 1])
                self.assertEqual(simulate(events, len(events), torn=False, before=Before(booting, current)), f"bose:bose:{other}")

    def test_every_power_cut_of_a_soundtap_install(self) -> None:
        """Every power cut of a SoundTap install leaves a bootable SoundTap slot."""
        events = self.run_probe("st10", "3630", held=True, installed="run nand_boot;", variant="rhino")["events"]
        for cut in range(len(events) + 1):
            for torn in (False, True) if cut < len(events) else (False,):
                got = simulate(events, cut, torn=torn, before=Before(package="soundtap"))
                self.assertIn(got, ("ours:soundtap:Ping", "ours:soundtap:Pong"), (cut, torn))

    def test_a_failure_at_each_step(self) -> None:
        """A failure at each step of the way back stops it where it is.

        A step that fails stops the way back where it is: the board then boots the old
        slot until the new one is selected, the new one under our U-Boot until Bose's chain
        is whole, and the speaker restarts once Bose's chain is written.
        """
        cases = {
            "kernel_write": ("kernel:Pong:torn", "run nand_boot;", "ours:soundtap:Ping", 0),
            "rt_write": ("rootfs:Pong:torn", "run nand_boot;", "ours:soundtap:Ping", 0),
            "capture": ("rootfs:Pong", "run nand_boot;", "ours:soundtap:Ping", 0),
            "digest2": ("rootfs:Pong", "run nand_boot;", "ours:soundtap:Ping", 0),
            "boot_ok": ("rootfs:Pong", "run nand_boot;", "ours:soundtap:Ping", 0),
            "save1": ("save:torn", "run nand_boot2;", "ours:soundtap:Ping", 1),   # reset: NAND boots
            "save2": ("save:torn", "run nand_boot2;", "ours:bose:Pong", 0),
            "legacy1": ("mtd5:torn", "run nand_boot2;", "ours:bose:Pong", 0),
            "redund_erase": ("redund:torn", "run nand_boot2;", "ours:bose:Pong", 0),
            "put:uboot": ("uboot:torn", "run nand_boot2;", "ours:bose:Pong", 0),
            "put:spl2": ("spl2:torn", "run nand_boot2;", "ours:bose:Pong", 0),
            "put:spl0": ("spl0:torn", "run nand_boot2;", "bose:bose:Pong", 0),
            "put:spare": ("spare:torn", "run nand_boot2;", "bose:bose:Pong", 0),
            "verify": ("uboot", "run nand_boot2;", "ours:bose:Pong", 0),
            "legacy2": ("mtd5:torn", "run nand_boot2;", "Bose's U-Boot on its built-in environment", 1),
        }
        for step, (last, bootcmd, boots, resets) in cases.items():
            with self.subTest(step=step):
                r = self.bose("st10", fail_at=step)
                self.assertEqual(r["events"][-1 - resets], last, r["events"])
                self.assertEqual((r["bootcmd"], int(r["resets"])), (bootcmd, resets))
                self.assertEqual(simulate(r["events"], len(r["events"]), torn=False), boots)
                self.assertEqual(r["lamp"], "refused" if step == "boot_ok" else "fail")
                self.assertNotIn("soundtap.recovery=0", r["cmdline"])

    def test_bootloaders_that_hold_bose_already_are_left_alone(self) -> None:
        """Boot chain copies that hold Bose's bytes already are not written."""
        r = self.bose("wla", same=("uboot", "spl1", "spl2", "spl3", "spl0", "spare"))
        self.assertEqual(r["events"], [e for e in self.BOSE_ORDER if e[:3] not in ("ubo", "spl", "spa")])
        r = self.bose("st20", same=("spl1", "spare"))
        self.assertEqual([e for e in r["events"] if e[:3] in ("ubo", "spl", "spa")], ["uboot", "spl2", "spl3", "spl0"])

    def test_bose_on_every_board(self) -> None:
        """Every board our U-Boot knows goes back to Bose with its own stock package.

        Every board our U-Boot knows goes back to Bose with its own stock package, the ST300
        and the preview boards included: board, variant_raw, manufacturing name, package. The
        Lifestyle 650 has no gesture in this U-Boot and is held to its tag by UsbRecoveryImage.
        """
        for board, raw, mfg, pkg, b in (
                ("st10", "3630", None, "bose-sm2", "rhino"), ("wla", "2518", None, "bose-sm2", "binky"),
                ("st20", "", None, "bose-st20", "spotty"), ("st300", "3669", None, "bose-st300", "ginger"),
                ("st10", "", "spotty", "bose-sm2", "spotty3"), ("st10", "", "mojo", "bose-sm2", "mojo3"),
                ("st10", "", "burns", "bose-burns", "burns"), ("st10", "", "lisa", "bose-triode-sm2", "lisa2"),
                ("st10", "", "lisa", "bose-nelson-sm2", "lisa2"), ("st10", "", "lisa", "bose-marconi-sm2", "lisa2"),
                ("st20", "2500", None, "bose-st20", "mojo"), ("st20", "3000", None, "bose-st20", "taigan"),
                ("st20", "500", None, "bose-wst-scm", "lisa"), ("st20", "500", None, "bose-triode-scm", "lisa"),
                ("st20", "500", None, "bose-marconi-scm", "lisa"),
                ("st20", "500", None, "bose-st20", "lisa")):
            with self.subTest(board=b, pkg=pkg):
                tag = "@scm/lisa" if (b, pkg) == ("lisa", "bose-st20") else None  # lisa.scm: the ST20's chain
                r = self.run_probe(board, raw, held=True, installed="run nand_boot;", pkg=pkg, mfg=mfg, bose_vars=True, tag=tag)
                self.assertEqual(r["env"]["rt_b"], b)
                self.assertEqual(r["events"], self.BOSE_ORDER, r["cmdline"])
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=0"), r["cmdline"])
                # Our -2 on the boards whose UART0 is a link goes back to Bose's 1.
                self.assertEqual(r["bootdelay"], "1" if b in ("ginger", "lisa", "lisa2") else "unset")

    def test_bose_for_another_board_writes_nothing(self) -> None:
        """Bose's package for another board writes nothing.

        The tag decides: another module, another variant, a chain that is no stock family.
        """
        for board, raw, mfg, pkg, tag in (
                ("st10", "3630", None, "bose-st20", None),                  # an scm package on the sm2
                ("st20", "", None, "bose-sm2", None),                       # and the other way round
                ("st10", "", "lisa", "bose-wst-scm", None),                 # @scm/lisa on the sm2 lisa
                ("st20", "500", None, "bose-triode-sm2", None),             # @sm2/lisa on the scm lisa
                ("st10", "3630", None, "bose-burns", None),                 # the SA-5's on an ST10
                ("st10", "", "burns", "bose-sm2", None),                    # the shared one on the SA-5
                ("wla", "2518", None, "bose-st300", None),                  # the ST300's on a Wireless Link
                ("st300", "3669", None, "bose-sm2", None),                  # and the shared one on the ST300
                ("st10", "", "lisa", "bose-sm2", None),                     # no lisa in spotty+mojo+binky
                ("st20", "3000", None, "bose-st20", "@scm/spotty+mojo"),    # no taigan in the tag
                ("st10", "3630", None, "bose-sm2", "@scm/spotty+mojo+binky"),  # wrong module
                ("st10", "3630", None, "bose-sm2", "@sm2/spotty+mojo+binkyx"),  # no prefix matches
                ("st10", "3630", None, "bose-sm2", ""),                     # no tag at all
                ("st10", "3630", None, "bose-unknown", None)):              # not a stock family
            with self.subTest(board=board, pkg=pkg, mfg=mfg, tag=tag):
                r = self.run_probe(board, raw, held=True, installed="run nand_boot;", pkg=pkg, mfg=mfg, bose_vars=True, tag=tag)
                self.assertEqual((r["events"], r["saves"], r["bootcmd"], r["lamp"]), ([], "0", "run nand_boot;", "refused"))
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=-8"), r["cmdline"])

    def test_bose_refused_before_anything_is_written(self) -> None:
        """A Bose package that fails a preflight check is refused before any write.

        A kernel that is not Bose's, a ubi.img larger than the slot,
        an SPL this U-Boot does not read as the ROM does, a bad environment block, a copy
        without room, an environment too large for 8 KiB.
        """
        for step, rc in (("kernel_hdr", -8), ("big", -8), ("spl_read", -5), ("env_bad", -5), ("fits", -28),
                         ("env_fit", -27)):
            with self.subTest(step=step):
                r = self.bose("st10", fail_at=step)
                self.assertEqual((r["events"], r["saves"], r["bootcmd"]), ([], "0", "run nand_boot;"))
                self.assertTrue(r["cmdline"].endswith(f" soundtap.recovery={rc}"), r["cmdline"])

    def test_bose_over_a_rebuilt_environment_keeps_our_recipes(self) -> None:
        """Bose over a rebuilt environment keeps our recipes and drops the rollback.

        Without Bose's own entries its recipes would read variables that are not there;
        ours stay, which Bose's U-Boot runs too. The rollback still goes.
        """
        r = self.bose("st10", bose_vars=False)
        self.assertEqual(r["events"][-2:], ["mtd5:Pong:ours", "reset"])
        self.assertIn("slot_boot", r["env"]["nand_boot"])
        for key in ("altbootcmd", "bootlimit", "upgrade_available", "bootcount"):
            self.assertNotIn(key, r["env"], key)
        # Erased: our recipes go in for the board, as for a SoundTap package.
        r = self.bose("wla", installed=None, bose_vars=False)
        self.assertEqual(r["env"]["slot_boot"], dict(e.decode().split("=", 1) for e in WLA_ENV.recipes(b"binky"))["slot_boot"])

    def test_variant_from_the_manufacturing_partition(self) -> None:
        """The manufacturing partition's product name decides the variant.

        The product name in the manufacturing partition decides, whatever the ADC read:
        an ST300 whose variant_raw looks like a Wireless Link's, an ST10 without a sample.
        """
        self.assertEqual(self.run_probe("st300", "2518", held=True)["rt_v"], "ginger")
        self.assertEqual(self.run_probe("st10", "", held=True)["rt_v"], "rhino")
        # An unknown or unreadable field falls back to variant_raw, as before.
        self.assertEqual(self.run_probe("st10", "3630", held=True, mfg="lisa")["rt_v"], "rhino")
        self.assertEqual(self.run_probe("wla", "2518", held=True, fail_at="mfg")["rt_v"], "binky")
        # Only the ST300 turns autoboot keys off: its UART0 carries the soundbar link.
        for board, (raw, _) in self.BOARDS.items():
            self.assertEqual(self.run_probe(board, raw, held=False)["bootdelay"], "-2" if board == "st300" else "unset")
        # The ST20 is told by SYSBOOT and never reads it.
        self.assertEqual(self.run_probe("st20", "", held=True, mfg="ginger")["rt_v"], "spotty")

    def test_variant_from_the_scm_resistor(self) -> None:
        """On the SCM the variant resistor's ranges pick the board.

        On the SCM, stock's decimal ranges of the variant resistor: the ST20's measured 1612
        and an ST30 Series I's count. The SM2's Series III say spotty or mojo in the
        manufacturing partition and boot the Series III tree on the ST10's kernel.
        """
        self.assertEqual(self.run_probe("st20", "1612", held=False)["rt_v"], "spotty")
        r = self.run_probe("st20", "2500", held=False)
        self.assertEqual((r["rt_v"], r["env"]["rt_b"], r["env"]["rt_dtb"], r["env"]["rt_kernel"]),
                         ("spotty", "mojo", "spotty_scm.dtb", "uImage-spotty"))
        for mfg, v in (("spotty", "spotty3"), ("mojo", "mojo3")):
            r = self.run_probe("st10", "", held=False, mfg=mfg)
            self.assertEqual((r["rt_v"], r["env"]["rt_b"], r["env"]["rt_dtb"], r["env"]["rt_kernel"]),
                             ("rhino", v, "spotty3_sm2.dtb", "uImage-rhino"))

    def test_the_scm_board_from_the_saved_environment(self) -> None:
        """On the SCM the board comes from the variant its installer saved.

        Nothing samples the variant resistor on the SCM in our U-Boot (the owner's ST20 has no
        variant_raw saved), so the board comes from the variant= its installer saved in
        soundtap_args: an SCM board only, spotty for anything else.
        """
        for variant, want in (("mojo", "mojo"), ("taigan", "taigan"), ("lisa", "lisa"), ("spotty", "spotty"),
                              ("binky", "spotty"), ("lisa2", "spotty"), ("rhino", "spotty"), ("mo", "spotty"),
                              ("mojox", "spotty"), ("", "spotty")):
            with self.subTest(variant=variant):
                r = self.run_probe("st20", "", held=False, installed="run nand_boot;", variant=variant)
                self.assertEqual((r["env"]["rt_b"], r["env"]["rt_m"]), (want, "scm"))
        # Erased: no soundtap_args, spotty.
        self.assertEqual(self.run_probe("st20", "", held=False)["env"]["rt_b"], "spotty")
        # A sampled count wins over what was saved, and one out of every range stays spotty.
        for raw, want in (("2500", "mojo"), ("500", "lisa"), ("1612", "spotty"), ("50", "spotty")):
            with self.subTest(raw=raw):
                r = self.run_probe("st20", raw, held=False, installed="run nand_boot;", variant="taigan")
                self.assertEqual(r["env"]["rt_b"], want)
        # The adapter then takes its own gesture and its own Bose package, not the ST20's.
        r = self.run_probe("st20", "", held=True, installed="run nand_boot;", variant="lisa", pkg="bose-wst-scm", bose_vars=True)
        self.assertEqual((r["env"]["rt_b"], r["events"][-1], r["bootdelay"]), ("lisa", "reset", "1"))
        r = self.run_probe("st20", "", held=True, installed="run nand_boot;", variant="lisa", pkg="bose-st20", bose_vars=True)
        self.assertEqual(r["events"], [])

    def test_the_sa5_button_installs(self) -> None:
        """The SA-5's one button held at power-on installs.

        The SA-5's one button (stock's bit 9) held at power-on, two reads 50 ms apart.
        """
        r = self.run_probe("st10", "", held=True, installed="run nand_boot2;", mfg="burns")
        self.assertEqual((r["env"]["rt_b"], r["written"], r["bootcmd"]), ("burns", "Ping", "run nand_boot;"))
        r = self.run_probe("st10", "", held=False, installed="run nand_boot2;", mfg="burns")
        self.assertEqual(r["written"], "none")

    def test_the_keypad_chord_installs_on_every_keypad_board(self) -> None:
        """The keypad chord installs on every board with the 3x3 keypad.

        Volume Down + Preset 4, as stock's 0x108 on every board with the 3x3 keypad.
        """
        for board, raw, mfg in (("st20", "2500", ""), ("st10", "", "spotty"), ("st10", "", "mojo")):
            with self.subTest(board=board, variant_raw=raw, mfg=mfg):
                r = self.run_probe(board, raw, held=True, installed="run nand_boot2;", mfg=mfg)
                self.assertEqual((r["written"], r["bootcmd"]), ("Ping", "run nand_boot;"))



# The package checks (rt_scan, rt_classify, rt_check, rt_kernel_ok, rt_boot_ok) compiled from the
# patch over a real CRC-32 and an Update.stu held in a file: argv[1] the file, argv[2] the board
# (rt_b), argv[3] the eraseblock size, argv[4] its module (rt_m). It prints each check's result and what was found.
IMG_PROBE = r"""
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define loff_t long long
typedef unsigned char u8;
typedef unsigned int u32;
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#define min_t(t, a, b) ((t)(a) < (t)(b) ? (t)(a) : (t)(b))
#define max_t(t, a, b) ((t)(a) > (t)(b) ? (t)(a) : (t)(b))
#undef EBADMSG
#define EBADMSG 74
static u32 crc32(u32 crc, const u8 *p, u32 n)
{
	crc = ~crc;
	while (n--) {
		crc ^= *p++;
		for (int k = 0; k < 8; k++)
			crc = crc >> 1 ^ (0xedb88320 & -(crc & 1));
	}
	return ~crc;
}
static u32 get_unaligned_be32(const void *p) { const u8 *b = p; return (u32)b[0] << 24 | b[1] << 16 | b[2] << 8 | b[3]; }
static u32 get_unaligned_le32(const void *p) { const u8 *b = p; return (u32)b[3] << 24 | b[2] << 16 | b[1] << 8 | b[0]; }
static void put_unaligned_be32(u32 v, void *p) { u8 *b = p; b[0] = v >> 24; b[1] = v >> 16; b[2] = v >> 8; b[3] = v; }
typedef struct { int n; } sha256_context;
static void sha256_update(sha256_context *c, const u8 *b, u32 n) { (void)c; (void)b; (void)n; }
static const char *board, *module;
static const char *env_get(const char *k) { return !strcmp(k, "rt_b") ? board : !strcmp(k, "rt_m") ? module : NULL; }
static FILE *stick;
static int rt_read(void *buf, loff_t off, loff_t len)
{
	return fseek(stick, off, SEEK_SET) || fread(buf, 1, len, stick) != (size_t)len ? -EIO : 0;
}
DEFINES
STRUCT
FUNCTIONS
int main(int argc, char **argv)
{
	static struct rt_pkg pkg;
	static u8 buf[RT_CHUNK], mlo[RT_SPL_SIZE], ub[RT_UBOOT_MAX];
	sha256_context ctx;
	loff_t file;
	int scan, classify = 1, check = 1, kernel = 1, boot = 1;

	if (argc != 5)
		return 2;
	stick = fopen(argv[1], "rb");
	board = argv[2];
	module = argv[4];
	fseek(stick, 0, SEEK_END);
	file = ftell(stick);
	scan = rt_scan(buf, file, &pkg);
	if (!scan)
		classify = rt_classify(&pkg, strtoul(argv[3], NULL, 0));
	if (!scan && !classify)
		check = rt_check(buf, &pkg, &ctx, file);
	if (!scan && !classify && pkg.bose) {
		kernel = rt_kernel_ok(buf, pkg.kernel);
		boot = rt_read(mlo, pkg.mlo->off, pkg.mlo->size) || rt_read(ub, pkg.uboot->off, pkg.uboot->size) ? -EIO :
			rt_boot_ok(mlo, pkg.mlo->size, ub, pkg.uboot->size);
	}
	printf("scan=%d|classify=%d|check=%d|kernel=%d|boot=%d|modules=%d|bose=%d|rootfs=%s|body=%u|tag=%s\n", scan,
		classify, check, kernel, boot, pkg.n, pkg.bose, pkg.rootfs ? pkg.rootfs->name : "",
		pkg.rootfs ? pkg.rootfs->body : 0, pkg.rootfs ? pkg.rootfs->tag : "");
	return 0;
}
"""


def stu_file(modules: list[tuple[str, bytes]], magic: bytes = b"BOSE", tag: str = "@sm2/spotty+mojo+binky") -> bytearray:
    """Build an Update.stu as stock's packager lays one out (internal/stu).

    Each module's header
    starts 0x14 bytes before the end of the previous payload, which those bytes belong to, and
    carries the version at 0xa8, ubi.img's naming the module and variants it is for.
    """
    data = bytearray()
    for i, (name, payload) in enumerate(modules):
        h = bytearray(0x138)
        if i:
            h[:0x14] = data[-0x14:]
        else:
            h[4:8] = magic
        h[0x18:0x18 + len(name)] = name.encode()
        version = b"27.0.6.46330.5043500" + (tag.encode() if name == "ubi.img" else b"")
        h[0xa8:0xa8 + len(version)] = version
        h[0x98:0x9c] = len(payload).to_bytes(4, "big")
        h[0x12c:0x130] = zlib.crc32(payload).to_bytes(4, "big")
        h[0x134:0x138] = zlib.crc32(bytes(h[0x14:0x134])).to_bytes(4, "big")
        data += h if not i else h[0x14:]
        data += payload
    data[8:12] = len(data).to_bytes(4, "big")
    data[0x10:0x14] = zlib.crc32(bytes(data[:0x10])).to_bytes(4, "big")
    return data


def uimage(name: str, body: bytes, magic: int = 0x27051956) -> bytes:
    """Build a legacy uImage of body named name."""
    h = bytearray(64)
    h[0:4] = magic.to_bytes(4, "big")
    h[12:16] = len(body).to_bytes(4, "big")
    h[16:24] = (0x80008000).to_bytes(4, "big") * 2
    h[24:28] = zlib.crc32(body).to_bytes(4, "big")
    h[28:32] = bytes((5, 2, 2, 0))
    h[32:32 + len(name)] = name.encode()
    h[4:8] = zlib.crc32(bytes(h)).to_bytes(4, "big")
    return bytes(h) + body


def spl(size: int = 0x8000, first: int = 0xeb000042) -> bytes:
    """Build a TI GP image as the ROM loads it.

    A NAND configuration header, then size and load address, then a branch to reset.
    """
    b = bytearray(0x200 + size)
    b[0:4] = (0x40).to_bytes(4, "little")
    b[0x200:0x204] = (size - 8).to_bytes(4, "little")
    b[0x204:0x208] = (0x402f0400).to_bytes(4, "little")
    b[0x208:0x20c] = first.to_bytes(4, "little")
    return bytes(b)


def bose_modules(chain: list[str], **override: bytes) -> list[tuple[str, bytes]]:
    """Return Bose-like payloads for each module of chain, override replacing some."""
    payloads = {"linuxPatched": uimage("Linub-3.14 vendor", bytes(range(256)) * 300),
                "ubi.img": bytes((i * 7) & 0xff for i in range(2 * 0x20000)),
                "MLO": spl(), "u-boot.img": uimage("U-Boot vendor", b"\x11" * 0x30000)}
    payloads.update(override)
    return [(name, payloads.get(name, name.encode() * 3)) for name in chain]


@unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
class UsbRecoveryImage(unittest.TestCase):
    """The package checks refuse a damaged file or another board's.

    The stick takes no signature, so the CRCs are what stand between a damaged or wrong
    file and NAND: every one of them is checked, on files laid out as stock's packager lays
    them out, and only Bose's chain for the board's own model is taken as Bose's.
    """

    probe: ClassVar[str] = ""

    @classmethod
    def setUpClass(cls) -> None:
        """Compile the package checks from the patch once for the class."""
        functions = "\n".join([
            patch_block(PATCH, "static const char *const rt_bose_chains[][RT_STU_MODULES] = {"),
            patch_block(PATCH, "static const struct rt_bose {"),
            patch_function(PATCH, "static int rt_scan(u8 *hdr, loff_t file, struct rt_pkg *pkg)"),
            patch_function(PATCH, "static struct rt_module *rt_module_named(struct rt_pkg *pkg, const char *name)"),
            patch_function(PATCH, "static bool rt_tag_names("),
            patch_function(PATCH, "static int rt_classify(struct rt_pkg *pkg, u32 erasesize)"),
            patch_function(PATCH, "static int rt_check("),
            patch_function(PATCH, "static int rt_kernel_ok("),
            patch_function(PATCH, "static int rt_boot_ok("),
        ])
        src = IMG_PROBE.replace("DEFINES", patch_defines()).replace("STRUCT", patch_structs()).replace("FUNCTIONS", functions)
        cls.dir = cc(src, opt="-O1")
        cls.probe = f"{cls.dir}/probe"

    @classmethod
    def tearDownClass(cls) -> None:
        """Remove the compiled probe."""
        shutil.rmtree(cls.dir)

    MODULE: ClassVar[dict[str, str]] = {"spotty": "scm", "mojo": "scm", "taigan": "scm", "lisa": "scm"}

    def check(self, data: bytes, board: str = "rhino", erase: int = 0x20000) -> dict[str, str]:
        """Run the package checks on data as board would; return each result."""
        path = Path(self.dir, "Update.stu")
        path.write_bytes(bytes(data))
        out = subprocess.run([self.probe, str(path), board, hex(erase), self.MODULE.get(board, "sm2")],
                             check=True, capture_output=True, text=True).stdout
        return dict(kv.split("=", 1) for kv in out.strip().splitlines()[-1].split("|"))

    def test_bose_for_this_board(self) -> None:
        """Bose's chain and tag for the board pass every check."""
        for board, chain in (("rhino", "bose-sm2"), ("binky", "bose-sm2"), ("spotty3", "bose-sm2"), ("mojo3", "bose-sm2"),
                             ("spotty", "bose-st20"), ("mojo", "bose-st20"), ("taigan", "bose-st20"),
                             ("ginger", "bose-st300"), ("bardeen", "bose-bardeen"), ("burns", "bose-burns"),
                             ("lisa2", "bose-nelson-sm2"), ("lisa2", "bose-triode-sm2"), ("lisa2", "bose-marconi-sm2"),
                             ("lisa", "bose-wst-scm"), ("lisa", "bose-triode-scm"), ("lisa", "bose-marconi-scm")):
            with self.subTest(board=board, chain=chain):
                r = self.check(stu_file(bose_modules(CHAINS[chain]), tag=TAGS[chain]), board)
                self.assertEqual((r["scan"], r["classify"], r["check"], r["kernel"], r["boot"], r["bose"], r["rootfs"]),
                                 ("0", "0", "0", "0", "0", "1", "ubi.img"))
                self.assertEqual((int(r["modules"]), r["tag"]), (len(CHAINS[chain]), "27.0.6.46330.5043500" + TAGS[chain]))
        # lisa.scm is the ST20's chain with the adapter's tag.
        self.assertEqual(self.check(stu_file(bose_modules(CHAINS["bose-st20"]), tag="@scm/lisa"), "lisa")["classify"], "0")

    def test_bose_for_another_board(self) -> None:
        """Bose's chain or tag for another board is refused."""
        for board, chain, tag in (("spotty", "bose-sm2", None), ("rhino", "bose-st20", None), ("ginger", "bose-sm2", None),
                                  ("binky", "bose-st300", None), ("burns", "bose-sm2", None), ("rhino", "bose-burns", None),
                                  ("lisa", "bose-triode-sm2", None), ("lisa2", "bose-wst-scm", None),
                                  ("bardeen", "bose-st300", None), ("ginger", "bose-st300", "@sm2/bardeen"),
                                  ("taigan", "bose-st20", "@scm/spotty+mojo"), ("rhino", "bose-sm2", "@scm/spotty+mojo+binky"),
                                  ("rhino", "bose-sm2", "@sm2/spotty+mojo+binkies"), ("rhino", "bose-sm2", "sm2/binky"),
                                  ("rhino", "bose-unknown", None), ("nosuchboard", "bose-sm2", None)):
            with self.subTest(board=board, chain=chain, tag=tag):
                r = self.check(stu_file(bose_modules(CHAINS[chain]), tag=tag if tag is not None else TAGS[chain]), board)
                self.assertEqual(r["classify"], "-8", r)

    def test_a_soundtap_package_on_any_board(self) -> None:
        """A SoundTap package passes the checks on any board."""
        modules = [(n, bytes(3 * 0x20000) if n == "soundtap-rootfs" else n.encode() * 3) for n in CHAINS["soundtap"]]
        for board in ("rhino", "binky", "spotty", "ginger", "burns"):
            r = self.check(stu_file(modules), board)
            self.assertEqual((r["scan"], r["classify"], r["check"], r["bose"], r["rootfs"], r["body"]),
                             ("0", "0", "0", "0", "soundtap-rootfs", str(3 * 0x20000)))

    def test_the_rootfs_body_is_whole_eraseblocks(self) -> None:
        """The rootfs body taken is whole eraseblocks, with or without the overlap.

        ubi.img counted with its overlap (stock's), or without it (ours): whichever is whole blocks.
        """
        for size, body in ((2 * 0x20000, 2 * 0x20000), (2 * 0x20000 + 0x14, 2 * 0x20000), (0x30000, 0)):
            modules = bose_modules(CHAINS["bose-sm2"], **{"ubi.img": bytes(size)})
            r = self.check(stu_file(modules))
            self.assertEqual(r["classify"], "0" if body else "-22", size)
            if body:
                self.assertEqual(r["body"], str(body))

    def test_damage_anywhere_is_refused(self) -> None:
        """Damage anywhere the stick writes from is refused by a CRC."""
        good = stu_file(bose_modules(CHAINS["bose-sm2"]))
        self.assertEqual(self.check(good)["check"], "0")
        ubi = good.index(b"ubi.img") - 0x18 + 0x138 + 1000  # inside the ubi.img payload
        for where, step in ((ubi, "check"),                          # an image CRC
                            (good.index(b"MLO") + 2, "scan"),        # a module header
                            (0x09, "scan"),                          # the package size
                            (len(good) - 1, "check")):               # the last module's last byte
            with self.subTest(where=where):
                bad = bytearray(good)
                bad[where] ^= 0x40
                r = self.check(bad)
                self.assertEqual(r[step], "-74", r)
        # A module the stick does not write is not held to its image CRC: Bose's own packages
        # carry host firmware whose CRC field does not match (TRI_shelby_update.tri: 0xffffffff).
        for name in ("SCMReset_Update", "UbootUpdate"):
            bad = bytearray(good)
            bad[good.index(name.encode()) - 0x18 + 0x138 + 3] ^= 0x40
            self.assertEqual(self.check(bad)["check"], "0", name)
        tri = stu_file(bose_modules(CHAINS["bose-triode-sm2"]), tag="@sm2/lisa")
        tri[-1] ^= 1
        self.assertEqual(self.check(tri, "lisa2")["check"], "0")
        self.assertEqual(self.check(good + b"\0")["scan"], "-74")      # trailing data
        self.assertEqual(self.check(good[:-1])["scan"], "-74")         # cut short
        self.assertEqual(self.check(good[:100])["scan"], "-8")         # not even a header

    def test_not_a_stu(self) -> None:
        """A file without the STU magic is not a .stu."""
        self.assertEqual(self.check(stu_file(bose_modules(CHAINS["bose-sm2"]), magic=b"RTCH"))["scan"], "-8")
        self.assertEqual(self.check(b"\x89PNG" + bytes(0x200))["scan"], "-8")

    def test_bose_modules_that_do_not_fit(self) -> None:
        """Bose's kernel, SPL or U-Boot too large for its place is refused.

        ubi.img larger than the slot is refused in UsbRecoveryProbe ("big"); these must fit
        the kernel partition, an SPL block and the half of NAND.U-Boot a copy has.
        """
        for name, payload in (("linuxPatched", uimage("Linub", bytes(0x500000))), ("MLO", spl(0x20000)),
                              ("u-boot.img", uimage("U-Boot", bytes(0xc0000)))):
            with self.subTest(name=name):
                self.assertEqual(self.check(stu_file(bose_modules(CHAINS["bose-sm2"], **{name: payload})))["classify"], "-8")

    def test_only_bose_kernel_and_boot_loaders(self) -> None:
        """Only a Bose kernel and a real SPL and U-Boot are taken as Bose's."""
        for name, payload, step in (
                ("linuxPatched", uimage("Linux-6.12 SoundTap st10", b"k" * 1000), "kernel"),  # errNotStock
                ("linuxPatched", uimage("Linem-3.14", b"k" * 1000), "kernel"),               # marked empty
                ("linuxPatched", uimage("Linub", b"k", magic=0x12345678), "kernel"),
                ("MLO", spl(first=0xe3a00000), "boot"),                                     # no branch
                ("MLO", spl()[:0x100], "boot"),
                ("u-boot.img", uimage("U-Boot", b"u" * 1000)[:-1] + b"v", "boot")):         # data CRC
            with self.subTest(name=name, payload=payload[32:40]):
                r = self.check(stu_file(bose_modules(CHAINS["bose-sm2"], **{name: payload})))
                self.assertEqual(r[step], "-8", r)
        # Bose's own SPL starts with a bl, ours with a b: both are SPLs (ValidateAM335xSPL).
        self.assertEqual(self.check(stu_file(bose_modules(CHAINS["bose-sm2"], MLO=spl(first=0xea00000f))))["boot"], "0")


# The boot chain write (rt_boot_chain, rt_copy_holds, rt_copy_fits, rt_put) and the 8 KiB
# environment (rt_env_legacy), compiled from the patch over a fake NAND from NAND.SPL to
# NAND.U-Boot-Env. Each copy starts holding OURS's bytes (ours) or Bose's (listed in SAME); BAD
# lists factory-bad blocks, FAULTS {op, block, errno} ('e' erase, 'w' program, 'u' a read the
# ECC corrected as often as it calls due for a rewrite). Every erase and program is logged.
BOOT_PROBE = r"""
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define loff_t long long
typedef unsigned char u8;
typedef unsigned int u32;
typedef unsigned long long u64;
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#define min_t(t, a, b) ((t)(a) < (t)(b) ? (t)(a) : (t)(b))
#define MTD_FAIL_ADDR_UNKNOWN (-1LL)
#undef EUCLEAN
#define EUCLEAN 117
static void rt_kick(void) {}
static u32 crc32(u32 crc, const u8 *p, u32 n)
{
	crc = ~crc;
	while (n--) {
		crc ^= *p++;
		for (int k = 0; k < 8; k++)
			crc = crc >> 1 ^ (0xedb88320 & -(crc & 1));
	}
	return ~crc;
}
static void put_unaligned_le32(u32 v, void *p) { u8 *b = p; b[0] = v; b[1] = v >> 8; b[2] = v >> 16; b[3] = v >> 24; }
static u32 get_unaligned_le32(const void *p) { const u8 *b = p; return (u32)b[3] << 24 | b[2] << 16 | b[1] << 8 | b[0]; }
enum env_valid { ENV_INVALID, ENV_VALID, ENV_REDUND };
static struct { int env_valid; } gdata = { ENV_REDUND }, *gd = &gdata;
struct hsearch_data { int n; } env_htab;
static const char *exported = @EXPORTED@;
static long hexport_r(struct hsearch_data *h, char sep, int flag, char **resp, size_t size, int argc, char *const argv[])
{
	size_t n = strlen(exported) + 2;

	(void)h; (void)sep; (void)flag; (void)argc; (void)argv;
	if (size < n)
		return -1;
	memset(*resp, 0, size);
	memcpy(*resp, exported, n - 2);
	for (char *p = *resp; p < *resp + n; p++)
		if (*p == '|')
			*p = 0;
	return n;
}
struct mtd_info { u32 erasesize, writesize; };
struct erase_info { struct mtd_info *mtd; u64 addr, len; long long fail_addr; };
#define ERASE 0x20000
#define PAGE 0x800
#define NBLOCKS 20	/* NAND.SPL to NAND.U-Boot-Env */
static struct { bool bad; u8 *data; } nand[NBLOCKS];
struct fault { char op; int block; int err; };
static const struct fault faults[] = { @FAULTS@ {0, 0, 0} };
static const int bad[] = { @BAD@ -1 };
static char log_[4096];
static int fault(char op, loff_t ofs)
{
	for (const struct fault *f = faults; f->op; f++)
		if (f->op == op && f->block == ofs / ERASE)
			return f->err;
	return 0;
}
static int idx(loff_t ofs)
{
	if (ofs < 0 || ofs >= NBLOCKS * ERASE) {
		printf("access outside the boot chain at 0x%llx\n", ofs);
		exit(3);
	}
	return (int)(ofs / ERASE);
}
static int mtd_block_isbad(struct mtd_info *m, loff_t ofs) { (void)m; return nand[idx(ofs)].bad; }
static int mtd_block_markbad(struct mtd_info *m, loff_t ofs) { (void)m; (void)ofs; printf("marked a boot chain block\n"); exit(3); }
static int mtd_erase(struct mtd_info *m, struct erase_info *ei)
{
	int i = idx(ei->addr), err = fault('e', ei->addr);

	(void)m;
	ei->fail_addr = err == -EIO ? (long long)ei->addr : MTD_FAIL_ADDR_UNKNOWN;
	sprintf(log_ + strlen(log_), "e%d,", i);
	if (err)
		return err;
	memset(nand[i].data, 0xff, ERASE);
	return 0;
}
static int mtd_write(struct mtd_info *m, loff_t to, size_t len, size_t *ret, const u8 *buf)
{
	int i = idx(to), err = fault('w', to);

	(void)m;
	*ret = 0;
	if (to % PAGE == 0 && to % ERASE == 0)
		sprintf(log_ + strlen(log_), "w%d,", i);
	if (err)
		return err;
	for (size_t n = 0; n < len; n++)
		nand[i].data[to % ERASE + n] &= buf[n];
	*ret = len;
	return 0;
}
static int mtd_read(struct mtd_info *m, loff_t from, size_t len, size_t *ret, u8 *buf)
{
	int i = idx(from);

	(void)m;
	memcpy(buf, nand[i].data + from % ERASE, len);
	*ret = len;
	return fault('r', from) ? fault('r', from) : fault('u', from);
}
static int mtd_is_bitflip(int err) { return err == -EUCLEAN; }
@DEFINES@
@FUNCTIONS@
/* Bose's SPL and U-Boot, and ours: patterns that tell the four apart. */
static void image(u8 *p, u32 len, int seed) { for (u32 n = 0; n < len; n++) p[n] = (u8)(n * 13 + seed + n / 509); }
int main(void)
{
	struct mtd_info mtd = { ERASE, PAGE };
	static u8 mlo[RT_SPL_SIZE], ub[RT_UBOOT_MAX];
	const u32 mlo_len = @MLO_LEN@, ub_len = @UB_LEN@;
	const char *same = @SAME@;
	int rc = 0;

	for (int i = 0; i < NBLOCKS; i++) {
		nand[i].data = malloc(ERASE);
		memset(nand[i].data, 0x00, ERASE);
	}
	/* NAND.SPL holds our SPL as Linux wrote it: the ROM's header, then the image. */
	memset(nand[0].data, 0xff, ERASE);
	put_unaligned_le32(0x40, nand[0].data);
	put_unaligned_le32(@SPL_LOAD@, nand[0].data + 0x204);
	image(nand[0].data + 0x208, 0x6000, 3);
	for (const int *b = bad; *b >= 0; b++)
		nand[*b].bad = true;
	memset(mlo, 0xff, sizeof(mlo));
	memset(ub, 0xff, sizeof(ub));
	image(mlo, mlo_len, 1);
	image(ub, ub_len, 2);
	/* Copies listed in SAME hold Bose's already, written where the write would put them. */
	for (const struct rt_copy *c = rt_stock_copies; c < rt_stock_copies + ARRAY_SIZE(rt_stock_copies); c++) {
		char key[40];

		snprintf(key, sizeof(key), ",%s,", c->name);
		if (strstr(same, key)) {
			const u8 *d = c->uboot ? ub : mlo;
			u32 len = c->uboot ? ub_len : mlo_len;
			loff_t blk = c->start;

			for (u32 n = 0; n < len; blk += ERASE) {
				if (nand[idx(blk)].bad)
					continue;
				memset(nand[idx(blk)].data, 0xff, ERASE);
				memcpy(nand[idx(blk)].data, d + n, min_t(u32, ERASE, len - n));
				n += ERASE;
			}
		}
	}
	if (@SPLREAD@)
		rc = rt_spl_readable(&mtd);
	else if (@ENV@)
		rc = rt_env_legacy(&mtd, @DROP@);
	else
		rc = rt_boot_chain(&mtd, mlo, mlo_len, ub, ub_len);
	printf("rc=%d|log=%s|holds=", rc, log_);
	for (const struct rt_copy *c = rt_stock_copies; c < rt_stock_copies + ARRAY_SIZE(rt_stock_copies); c++)
		printf("%d", rt_copy_holds(&mtd, c, c->uboot ? ub : mlo, c->uboot ? ub_len : mlo_len, false));
	printf("|env_valid=%d|mtd5=", gd->env_valid);
	for (int n = 0; n < 0x40; n++)
		printf("%02x", nand[RT_ENV_OFFSET / ERASE].data[n]);
	printf("|mtd5_crc=%08x|redund_erased=%d\n", crc32(0, nand[RT_ENV_OFFSET / ERASE].data + 4, RT_ENV_LEGACY - 4),
		nand[RT_ENV_REDUND / ERASE].data[0] == 0xff && nand[RT_ENV_REDUND / ERASE].data[RT_ENV_SIZE - 1] == 0xff);
	return 0;
}
"""


class ChainOptions(TypedDict, total=False):
    """The fake NAND and the call of run_chain."""

    same: Sequence[str]                   # copies that hold Bose's bytes already
    bad: Sequence[int]                    # factory-bad blocks
    faults: Sequence[tuple[str, int, str]]  # (op, block, errno name)
    mlo_len: int
    ub_len: int
    env: bool                             # write the 8 KiB environment instead of the chain
    drop: bool                            # with env: erase the redundant copy too
    exported: str                         # the environment hexport_r returns, | between entries
    splread: bool                         # run rt_spl_readable instead
    spl_load: int                         # the load address in NAND.SPL's GP header


@unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
class UsbRecoveryBootChain(unittest.TestCase):
    """The boot chain write puts Bose's SPL and U-Boot over ours as WriteBootloaders does.

    Bose's SPL and U-Boot over ours as install.WriteBootloaders writes them: in
    stockBootSlots order, only the copies that differ (or read right only after as many ECC
    corrections as call for a rewrite), each in its own range, read back, nothing marked.
    """

    NAMES: ClassVar[list[str]] = ["NAND.U-Boot", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3",
                                  "NAND.SPL", "the spare NAND.U-Boot"]
    # The first block of each copy, in that order.
    FIRST: ClassVar[list[int]] = [4, 1, 2, 3, 0, 12]

    def run_chain(self, **options: Unpack[ChainOptions]) -> dict:
        """Run the boot chain write (or the 8 KiB environment, or the SPL read) on the fake NAND."""
        same, bad, faults = options.get("same", ()), options.get("bad", ()), options.get("faults", ())
        mlo_len, ub_len = options.get("mlo_len", 0x8200), options.get("ub_len", 0x5a000)
        env, drop = options.get("env", False), options.get("drop", True)
        exported = options.get("exported", "bootcmd=run nand_boot2;|nand_boot=x")
        splread, spl_load = options.get("splread", False), options.get("spl_load", 0x402f0400)
        functions = "\n".join([
            "\n".join(line[1:] for line in re.findall(r"^\+static u8 rt_page\[4096\];$", PATCH, re.MULTILINE)),
            patch_function(PATCH, "static bool rt_page_empty("),
            patch_function(PATCH, "static int rt_erase_err("),
            patch_function(PATCH, "static int rt_mark_bad("),
            patch_function(PATCH, "static int rt_write_block("),
            patch_function(PATCH, "static int rt_erase_rest("),
            patch_block(PATCH, "static const struct rt_copy {"),
            patch_function(PATCH, "static bool rt_copy_holds("),
            patch_function(PATCH, "static bool rt_copy_fits("),
            patch_function(PATCH, "static int rt_put("),
            patch_function(PATCH, "static int rt_boot_chain("),
            "\n".join(line[1:] for line in re.findall(r"^\+static u8 rt_env_img\[RT_ENV_LEGACY\];$", PATCH, re.MULTILINE)),
            patch_function(PATCH, "static int rt_env_export(int margin)"),
            patch_function(PATCH, "static int rt_env_legacy("),
            patch_function(PATCH, "static bool rt_spl_readable("),
        ])
        defines = patch_defines()
        fault_list = " ".join(f"{{'{op}', {block}, -{err}}}," for op, block, err in faults)
        src = (BOOT_PROBE.replace("@FUNCTIONS@", functions).replace("@DEFINES@", defines)
               .replace("@FAULTS@", fault_list).replace("@BAD@", "".join(f"{b}, " for b in bad))
               .replace("@MLO_LEN@", hex(mlo_len)).replace("@UB_LEN@", hex(ub_len))
               .replace("@SAME@", '",' + ",".join(same) + ',"').replace("@EXPORTED@", f'"{exported}"')
               .replace("@ENV@", str(int(env))).replace("@DROP@", "true" if drop else "false")
               .replace("@SPLREAD@", str(int(splread))).replace("@SPL_LOAD@", hex(spl_load)))
        d = cc(src, opt="-O1")
        try:
            out = subprocess.run([f"{d}/probe"], check=True, capture_output=True, text=True).stdout
        finally:
            shutil.rmtree(d)
        r = dict(kv.split("=", 1) for kv in out.splitlines()[-1].split("|"))
        r["out"] = out
        r["writes"] = [int(w[1:]) for w in r["log"].split(",") if w.startswith("w")]
        return r

    def copies_written(self, r: dict) -> list[str]:
        """Return which copies were programmed, by the first block each write began in, in order."""
        firsts = [b for i, b in enumerate(r["writes"]) if i == 0 or b not in r["writes"][:i]]
        return [self.NAMES[self.FIRST.index(b)] for b in firsts if b in self.FIRST]

    def test_the_whole_chain_in_stock_order(self) -> None:
        """The whole boot chain is written in stock order, each copy in its own range."""
        r = self.run_chain()
        self.assertEqual((r["rc"], r["holds"]), ("0", "111111"), r["out"])
        self.assertEqual(self.copies_written(r), self.NAMES)
        # Each copy is erased in its own range only: the first U-Boot in blocks 4-11, the spare
        # in 12-17; the environment blocks 18 and 19 are never erased.
        erased = [int(e[1:]) for e in r["log"].split(",") if e.startswith("e")]
        self.assertNotIn(18, erased)
        self.assertNotIn(19, erased)
        self.assertEqual(sorted(set(erased)), list(range(18)))

    def test_copies_that_hold_bose_are_left_alone(self) -> None:
        """Copies that hold Bose's bytes are left alone."""
        r = self.run_chain(same=self.NAMES)
        self.assertEqual((r["rc"], r["log"], r["holds"]), ("0", "", "111111"))
        r = self.run_chain(same=["NAND.SPL.backup2", "the spare NAND.U-Boot"])
        self.assertEqual(self.copies_written(r), ["NAND.U-Boot", "NAND.SPL.backup1", "NAND.SPL.backup3", "NAND.SPL"])
        self.assertEqual(r["holds"], "111111")

    def test_a_worn_copy_is_written_again(self) -> None:
        """A worn copy is written again.

        Reads right, but only after as many corrections as the NAND calls due (bootSlot.worn).
        """
        r = self.run_chain(same=self.NAMES, faults=[("u", 2, "EUCLEAN")])
        self.assertEqual((r["rc"], self.copies_written(r)), ("0", ["NAND.SPL.backup2"]))

    def test_bad_blocks_in_u_boot_are_skipped_in_its_own_half(self) -> None:
        """Bad blocks in NAND.U-Boot are skipped within the copy's own half."""
        r = self.run_chain(bad=(5, 13))
        self.assertEqual((r["rc"], r["holds"]), ("0", "111111"), r["out"])
        self.assertNotIn(5, r["writes"])
        self.assertNotIn(13, r["writes"])

    def test_a_failing_copy_stops_the_chain(self) -> None:
        """A copy that fails to program stops the chain.

        NAND.SPL.backup2 does not program: backup3, NAND.SPL and the spare stay ours, and
        nothing is marked bad (the probe exits if anything is).
        """
        r = self.run_chain(faults=[("w", 2, "EIO")])
        self.assertEqual(r["rc"], "-5")
        self.assertEqual(self.copies_written(r), ["NAND.U-Boot", "NAND.SPL.backup1", "NAND.SPL.backup2"])
        self.assertEqual(r["holds"][:2] + r["holds"][3:], "11000")
        # A bad SPL block has no room for the SPL at all.
        self.assertEqual(self.run_chain(bad=(0,))["rc"], "-28")

    def test_the_spl_reads_as_the_rom_reads_it(self) -> None:
        """The SPL in place must read as the ROM reads it.

        Bose's chain is written only by a U-Boot that reads the SPL the ROM booted without an
        ECC error: its layout is then the one the ROM reads.
        """
        self.assertEqual(self.run_chain(splread=True)["rc"], "1")
        self.assertEqual(self.run_chain(splread=True, faults=[("u", 0, "EUCLEAN")])["rc"], "1")
        self.assertEqual(self.run_chain(splread=True, faults=[("r", 0, "EBADMSG")])["rc"], "0")
        self.assertEqual(self.run_chain(splread=True, spl_load=0x80800000)["rc"], "0")

    def test_the_8k_environment(self) -> None:
        """The 8 KiB environment is written with its CRC, within its size."""
        r = self.run_chain(env=True)
        self.assertEqual((r["rc"], r["redund_erased"], r["env_valid"]), ("0", "1", "1"), r["out"])
        image = bytes.fromhex(r["mtd5"])
        self.assertEqual(int.from_bytes(image[:4], "little"), int(r["mtd5_crc"], 16))
        self.assertEqual(image[4:].split(b"\0\0")[0], b"bootcmd=run nand_boot2;\0nand_boot=x")
        # The way back's last write leaves the redundant copy as it is (already erased).
        r = self.run_chain(env=True, drop=False)
        self.assertEqual((r["rc"], r["redund_erased"]), ("0", "0"))
        # An environment larger than 8 KiB writes nothing.
        r = self.run_chain(env=True, exported="x=" + "y" * 0x2000)
        self.assertEqual((r["rc"], r["log"]), ("-27", ""))
        # A redundant block that is bad holds no copy: left alone.
        r = self.run_chain(env=True, bad=(18,))
        self.assertEqual((r["rc"], r["log"]), ("0", "e19,w19,"))


# Pass two's write (rt_write, rt_write_block, rt_erase_rest, rt_mark_bad), compiled on the host
# from the patch over a fake NAND that holds the two kernel partitions and the two rootfs slots. FAULTS lists blocks of the slot being
# written that fail: {op, block, first failing byte of it, errno}. 'e' erase (an -EIO erase
# names the block in fail_addr, as nand_erase_nand does for a FAIL status), 'w' program,
# 'r' read, 'c' a program that reports success and keeps another byte, 'm' the bad-block mark.
# Every other block holds the old image until it is erased; programming one that was not erased
# aborts the probe.
WRITE_PROBE = r"""
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define loff_t long long
typedef unsigned char u8;
typedef unsigned int u32;
typedef unsigned long long u64;
#define min_t(t, a, b) ((t)(a) < (t)(b) ? (t)(a) : (t)(b))
#define roundup(x, y) ((((x) + ((y) - 1)) / (y)) * (y))
#define MTD_FAIL_ADDR_UNKNOWN (-1LL)
#undef EUCLEAN
#define EUCLEAN 117
#undef EBADMSG
#define EBADMSG 74
@DEFINES@
@STRUCT@
typedef struct { int n; } sha256_context;
static void sha256_update(sha256_context *c, const u8 *b, u32 n) { (void)c; (void)b; (void)n; }
static void rt_kick(void) {}
struct mtd_info { u32 erasesize, writesize; };
struct erase_info { struct mtd_info *mtd; u64 addr, len; long long fail_addr; };

#define ERASE 0x20000
#define PAGE 0x800
#define NBLOCKS ((RT_PONG_ROOTFS + RT_SLOT_SIZE - RT_PING_KERNEL) / ERASE)
static struct { bool bad, old; u8 *data; } nand[NBLOCKS];	/* data NULL: erased */
struct fault { char op; int block; u32 from; int err; };
static const struct fault faults[] = { @FAULTS@ {0, 0, 0, 0} };
static const bool write_protected = @WP@;
static const loff_t slot = @SLOT@, len = @LEN@;
static int marks[64], nmarks;

static int idx(loff_t ofs)
{
	if (ofs < RT_PING_KERNEL || ofs >= RT_PONG_ROOTFS + RT_SLOT_SIZE) {
		printf("access outside the kernel partitions and rootfs slots at 0x%llx\n", ofs);
		exit(3);
	}
	return (int)((ofs - RT_PING_KERNEL) / ERASE);
}
static int fault(char op, loff_t ofs)
{
	for (const struct fault *f = faults; f->op; f++)
		if (f->op == op && slot + (loff_t)f->block * ERASE == ofs - ofs % ERASE && ofs % ERASE >= f->from)
			return f->err;
	return 0;
}
static int mtd_block_isbad(struct mtd_info *m, loff_t ofs) { (void)m; return nand[idx(ofs)].bad; }
static int mtd_block_markbad(struct mtd_info *m, loff_t ofs)
{
	int err = fault('m', ofs);

	(void)m;
	if (err)
		return err;
	nand[idx(ofs)].bad = true;
	marks[nmarks++] = (int)((ofs - slot) / ERASE);
	return 0;
}
static int mtd_erase(struct mtd_info *m, struct erase_info *ei)
{
	int err = fault('e', ei->addr), i = idx(ei->addr);

	(void)m;
	ei->fail_addr = MTD_FAIL_ADDR_UNKNOWN;
	if (write_protected)
		return -EIO;
	if (err) {
		if (err == -EIO)
			ei->fail_addr = ei->addr;
		return err;
	}
	free(nand[i].data);
	nand[i].data = NULL;
	nand[i].old = false;
	return 0;
}
static int mtd_write(struct mtd_info *m, loff_t to, size_t len, size_t *ret, const u8 *buf)
{
	int err = fault('w', to), i = idx(to);

	(void)m;
	*ret = 0;
	if (write_protected)
		return -EIO;
	if (err)
		return err;
	if (nand[i].old) {
		printf("block %d programmed without an erase\n", i);
		exit(3);
	}
	if (!nand[i].data) {
		nand[i].data = malloc(ERASE);
		memset(nand[i].data, 0xff, ERASE);
	}
	for (size_t n = 0; n < len; n++)
		nand[i].data[to % ERASE + n] &= buf[n];
	if (fault('c', to))
		nand[i].data[to % ERASE] ^= 1;
	*ret = len;
	return 0;
}
static int mtd_read(struct mtd_info *m, loff_t from, size_t len, size_t *ret, u8 *buf)
{
	int i = idx(from);

	(void)m;
	*ret = len;
	if (nand[i].old)
		memset(buf, 0, len);
	else if (nand[i].data)
		memcpy(buf, nand[i].data + from % ERASE, len);
	else
		memset(buf, 0xff, len);
	return fault('r', from);
}
static int mtd_is_bitflip(int err) { return err == -EUCLEAN; }

/* The image: IMAGE_BYTES of a pattern, every fifth page empty. */
static u8 image_byte(u32 off)
{
	return (off / PAGE) % 5 == 4 ? 0xff : (u8)((off * 7 + off / 251) % 255);
}
static int rt_read(void *buf, loff_t off, loff_t len)
{
	for (loff_t n = 0; n < len; n++)
		((u8 *)buf)[n] = image_byte((u32)(off + n));
	return 0;
}
@FUNCTIONS@
int main(void)
{
	struct mtd_info mtd = { ERASE, PAGE };
	struct rt_module rf = { .off = 0, .size = @IMAGE_BYTES@, .body = @IMAGE_BYTES@ };
	static const int factory_bad[] = { @FACTORY_BAD@ -1 };
	sha256_context ctx;
	u8 *buf = malloc(RT_CHUNK);
	bool image = true, tail = true, other = true;
	u32 got = 0;
	int rc, outside, m = 0;

	for (int i = 0; i < NBLOCKS; i++)
		nand[i].old = true;
	for (const int *b = factory_bad; *b >= 0; b++)
		nand[idx(slot + (loff_t)*b * ERASE)].bad = true;
	rc = rt_write(&mtd, buf, &rf, slot, len, &ctx);
	/* What a reader that skips bad blocks finds: the image, then erased bytes. */
	for (loff_t b = slot; b < slot + len; b += ERASE) {
		int i = idx(b);

		if (nand[i].bad)
			continue;
		for (u32 n = 0; n < ERASE; n++) {
			u8 v = nand[i].old ? 0 : nand[i].data ? nand[i].data[n] : 0xff;

			if (got + n < rf.body ? v != image_byte(got + n) : v != 0xff) {
				*(got + n < rf.body ? &image : &tail) = false;
				break;
			}
		}
		got += ERASE;
	}
	/* Nothing outside the range is touched, and rt_mark_bad marks nothing outside this one. */
	for (int i = 0; i < NBLOCKS; i++) {
		loff_t b = RT_PING_KERNEL + (loff_t)i * ERASE;

		if ((b < slot || b >= slot + len) && (!nand[i].old || nand[i].bad))
			other = false;
	}
	outside = rt_mark_bad(&mtd, slot, RT_PONG_ROOTFS + RT_SLOT_SIZE, "erase", -EIO, &m) ||
		rt_mark_bad(&mtd, slot, slot - ERASE, "erase", -EIO, &m);
	printf("rc=%d|marks=", rc);
	for (int i = 0; i < nmarks; i++)
		printf("%s%d", i ? "," : "", marks[i]);
	printf("|image=%d|tail=%d|other=%d|outside_refused=%d|outside_marked=%d\n", image, tail, other, outside, m);
	return 0;
}
"""


@unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
class UsbRecoveryBadBlocks(unittest.TestCase):
    """Pass two marks a failing slot block bad and goes on, and nothing else.

    A block of the slot whose erase or program the NAND fails is marked bad and the image goes
    on in the next good block, as ubiformat does; at most RT_MARK_BAD_MAX of them. Nothing else
    marks a block: not a write-protected chip, a timeout, a read error or a readback that
    differs, and nothing outside the slot being written.
    """

    IMAGE_BLOCKS = 12  # 1.5 MiB: across RT_CHUNK

    def run_write(self, *, faults: Sequence[tuple[str, int, int, str]] = (), factory_bad: Sequence[int] = (),
                  wp: bool = False, slot: str = "RT_PONG_ROOTFS", image_bytes: int | None = None) -> dict:
        """Write an image into slot on the fake NAND with faults; return what the probe found."""
        functions = "\n".join([
            "\n".join(line[1:] for line in re.findall(r"^\+static u8 rt_page\[4096\];$", PATCH, re.MULTILINE)),
            patch_function(PATCH, "static bool rt_page_empty("),
            patch_function(PATCH, "static int rt_erase_err("),
            patch_function(PATCH, "static int rt_mark_bad("),
            patch_function(PATCH, "static int rt_write_block("),
            patch_function(PATCH, "static int rt_erase_rest("),
            patch_function(PATCH, "static int rt_write("),
        ])
        struct = patch_block(PATCH, "struct rt_module {")
        defines = patch_defines()
        kernel = "KERNEL" in slot
        fault_list = " ".join(f"{{'{op}', {block}, {start}, -{err}}}," for op, block, start, err in faults)
        src = (WRITE_PROBE.replace("@FUNCTIONS@", functions).replace("@STRUCT@", struct).replace("@DEFINES@", defines)
               .replace("@FAULTS@", fault_list).replace("@FACTORY_BAD@", "".join(f"{b}, " for b in factory_bad))
               .replace("@WP@", "true" if wp else "false").replace("@SLOT@", slot)
               .replace("@LEN@", "RT_KERNEL_SIZE" if kernel else "RT_SLOT_SIZE")
               .replace("@IMAGE_BYTES@", str(image_bytes or self.IMAGE_BLOCKS * 0x20000)))
        with tempfile.TemporaryDirectory() as d:
            Path(d, "probe.c").write_text(src)
            subprocess.run(["cc", "-std=gnu11", "-O1", "-Wall", "-Werror", "-Wno-unused-function", "-o", f"{d}/probe",
                            f"{d}/probe.c"], check=True)
            out = subprocess.run([f"{d}/probe"], check=True, capture_output=True, text=True).stdout
        r = dict(kv.split("=", 1) for kv in out.splitlines()[-1].split("|"))
        r["marks"] = [int(b) for b in r["marks"].split(",") if b]
        r["log"] = out
        # rt_mark_bad never marks outside the slot it is given, whatever it is asked.
        self.assertEqual((r["outside_refused"], r["outside_marked"]), ("1", "0"), out)
        self.assertEqual(r["other"], "1", out)
        return r

    def assert_written(self, r: dict, marks: list[int]) -> None:
        """Assert the write succeeded with exactly marks marked bad and the image whole."""
        self.assertEqual((r["rc"], r["marks"], r["image"], r["tail"]), ("0", marks, "1", "1"), r["log"])

    def assert_refused(self, r: dict, marks: Sequence[int] = ()) -> None:
        """Assert the write was refused with exactly marks marked bad."""
        self.assertEqual((r["rc"], r["marks"]), ("-5", list(marks)), r["log"])

    def test_a_clean_slot(self) -> None:
        """A clean slot takes the whole image and marks nothing."""
        for slot in ("RT_PING_ROOTFS", "RT_PONG_ROOTFS"):
            with self.subTest(slot=slot):
                self.assert_written(self.run_write(slot=slot), [])

    def test_factory_bad_blocks_are_skipped_and_not_counted(self) -> None:
        """Factory-bad blocks are skipped and not counted against the limit."""
        self.assert_written(self.run_write(factory_bad=(0, 2, 3, 4), faults=[("w", 6, 0, "EIO"), ("e", 7, 0, "EIO")]), [6, 7])

    def test_a_block_that_fails_to_erase_is_marked(self) -> None:
        """A block that fails to erase is marked bad and logged."""
        r = self.run_write(faults=[("e", 1, 0, "EIO")])
        self.assert_written(r, [1])
        self.assertIn("NAND.File-System-Pong block 1 (0x6ca0000) marked bad: erase failed (-5), 1 of 2", r["log"])

    def test_a_block_that_fails_to_program_moves_to_the_next(self) -> None:
        """A block that fails to program is marked bad and its data moves to the next.

        Page 10 of block 3 fails; the whole image block goes into block 4 from its first page.
        """
        r = self.run_write(faults=[("w", 3, 10 * 0x800, "EIO")], slot="RT_PING_ROOTFS")
        self.assert_written(r, [3])
        self.assertIn("NAND.File-System-Ping block 3 (0xce0000) marked bad: write failed (-5)", r["log"])

    def test_the_block_after_a_failed_one_can_fail_too(self) -> None:
        """The block after a failed one may fail too."""
        self.assert_written(self.run_write(faults=[("w", 3, 0, "EIO"), ("e", 4, 0, "EIO")]), [3, 4])

    def test_a_block_after_the_image_that_does_not_erase_is_marked(self) -> None:
        """A block after the image that does not erase is marked bad.

        It could hold a volume header of the old image; marked bad, UBI never reads it.
        """
        self.assert_written(self.run_write(faults=[("e", self.IMAGE_BLOCKS + 5, 0, "EIO")]), [self.IMAGE_BLOCKS + 5])

    def test_marking_stops_at_two(self) -> None:
        """Marking stops at two blocks and the write is refused."""
        r = self.run_write(faults=[("w", 3, 0, "EIO"), ("w", 4, 0, "EIO"), ("e", 5, 0, "EIO")])
        self.assert_refused(r, [3, 4])
        self.assertIn("NAND.File-System-Pong block 5: erase failed (-5); 2 blocks marked bad already, the most "
                      "allowed: not marked, nothing selected", r["log"])
        r = self.run_write(faults=[("e", 1, 0, "EIO"), ("e", 30, 0, "EIO"), ("e", 700, 0, "EIO")])
        self.assert_refused(r, [1, 30])

    def test_a_write_protected_chip_marks_nothing(self) -> None:
        """A write-protected chip marks nothing.

        Every erase fails with -EIO, but the NAND names no block: fail_addr stays unknown.
        """
        r = self.run_write(wp=True)
        self.assert_refused(r)
        self.assertIn("block 0: erase failed (-30), not a NAND failure; not marked bad", r["log"])

    def test_a_failure_that_is_not_the_nands_marks_nothing(self) -> None:
        """A failure that is not the NAND's marks nothing."""
        for op in ("e", "w"):
            with self.subTest(op=op):
                self.assert_refused(self.run_write(faults=[(op, 2, 0, "ETIMEDOUT")]))

    def test_a_readback_that_fails_or_differs_marks_nothing(self) -> None:
        """A readback that fails or differs marks nothing."""
        for fault in (("r", 2, 0, "EBADMSG"), ("c", 2, 0, "EIO")):
            with self.subTest(fault=fault):
                self.assert_refused(self.run_write(faults=[fault]))
        # A corrected read is a good read.
        self.assert_written(self.run_write(faults=[("r", 2, 0, "EUCLEAN")]), [])

    def test_a_mark_that_does_not_take_stops_the_write(self) -> None:
        """A bad-block mark that does not take stops the write."""
        r = self.run_write(faults=[("e", 2, 0, "EIO"), ("m", 2, 0, "EROFS")])
        self.assert_refused(r)
        self.assertIn("marking it bad failed (-30)", r["log"])

    def test_a_kernel_that_ends_inside_a_block(self) -> None:
        """A kernel that ends inside a block leaves the rest erased.

        Bose's kernel module is no whole number of blocks: the rest of its last block and
        the rest of the partition read erased, as nandwrite -p and flash_erase leave them.
        """
        for slot in ("RT_PING_KERNEL", "RT_PONG_KERNEL"):
            with self.subTest(slot=slot):
                self.assert_written(self.run_write(slot=slot, image_bytes=3 * 0x20000 + 0x1234), [])

    def test_nothing_in_a_kernel_partition_is_marked_bad(self) -> None:
        """Nothing in a kernel partition is marked bad.

        Only the rootfs slots are UBI, which skips a marked block; a kernel partition that
        fails a block fails the write.
        """
        r = self.run_write(slot="RT_PONG_KERNEL", image_bytes=0x30000, faults=[("e", 1, 0, "EIO")])
        self.assert_refused(r)
        self.assertIn("is outside the slot; not marked bad", r["log"])
        self.assert_written(self.run_write(slot="RT_PONG_KERNEL", image_bytes=0x30000, factory_bad=(0,)), [])

    def test_the_limit_is_the_installers(self) -> None:
        """One limit in two C sources that never link together, read from both."""
        mtd_h = (ROOT / "os/installer/module/mtd.h").read_text()
        per_part = int(re.search(r"^#define MTD_MARK_BAD_PER_PARTITION (\d+)$", mtd_h, re.MULTILINE).group(1))
        self.assertEqual(define("RT_MARK_BAD_MAX"), per_part)


if __name__ == "__main__":
    unittest.main()
