#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Hold the U-Boot USB emergency update to the sources it copies its numbers from.

os/bootloader/uboot/uboot-2026.07-usb-recovery.patch is C inside U-Boot and cannot import
anything from this tree, so every number in it is a copy: the .stu layout from
app/internal/stu, the key indices from soundtapd's defaultKeymap, the keypad GPIOs from
app/internal/hw/keypad.go and the slot offsets from the partition table. A copy that drifts installs to
the wrong place or never fires, and nobody holds those buttons often enough to notice.
"""
import hashlib
import importlib.util
import re
import shutil
import subprocess
import tempfile
import unittest
from typing import ClassVar
from pathlib import Path

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


def define(name):
    m = re.search(rf"^\+#define {name}\s+(\S+)", PATCH, re.M)
    if not m:
        raise AssertionError(f"{name} missing from the patch")
    return int(m.group(1), 0)


def go_const(name):
    return int(re.search(rf"^\s+{name}\s+= (0x[0-9a-f]+)", STU, re.M).group(1), 16)


class UsbRecoveryContract(unittest.TestCase):
    """Constants the U-Boot patch shares with soundtapd, the device trees, the recovery tools and
    the build scripts. Read as text: each pair is two spellings of one value in two languages,
    and only a cross build of U-Boot would run the patch's side; the probes below run its code."""

    def test_stu_layout(self):
        self.assertEqual(define("RT_STU_HEADER"), go_const("headerSize"))
        self.assertEqual(define("RT_STU_OVERLAP"), go_const("overlap"))
        # The module CRC at 0x12c is not read: the signed SHA-256 of the whole file covers it.
        for off, name in ((0x98, "sizeOff"), (0x134, "headerCRCOff"),
                          (0x18, "nameOff"), (0x28, "nameLen")):
            self.assertEqual(go_const(name), off, name)
            self.assertIn(f"0x{off:x}", PATCH, name)
        self.assertIn('"soundtap-rootfs"', STU_PROFILES)
        self.assertIn('"soundtap-rootfs", 0x28', PATCH)

    def test_the_verifier_is_built_in(self):
        # board.c includes the verifier, the keys and the profile; both builds put them next
        # to it, and the board Makefile compiles the verifier where the recovery is.
        for name in ("soundtap-sig.h", "soundtap-keys.h", "soundtap-profile.h"):
            self.assertIn(f'+#include "{name}"', PATCH)
        self.assertIn("+obj-$(CONFIG_USB_STORAGE) += soundtap-sig.o tweetnacl.o", PATCH)
        sources = ("soundtap-sig.c", "soundtap-sig.h", "soundtap-keys.h", "tweetnacl.c", "tweetnacl.h")
        mainline = (ROOT / "os/bootloader/build-wla-uboot.sh").read_text()
        variant = (ROOT / "os/package/soundtap-bootloaders/build-variant.sh").read_text()
        for name in sources:
            self.assertTrue((ROOT / "os/bootloader/uboot" / name).is_file(), name)
            self.assertIn(f"/repo/os/bootloader/uboot/{name}", mainline, name)
            self.assertIn(name, variant.split("for f in ")[1].split(";")[0], name)
        self.assertIn("> board/ti/am335x/soundtap-profile.h", mainline)
        self.assertIn(">board/ti/am335x/soundtap-profile.h", variant)
        # Buildroot's profile is the image's, by the rule post-build.sh uses.
        mk = (ROOT / "os/package/soundtap-bootloaders/soundtap-bootloaders.mk").read_text()
        self.assertIn("$(if $(BR2_PACKAGE_DROPBEAR),alpha,production)", mk)
        self.assertIn("grep -qx 'BR2_PACKAGE_DROPBEAR=y'", (ROOT / "os/board/rootfs/post-build.sh").read_text())
        self.assertIn('soundtap-uboot-profile=$profile)', (ROOT / "os/board/image/post-image.sh").read_text())
        self.assertIn('"Update.stu.sig: SoundTap Core %s, %s, verified (soundtap-uboot-profile="', PATCH)

    def test_tweetnacl_is_upstream(self):
        # Vendored unchanged (ADR 0124): the hashes recorded there are the files'.
        adr = (ROOT / "docs/decisions/0124-third-party-code.md").read_text()
        for name in ("tweetnacl.c", "tweetnacl.h"):
            digest = hashlib.sha256((ROOT / "os/bootloader/uboot" / name).read_bytes()).hexdigest()
            self.assertIn(f"`{name}` sha256 `{digest}`", adr)

    def test_keys(self):
        keymap = {v: int(k) for k, v in re.findall(r'(\d+): "([A-Z_0-9]+)"',
                                                         BUTTONS.split("var defaultKeymap")[1].split("}")[0])}
        self.assertIn(f"RT_KEY({keymap['VOLUME_DOWN']}) | RT_KEY({keymap['PRESET_4']})", PATCH)

    def test_keypad_gpios(self):
        strobes = re.search(r"keypadStrobeOffsets\s+= \[\]int\{([^}]*)\}", KEYPAD).group(1)
        ins = re.search(r"keypadSwitchinOffsets\s+= \[\]int\{([^}]*)\}", KEYPAD).group(1)
        want_s = ", ".join(f'"gpio@4804c000_{n.strip()}"' for n in strobes.split(","))
        want_i = ", ".join(f'"gpio@44e07000_{n.strip()}"' for n in ins.split(","))
        self.assertIn(want_s, PATCH)
        self.assertIn(want_i, PATCH)

    def test_st20_keypad_gpios(self):
        spotty = re.search(r"strobeOffsets: \[\]int\{([^}]*)\}, switchOffsets: \[\]int\{([^}]*)\}", KEYPAD)
        for offsets in spotty.groups():
            self.assertIn(", ".join(f'"gpio@44e07000_{n.strip()}"' for n in offsets.split(",")), PATCH)

    def test_wla_buttons(self):
        rows = re.search(r"switchins = <(\d+) (\d+)>", WLA_DTS).groups()
        self.assertEqual(rows, ("23", "27"))
        self.assertIn(", ".join(f'"gpio@44e07000_{n}"' for n in rows), PATCH)
        for pad in ("gpmc_ad9", "gpmc_ad11"):
            self.assertIn(f"{{OFFSET({pad}), MODE(7) | RXACTIVE | PULLUDDIS}}", PATCH)
        self.assertIn('!strcmp(v, "binky")', PATCH)

    def test_ginger_combo(self):
        # Stock U-Boot's ginger route: the LPM raises gpio0_26 (this SM2 revision) or
        # gpio0_23 (the other) at power-on; gpmc_ad10 and gpmc_ad9 are those pads.
        self.assertIn('rev2 ? "gpio@44e07000_26" : "gpio@44e07000_23"', PATCH)
        self.assertIn("rev2 ? OFFSET(gpmc_ad10) : OFFSET(gpmc_ad9)", PATCH)
        self.assertIn("dm_gpio_get_value(&in) == 1", PATCH)
        self.assertIn('!strcmp(v, "ginger")', PATCH)

    def test_hunk_counts(self):
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

    def test_slots(self):
        parts = {name: (int(size, 16), int(off, 16))
                     for size, off, name in re.findall(r"(0x[0-9A-F]+)@(0x[0-9A-F]+)\(([^)]+)\)", ENV)}
        self.assertEqual(parts["NAND.File-System-Ping"], (define("RT_SLOT_SIZE"), define("RT_PING_ROOTFS")))
        self.assertEqual(parts["NAND.File-System-Pong"], (define("RT_SLOT_SIZE"), define("RT_PONG_ROOTFS")))
        # nand_boot is Ping, nand_boot2 Pong (os/support/uboot_recipes.py, bootselect.go).
        self.assertIn('pong ? "run nand_boot;" : "run nand_boot2;"', PATCH)
        self.assertIn('pong ? RT_PING_ROOTFS : RT_PONG_ROOTFS', PATCH)

    def test_env_marker(self):
        # soundtapd writes the redundant environment only under a U-Boot that says it reads
        # it on the kernel command line (ADR 0010); the two spellings are one.
        go = re.search(r'^const EnvRedundantMarker = "([^"]+)"$', FLASH, re.M).group(1)
        self.assertEqual(re.search(r'^\+#ifdef CONFIG_ENV_REDUNDANT\n\+#define RT_ENV_MARKER\s+" ([^"]+)"$',
                                   PATCH, re.M).group(1), go)

    def test_st10_partition_table_matches_shared(self):
        # The ST10 device tree carries its own copy of the table the others include.
        dts = ROOT / "os/board/common/dts/ti/omap"
        def table(name):
            return re.findall(r'label = "(NAND\.[^"]+)";\s*reg = <(0x\w+ 0x\w+)>',
                              (dts / name).read_text())
        shared = table("am335x-soundtap-nand.dtsi")
        self.assertEqual(len(shared), 13)
        self.assertEqual(table("am335x-soundtap-st10.dts"), shared)


# The power-on path, compiled on the host from the patches with the hardware stubbed out:
# every EVT_LAST_STAGE_INIT spy in U-Boot's order (rt_board_setup reads the variant,
# rt_usb_recovery reads the gesture and runs rt_usb_update), over an environment table that
# holds only what was set. An erased or torn mtd5 leaves U-Boot its built-in bootcmd plus the
# variant_raw board_late_init sampled; an installed one carries the slot recipes.
# FAIL_AT names a stub that fails, to show nothing is selected.
PROBE = r"""
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define loff_t long long	/* glibc has it, macOS does not */
struct fdt_property;
typedef unsigned char u8;
typedef unsigned int u32;
static const char *board = BOARD;	/* st10, st20, wla or st300 */
static const int held = HELD;		/* that model's recovery gesture, held at power-on */
static const char *default_bootcmd = DEFAULT_BOOTCMD;
static int fail(const char *what) { return !strcmp(FAIL_AT, what) ? -EIO : 0; }

/* CONTROL_STATUS: SYSBOOT 10010b on the ST20 only. Everything else reads 0. */
static u32 readl(unsigned long addr) { return addr == 0x44e10040 ? (!strcmp(board, "st20") ? 0x12 : 0x18) : 0; }
static void mdelay(int ms) { (void)ms; }
static void configure_module_pin_mux(void *mux) { (void)mux; }
static int scm_nand_wp_pin_mux[1];
static unsigned long hextoul(const char *s, char **end) { return strtoul(s, end, 16); }

/* The environment: a key is there only once set, as in U-Boot. */
#define ENV_MAX 32
static struct { char key[32]; char *val; } env[ENV_MAX];
static int saves;
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
			env[i].val = strdup(v);
			return 0;
		}
		if (!env[i].val && free_slot < 0)
			free_slot = i;
	}
	if (free_slot < 0)
		abort();
	strcpy(env[free_slot].key, k);
	env[free_slot].val = strdup(v);
	return 0;
}
static int env_save(void)
{
	int ret = fail("env_save");

	if (!ret)
		saves++;
	return ret;
}
static unsigned long bootcount = 7; /* the old slot kept failing: why recovery runs */
static void bootcount_store(unsigned long n) { bootcount = n; }

/* Each model reads its own gesture; on the others those pins are not the gesture. */
static bool rt_wla_both_held(void) { return held && !strcmp(board, "wla"); }
static bool rt_ginger_combo(void) { return held && !strcmp(board, "st300"); }
static void rt_lamp(bool on) { (void)on; }
static const char *lamp = "none";	/* which failure the lamps showed */
static void rt_lamp_fail(void) { lamp = "fail"; }
static void rt_lamp_refused(void) { lamp = "refused"; }

/* U-Boot's numbers, whatever the host's errno.h says (macOS has no ENOKEY). */
#undef EBADMSG
#define EBADMSG 74
#undef ENOKEY
#define ENOKEY 126
#undef EKEYREJECTED
#define EKEYREJECTED 129

/* The signature (soundtap-sig.c, tested on its own in app/internal/release/uboot_test.go):
 * FAIL_AT "unsigned" is a stick without Update.stu.sig, "sig" one whose statement is
 * refused. The first SHA-256 finished is pass one's, the second pass two's; "digest1" and
 * "digest2" make that one miss the signed digest. */
#define SHA256_SUM_LEN 32
typedef struct { int n; } sha256_context;
static int finishes;
static void sha256_starts(sha256_context *c) { c->n = 0; }
static void sha256_update(sha256_context *c, const u8 *b, u32 n) { (void)c; (void)b; (void)n; }
static void sha256_finish(sha256_context *c, u8 *d) { (void)c; memset(d, 0, SHA256_SUM_LEN); d[0] = ++finishes; }
struct st_statement { u8 digest[SHA256_SUM_LEN]; };
#define ST_WRONG_FILE 7
static const char *st_result_text(int r) { (void)r; return "refused"; }
static int st_matches(const struct st_statement *st, const u8 *d)
{
	(void)st;
	return fail(d[0] == 1 ? "digest1" : "digest2") ? ST_WRONG_FILE : 0;
}
static int rt_check_sig(u8 *buf, struct st_statement *st)
{
	(void)buf; (void)st;
	return !strcmp(FAIL_AT, "unsigned") ? -ENOKEY : !strcmp(FAIL_AT, "sig") ? -EKEYREJECTED : 0;
}
static int rt_hash(u8 *buf, sha256_context *ctx, loff_t off, loff_t len) { (void)buf; (void)ctx; (void)off; (void)len; return 0; }
/* Reads zeroes, as the header rt_find_rootfs kept; "hdr" reads it back otherwise. */
static int rt_read(void *buf, loff_t off, loff_t len)
{
	(void)off;
	memset(buf, 0, len);
	((u8 *)buf)[0] = !strcmp(FAIL_AT, "hdr");
	return 0;
}

/* A stick with a good package in; rt_write records which slot it was given. */
struct mtd_info { u32 erasesize; };
static struct mtd_info nand = { 0x20000 };
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
static int rt_find_rootfs(u8 *hdr, loff_t file, struct rt_rootfs *rf)
{
	(void)hdr; (void)file; rf->off = RT_STU_HEADER; rf->size = 0x20000; return 0;
}
static int rt_write(struct mtd_info *mtd, u8 *buf, const struct rt_rootfs *rf, loff_t part, sha256_context *ctx)
{
	(void)mtd; (void)buf; (void)rf; (void)ctx;
	written = part;
	return fail("rt_write");
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
		env_set("nand_boot", "boot Ping");
		env_set("nand_boot2", "boot Pong");
		env_set("soundtap_args", "setenv bootargs variant=" INSTALLED_VARIANT);
		/* The rest of the recipes its installer wrote; Bose's environment has none of them. */
		if (!BOSE_RECIPES) {
			env_set("slot_boot", "installed");
			env_set("stock_args", "installed");
			env_set("mtdids", "installed");
			env_set("mtdparts", "installed");
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
UBOOT_CONFIG = (ROOT / "os/package/soundtap-bootloaders/configs/wla-uboot.config").read_text()
# Applied in this order by os/bootloader/build-wla-uboot.sh.
SPY_PATCHES = (SCM_PATCH, PATCH)


def patch_function(patch, signature):
    body = patch.split("+" + signature)[1].split("\n+}\n")[0]
    return "\n".join(l[1:] for l in ("+" + signature + body + "\n+}").split("\n"))


def last_stage_spies():
    """The EVT_LAST_STAGE_INIT spies the patches add, in the order U-Boot runs them: its
    linker script keeps the u_boot_list entries SORTed by name, and a simple spy's entry is
    named after the event and then the function, so one event's spies run by function name."""
    names = [fn for patch in SPY_PATCHES
             for fn in re.findall(r"^\+EVENT_SPY_SIMPLE\(EVT_LAST_STAGE_INIT, (\w+)\);", patch, re.M)]
    return sorted(names)


def spy_function(name):
    for patch in SPY_PATCHES:
        m = re.search(rf"^\+static int {name}\(void\)$", patch, re.M)
        if m:
            return patch_function(patch, f"static int {name}(void)")
    raise AssertionError(f"spy {name} has no body in the patches")


@unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
class UsbRecoveryProbe(unittest.TestCase):
    # variant_raw as board_late_init publishes it (hex; build-wla-uboot.sh: WLA 2518-2618,
    # ST10 3630). The ST20 is told apart by SYSBOOT, whatever its ADC reads.
    BOARDS: ClassVar[dict[str, tuple[str, str]]] = {"st10": ("3630", "rhino"), "wla": ("2518", "binky"), "st20": ("", "spotty"),
                                                     "st300": ("3669", "ginger")}

    MFG: ClassVar[dict[str, str]] = {"st10": "rhino", "wla": "binky", "st20": "", "st300": "ginger"}

    def run_probe(self, board, held, variant_raw, installed=None, variant="", fail_at="", spies=None, mfg=None,
                  bose_recipes=False):
        """installed is the saved bootcmd ("run nand_boot;"), or None for an erased mtd5."""
        default = re.search(r'^CONFIG_BOOTCOMMAND=(".*")$', UBOOT_CONFIG, re.M).group(1)
        spies = last_stage_spies() if spies is None else spies
        functions = "\n".join([
            "\n".join(l[1:] for l in re.findall(r"^\+#define RT_MFG_\S+\s+\S.*$", SCM_PATCH, re.M)),
            patch_function(SCM_PATCH, "static const char *rt_mfg_variant(void)"),
            patch_function(PATCH, "static bool rt_st20(void)"),
            "\n".join(l[1:] for l in re.search(r"^\+static const char \*const rt_env_templates\[\] = \{.*?^\+\};",
                                                PATCH, re.M | re.S).group(0).split("\n")),
            patch_function(PATCH, "static int rt_set_recipes(void)"),
            patch_function(PATCH, "static int rt_usb_update(void)"),
            "\n".join(l[1:] for l in re.findall(r"^\+static int rt_recovery_result = .*$", PATCH, re.M)),
            patch_function(PATCH, "static void rt_mark_cmdline(int ret)"),
            patch_function(PATCH, "const char *board_fdt_chosen_bootargs(const struct fdt_property *fdt_ba)"),
        ] + [spy_function(name) for name in last_stage_spies()])
        struct = "\n".join(l[1:] for l in re.search(r"^\+struct rt_rootfs \{.*?^\+\};", PATCH, re.M | re.S).group(0).split("\n"))
        # RT_ENV_MARKER as the product build has it: CONFIG_ENV_REDUNDANT is on there.
        defines = "\n".join(l[1:] for l in re.findall(r"^\+#define RT_\S+\s+\S.*$", PATCH, re.M)
                            if "RT_ENV_MARKER" not in l)
        defines += "\n" + re.search(r'^\+#ifdef CONFIG_ENV_REDUNDANT\n\+(#define RT_ENV_MARKER.*)$', PATCH, re.M).group(1)
        src = (PROBE.replace("FUNCTIONS", functions).replace("STRUCT", struct)
               .replace("DEFINES", defines).replace("DEFAULT_BOOTCMD", default)
               .replace("SPIES", " ".join(f"{name}();" for name in spies))
               .replace("BOARD", f'"{board}"').replace("HELD", str(int(held)))
               .replace("VARIANT_RAW", f'"{variant_raw}"').replace("FAIL_AT", f'"{fail_at}"')
               .replace("INSTALLED_BOOTCMD", f'"{installed or ""}"')
               .replace("INSTALLED_VARIANT", f'"{variant}"').replace("BOSE_RECIPES", str(int(bose_recipes)))
               .replace("MFG_NAME", f'"{self.MFG[board] if mfg is None else mfg}"'))
        with tempfile.TemporaryDirectory() as d:
            Path(d, "probe.c").write_text(src)
            subprocess.run(["cc", "-std=gnu11", "-Wall", "-Werror", "-Wno-unused-function", "-o", f"{d}/probe", f"{d}/probe.c"],
                           check=True)
            out = subprocess.run([f"{d}/probe"], check=True, capture_output=True, text=True).stdout
        r = dict(kv.split("=", 1) for kv in out[out.index("cmdline="):].strip().split("|"))
        # Every entry the environment holds at the end, as env_save would store it.
        r["env"] = dict(line[4:].split("=", 1) for line in out.splitlines() if line.startswith("env:"))
        return r

    def booted_slot(self, r, bootcmd=None):
        """The slot a boot lands in, by the bootcmd it runs."""
        bootcmd = bootcmd or r["bootcmd"]
        if bootcmd == "default":
            # The built-in bootcmd tries the slots in the order it names them.
            return re.findall(r"ubi part NAND\.File-System-(\w+)", UBOOT_CONFIG)[0]
        recipe = {"run nand_boot;": "nand_boot", "run nand_boot2;": "nand_boot2"}.get(bootcmd)
        if not recipe or r[recipe] != "set":
            return None  # a bootcmd naming a recipe that is not there boots nothing
        return {"nand_boot": "Ping", "nand_boot2": "Pong"}[recipe]

    @staticmethod
    def boots(r, n=5):
        """U-Boot's bootcount choice over the environment left (bootdelay_process: altbootcmd
        once the count passes bootlimit), for n boots that never confirm."""
        if r["bootlimit"] == "unset":
            return [r["bootcmd"]] * n
        count, limit = int(r["bootcount"]), int(r["bootlimit"])
        return [r["altbootcmd"] if count + i > limit else r["bootcmd"] for i in range(1, n + 1)]

    def test_spies_run_board_setup_first(self):
        # The variant has to be set before the gesture asks for it on a torn environment.
        self.assertEqual(last_stage_spies(), ["rt_board_setup", "rt_usb_recovery"])
        # And the order is what makes it work: the other way round the ST10 gesture is lost.
        r = self.run_probe("st10", True, "3630", spies=["rt_usb_recovery", "rt_board_setup"])
        self.assertEqual(r["written"], "none")

    def test_gesture_installs_and_boots_with_an_erased_environment(self):
        for board, (raw, variant) in self.BOARDS.items():
            with self.subTest(board=board):
                r = self.run_probe(board, True, raw)
                self.assertEqual(r["rt_v"], variant)
                self.assertNotEqual(r["written"], "none", "the gesture was ignored")
                # Saved, with every recipe the saved bootcmd names: the built-in
                # environment had none of them.
                self.assertEqual(r["saves"], "1")
                self.assertEqual((r["bootcmd"], r["altbootcmd"], r["upgrade_available"]),
                                 ("run nand_boot2;", "run nand_boot;", "1"))
                self.assertEqual(self.booted_slot(r), r["written"])
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=0"), r["cmdline"])

    def test_the_saved_environment_is_complete(self):
        # After a torn environment the save holds this board's recipes, the same ones every
        # installer writes for it, and the nand_args pair soundtapd selects slots through.
        rebuilt = BOOTSELECT[BOOTSELECT.index("func (in *Installer) rebuiltEnv("):]
        for board, (raw, variant) in self.BOARDS.items():
            with self.subTest(board=board):
                env = self.run_probe(board, True, raw)["env"]
                for entry in WLA_ENV.recipes(variant.encode()):
                    key, value = entry.decode().split("=", 1)
                    self.assertEqual(env.get(key), value, key)
                for key in ("nand_args", "nand_args2"):
                    self.assertIn(f'env.Set("{key}", "{env[key]}")', rebuilt)

    def test_the_model_comes_from_the_board(self):
        # An environment that names another model (a board moved between speakers, a wrong
        # restore) does not decide which gesture is looked for: the board does.
        for board, other in (("st10", "binky"), ("wla", "rhino"), ("st300", "rhino"), ("st20", "binky")):
            with self.subTest(board=board):
                raw, _ = self.BOARDS[board]
                r = self.run_probe(board, True, raw, installed="run nand_boot;", variant=other)
                self.assertEqual(r["written"], "Pong")
                # Its recipes are all there, so they stay: its installer chose them.
                self.assertTrue(r["args"].endswith(f"variant={other}"), r["args"])

    def test_an_environment_without_our_recipes_gets_them(self):
        # Bose's environment names nand_boot (nboot.e) but has no slot_boot: the save holds
        # the whole set for the board, so the bootcmd it writes runs.
        r = self.run_probe("wla", True, "2518", installed="run nand_boot;", variant="binky", bose_recipes=True)
        self.assertEqual((r["written"], r["saves"], r["bootcmd"]), ("Pong", "1", "run nand_boot2;"))
        for entry in WLA_ENV.recipes(b"binky"):
            key, value = entry.decode().split("=", 1)
            self.assertEqual(r["env"].get(key), value, key)

    def test_no_gesture_with_an_erased_environment_changes_nothing(self):
        for board, (raw, _) in self.BOARDS.items():
            with self.subTest(board=board):
                r = self.run_probe(board, False, raw)
                self.assertEqual((r["written"], r["saves"], r["bootcmd"]), ("none", "0", "default"))

    def test_wla_variant_range(self):
        for raw in ("2518", "2618"):
            self.assertEqual(self.run_probe("wla", True, raw)["rt_v"], "binky")

    def test_gesture_with_the_installed_environment(self):
        # The normal case moves the saved bootcmd to the slot it wrote and tells Linux.
        for board, (raw, variant) in self.BOARDS.items():
            with self.subTest(board=board):
                r = self.run_probe(board, True, raw, installed="run nand_boot;", variant=variant)
                self.assertEqual((r["written"], r["saves"], r["bootcmd"]), ("Pong", "1", "run nand_boot2;"))
                self.assertEqual(self.booted_slot(r), "Pong")
                self.assertTrue(r["cmdline"].endswith(" soundtap.env=redundant soundtap.recovery=0"), r["cmdline"])
                # The mark is for this boot only: the environment, which the boot count of
                # the unconfirmed slot saves again, never carries it.
                self.assertNotIn("soundtap.recovery", r["args"])

    def test_recovery_leaves_the_normal_install_env(self):
        # Bootcmd moved to the new slot while altbootcmd was left alone, so a recovered
        # slot that never confirmed kept being retried instead of the preserved one.
        limit = re.search(r"const BootLimit = (\d+)", BOOTSELECT).group(1)
        for running, other in (("run nand_boot;", "run nand_boot2;"), ("run nand_boot2;", "run nand_boot;")):
            with self.subTest(running=running):
                r = self.run_probe("st10", True, "3630", installed=running, variant="rhino")
                self.assertEqual((r["bootcmd"], r["altbootcmd"], r["bootlimit"], r["bootcount"]),
                                 (other, running, limit, "0"))
                # Unconfirmed: the count is kept in the environment, saved with the slot.
                self.assertEqual((r["upgrade_available"], r["env_bootcount"]), ("1", "0"))
                # The count starts over: the new slot gets bootlimit tries, then the old one boots.
                self.assertEqual(self.boots(r), [other if n <= int(limit) else running for n in range(1, 6)])

    def test_failed_recovery_selects_nothing(self):
        for step in ("rt_write", "env_save"):
            with self.subTest(step=step):
                r = self.run_probe("st10", True, "3630", installed="run nand_boot;", variant="rhino", fail_at=step)
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=-5"), r["cmdline"])
                # Nothing was installed or saved, so the count stays as it was.
                self.assertEqual((r["saves"], r["bootcount"]), ("0", "7"))
                if step == "rt_write":
                    self.assertEqual((r["bootcmd"], r["altbootcmd"], r["bootlimit"]),
                                     ("run nand_boot;", "unset", "unset"))

    def test_a_stick_that_is_not_signed_for_this_speaker_writes_nothing(self):
        # ADR 0048: no Update.stu.sig (Bose's file, or ours without it), a statement that is
        # refused, or an Update.stu that does not hash to it. Nothing is erased, nothing is
        # saved, the lamps say refused rather than failed, and Linux is told why.
        for step, rc in (("unsigned", -126), ("sig", -129), ("digest1", -129)):
            with self.subTest(step=step):
                r = self.run_probe("st10", True, "3630", installed="run nand_boot;", variant="rhino", fail_at=step)
                self.assertEqual((r["written"], r["saves"], r["bootcmd"], r["bootcount"]),
                                 ("none", "0", "run nand_boot;", "7"))
                self.assertEqual(r["lamp"], "refused")
                self.assertTrue(r["cmdline"].endswith(f" soundtap.recovery={rc}"), r["cmdline"])

    def test_a_stick_that_reads_back_otherwise_selects_nothing(self):
        # Pass two hashes the file again; a stick that changed under it, or a rootfs header
        # that is not the one that placed the write, leaves the old slot booting.
        for step, written in (("digest2", "Pong"), ("hdr", "none")):
            with self.subTest(step=step):
                r = self.run_probe("st10", True, "3630", installed="run nand_boot;", variant="rhino", fail_at=step)
                self.assertEqual((r["written"], r["saves"], r["bootcmd"], r["altbootcmd"]),
                                 (written, "0", "run nand_boot;", "unset"))
                self.assertEqual(r["lamp"], "fail")
                self.assertTrue(r["cmdline"].endswith(" soundtap.recovery=-74"), r["cmdline"])

    def test_a_signed_stick_installs(self):
        r = self.run_probe("st20", True, "", installed="run nand_boot2;", variant="spotty")
        self.assertEqual((r["written"], r["saves"], r["bootcmd"], r["lamp"]), ("Ping", "1", "run nand_boot;", "none"))

    def test_variant_from_the_manufacturing_partition(self):
        # The product name in the manufacturing partition decides, whatever the ADC read:
        # an ST300 whose variant_raw looks like a Wireless Link's, an ST10 without a sample.
        self.assertEqual(self.run_probe("st300", True, "2518")["rt_v"], "ginger")
        self.assertEqual(self.run_probe("st10", True, "")["rt_v"], "rhino")
        # An unknown or unreadable field falls back to variant_raw, as before.
        self.assertEqual(self.run_probe("st10", True, "3630", mfg="lisa")["rt_v"], "rhino")
        self.assertEqual(self.run_probe("wla", True, "2518", fail_at="mfg")["rt_v"], "binky")
        # Only the ST300 turns autoboot keys off: its UART0 carries the soundbar link.
        for board, (raw, _) in self.BOARDS.items():
            self.assertEqual(self.run_probe(board, False, raw)["bootdelay"], "-2" if board == "st300" else "unset")
        # The ST20 is told by SYSBOOT and never reads it.
        self.assertEqual(self.run_probe("st20", True, "", mfg="ginger")["rt_v"], "spotty")


# The recovery's own rt_check_sig, from the patch, over the real verifier (soundtap-sig.c,
# tweetnacl.c) and a stick held in memory. The probe makes a release key and the owner's
# key with TweetNaCl, signs Update.stu.sig with the one argv[1] names and sets the
# environment's soundtap_owner_key as argv[2] says; it prints rt_check_sig's result.
SIG_PROBE = r"""
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "soundtap-sig.h"
#include "tweetnacl.h"
#define loff_t long long
typedef unsigned char u8;
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#undef ENOKEY
#define ENOKEY 126
#undef EKEYREJECTED
#define EKEYREJECTED 129
#define SOUNDTAP_UBOOT_PROFILE "production"
SIG_NAME

/* Deterministic keys: each keypair takes its seed from the next byte. */
static unsigned char seed;
void randombytes(unsigned char *x, unsigned long long n) { memset(x, ++seed, n); }

static unsigned char st_release_keys[1][32], release_sk[64], owner_pk[32], owner_sk[64], other_pk[32], other_sk[64];
static char owner_env[64];
static const char *env_mode;
static const char *env_get(const char *name)
{
	if (strcmp(name, "soundtap_owner_key") || !strcmp(env_mode, "unset"))
		return NULL;
	return owner_env;
}

static char sigfile[512];
static int rt_part;
static int fs_set_blk_dev(const char *ifname, int part, int fstype) { (void)ifname; (void)part; (void)fstype; return 0; }
#define FS_TYPE_ANY 0
static int fs_size(const char *name, loff_t *size) { (void)name; *size = strlen(sigfile); return 0; }
static unsigned long map_to_sysmem(void *p) { return (unsigned long)p; }
static int fs_read(const char *name, unsigned long addr, loff_t off, loff_t len, loff_t *got)
{
	(void)name;
	memcpy((void *)addr, sigfile + off, len);
	*got = len;
	return 0;
}

static void b64(const unsigned char *in, int n, char *out)
{
	static const char a[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	int i, o = 0;

	for (i = 0; i < n; i += 3) {
		unsigned v = in[i] << 16 | (i + 1 < n ? in[i + 1] << 8 : 0) | (i + 2 < n ? in[i + 2] : 0);
		out[o++] = a[v >> 18 & 63];
		out[o++] = a[v >> 12 & 63];
		out[o++] = i + 1 < n ? a[v >> 6 & 63] : '=';
		out[o++] = i + 2 < n ? a[v & 63] : '=';
	}
	out[o] = 0;
}

RT_CHECK_SIG

int main(int argc, char **argv)
{
	static unsigned char sm[512], buf[1024];
	unsigned long long smlen;
	const char *text = "soundtap-package v1 kind=core profile=production version=0.2.0 "
		"sha256=abababababababababababababababababababababababababababababababab";
	const unsigned char *sk;
	char sig[100];
	struct st_statement st;

	if (argc != 3)
		return 2;
	crypto_sign_keypair(st_release_keys[0], release_sk);
	crypto_sign_keypair(owner_pk, owner_sk);
	crypto_sign_keypair(other_pk, other_sk);
	sk = !strcmp(argv[1], "owner") ? owner_sk : !strcmp(argv[1], "release") ? release_sk : other_sk;
	crypto_sign(sm, &smlen, (const unsigned char *)text, strlen(text), sk);
	b64(sm, 64, sig);
	snprintf(sigfile, sizeof(sigfile), "%s sig=%s\n", text, sig);
	env_mode = argv[2];
	if (!strcmp(env_mode, "set"))
		b64(owner_pk, 32, owner_env);
	else if (!strcmp(env_mode, "other"))
		b64(other_pk, 32, owner_env);
	else if (!strcmp(env_mode, "garbled")) {
		b64(owner_pk, 32, owner_env);
		owner_env[43] = 0; /* cut short */
	} else if (!strcmp(env_mode, "zero"))
		strcpy(owner_env, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=");
	printf("result=%d\n", rt_check_sig(buf, &st));
	return 0;
}
"""


@unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
class UsbRecoveryOwnerKey(unittest.TestCase):
    """The owner key (ADR 0048): soundtap_owner_key in the environment is trusted next to the
    compiled release keys; unset, empty, cut short or of small order it is no key at all."""

    probe: ClassVar[str] = ""

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        d, src = Path(cls.tmp.name), ROOT / "os/bootloader/uboot"
        sig_name = re.search(r"^\+(#define RT_SIG_NAME\s+.*)$", PATCH, re.M).group(1)
        probe = (SIG_PROBE.replace("SIG_NAME", sig_name)
                 .replace("RT_CHECK_SIG", patch_function(PATCH, "static int rt_check_sig(u8 *buf, struct st_statement *st)")))
        (d / "probe.c").write_text(probe)
        subprocess.run(["cc", "-std=gnu11", "-O1", "-w", "-c", "-o", str(d / "tweetnacl.o"), str(src / "tweetnacl.c")], check=True)
        subprocess.run(["cc", "-std=gnu11", "-Wall", "-Werror", "-Wno-unused-function", "-I", str(src), "-o", str(d / "probe"),
                        str(d / "probe.c"), str(src / "soundtap-sig.c"), str(d / "tweetnacl.o")], check=True)
        cls.probe = str(d / "probe")

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def check(self, signer, env):
        out = subprocess.run([self.probe, signer, env], check=True, capture_output=True, text=True).stdout
        return int(re.search(r"^result=(-?\d+)$", out, re.M).group(1)), out

    def test_the_owner_key_from_the_environment_is_trusted(self):
        rc, out = self.check("owner", "set")
        self.assertEqual(rc, 0, out)
        self.assertIn("soundtap_owner_key: the owner key is trusted as well", out)
        self.assertIn("verified", out)

    def test_without_the_owner_key_an_owner_signed_stick_is_refused(self):
        for env in ("unset", "empty", "other", "garbled", "zero"):
            with self.subTest(env=env):
                rc, out = self.check("owner", env)
                self.assertEqual(rc, -129, out)
                # Another owner's key is a key, trusted and of no use for this package.
                self.assertEqual("trusted as well" in out, env == "other", out)

    def test_a_value_that_is_no_key_is_ignored_and_the_release_key_still_works(self):
        for env in ("unset", "set", "empty", "garbled", "zero"):
            with self.subTest(env=env):
                rc, out = self.check("release", env)
                self.assertEqual(rc, 0, out)
                self.assertEqual("not an ed25519 public key; ignored" in out, env in ("empty", "garbled", "zero"), out)

    def test_another_key_is_still_refused(self):
        self.assertEqual(self.check("other", "set")[0], -129)

    def test_the_variable_is_the_one_soundtapd_writes(self):
        """One name in Go and in C, read from both sources."""
        go = (ROOT / "app/internal/release/owner.go").read_text()
        name = re.search(r'^const OwnerKeyEnv = "([a-z_]+)"$', go, re.M).group(1)
        header = (ROOT / "os/bootloader/uboot/soundtap-sig.h").read_text()
        self.assertIn(f'#define ST_OWNER_KEY_ENV\t"{name}"', header)
        self.assertIn("env_get(ST_OWNER_KEY_ENV)", PATCH)


# Pass two's write (rt_write, rt_write_block, rt_mark_bad), compiled on the host from the patch
# over a fake NAND that holds the two rootfs slots. FAULTS lists blocks of the slot being
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
#define MTD_FAIL_ADDR_UNKNOWN (-1LL)
#undef EUCLEAN
#define EUCLEAN 117
#undef EBADMSG
#define EBADMSG 74
DEFINES
STRUCT
typedef struct { int n; } sha256_context;
static void sha256_update(sha256_context *c, const u8 *b, u32 n) { (void)c; (void)b; (void)n; }
static void rt_kick(void) {}
struct mtd_info { u32 erasesize, writesize; };
struct erase_info { struct mtd_info *mtd; u64 addr, len; long long fail_addr; };

#define ERASE 0x20000
#define PAGE 0x800
#define NBLOCKS ((RT_PONG_ROOTFS + RT_SLOT_SIZE - RT_PING_ROOTFS) / ERASE)
static struct { bool bad, old; u8 *data; } nand[NBLOCKS];	/* data NULL: erased */
struct fault { char op; int block; u32 from; int err; };
static const struct fault faults[] = { FAULTS {0, 0, 0, 0} };
static const bool write_protected = WP;
static const loff_t slot = SLOT;
static int marks[64], nmarks;

static int idx(loff_t ofs)
{
	if (ofs < RT_PING_ROOTFS || ofs >= RT_PONG_ROOTFS + RT_SLOT_SIZE) {
		printf("access outside the rootfs slots at 0x%llx\n", ofs);
		exit(3);
	}
	return (int)((ofs - RT_PING_ROOTFS) / ERASE);
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

/* The image: IMAGE_BLOCKS eraseblocks of a pattern, every fifth page empty. */
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
FUNCTIONS
int main(void)
{
	struct mtd_info mtd = { ERASE, PAGE };
	struct rt_rootfs rf = { .off = 0, .size = IMAGE_BLOCKS * ERASE, .body = IMAGE_BLOCKS * ERASE };
	static const int factory_bad[] = { FACTORY_BAD -1 };
	sha256_context ctx;
	u8 *buf = malloc(RT_CHUNK);
	bool image = true, tail = true, other = true;
	u32 got = 0;
	int rc, outside, m = 0;

	for (int i = 0; i < NBLOCKS; i++)
		nand[i].old = true;
	for (const int *b = factory_bad; *b >= 0; b++)
		nand[idx(slot + (loff_t)*b * ERASE)].bad = true;
	rc = rt_write(&mtd, buf, &rf, slot, &ctx);
	/* What a reader that skips bad blocks finds: the image, then erased blocks. */
	for (loff_t b = slot; b < slot + RT_SLOT_SIZE; b += ERASE) {
		int i = idx(b);

		if (nand[i].bad)
			continue;
		for (u32 n = 0; n < ERASE; n++) {
			u8 v = nand[i].old ? 0 : nand[i].data ? nand[i].data[n] : 0xff;

			if (got < rf.body ? v != image_byte(got + n) : v != 0xff) {
				*(got < rf.body ? &image : &tail) = false;
				break;
			}
		}
		got += ERASE;
	}
	/* The other slot is never touched, and rt_mark_bad marks nothing outside this one. */
	for (loff_t b = slot == RT_PING_ROOTFS ? RT_PONG_ROOTFS : RT_PING_ROOTFS, e = b + RT_SLOT_SIZE; b < e; b += ERASE)
		if (!nand[idx(b)].old || nand[idx(b)].bad)
			other = false;
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
    """A block of the slot whose erase or program the NAND fails is marked bad and the image goes
    on in the next good block, as ubiformat does; at most RT_MARK_BAD_MAX of them. Nothing else
    marks a block: not a write-protected chip, a timeout, a read error or a readback that
    differs, and nothing outside the slot being written."""

    IMAGE_BLOCKS = 12  # 1.5 MiB: across RT_CHUNK

    def run_write(self, faults=(), factory_bad=(), wp=False, slot="RT_PONG_ROOTFS"):
        functions = "\n".join([
            "\n".join(l[1:] for l in re.findall(r"^\+static u8 rt_page\[4096\];$", PATCH, re.M)),
            patch_function(PATCH, "static bool rt_page_empty("),
            patch_function(PATCH, "static int rt_erase_err("),
            patch_function(PATCH, "static int rt_mark_bad("),
            patch_function(PATCH, "static int rt_write_block("),
            patch_function(PATCH, "static int rt_write("),
        ])
        struct = "\n".join(l[1:] for l in re.search(r"^\+struct rt_rootfs \{.*?^\+\};", PATCH, re.M | re.S).group(0).split("\n"))
        defines = "\n".join(l[1:] for l in re.findall(r"^\+#define RT_\S+\s+\S.*$", PATCH, re.M) if "RT_ENV_MARKER" not in l)
        fault_list = " ".join(f"{{'{op}', {block}, {start}, -{err}}}," for op, block, start, err in faults)
        src = (WRITE_PROBE.replace("FUNCTIONS", functions).replace("STRUCT", struct).replace("DEFINES", defines)
               .replace("FAULTS", fault_list).replace("FACTORY_BAD", "".join(f"{b}, " for b in factory_bad))
               .replace("WP", "true" if wp else "false").replace("SLOT", slot)
               .replace("IMAGE_BLOCKS", str(self.IMAGE_BLOCKS)))
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

    def assertWritten(self, r, marks):
        self.assertEqual((r["rc"], r["marks"], r["image"], r["tail"]), ("0", marks, "1", "1"), r["log"])

    def assertRefused(self, r, marks=()):
        self.assertEqual((r["rc"], r["marks"]), ("-5", list(marks)), r["log"])

    def test_a_clean_slot(self):
        for slot in ("RT_PING_ROOTFS", "RT_PONG_ROOTFS"):
            with self.subTest(slot=slot):
                self.assertWritten(self.run_write(slot=slot), [])

    def test_factory_bad_blocks_are_skipped_and_not_counted(self):
        self.assertWritten(self.run_write(factory_bad=(0, 2, 3, 4), faults=[("w", 6, 0, "EIO"), ("e", 7, 0, "EIO")]), [6, 7])

    def test_a_block_that_fails_to_erase_is_marked(self):
        r = self.run_write(faults=[("e", 1, 0, "EIO")])
        self.assertWritten(r, [1])
        self.assertIn("NAND.File-System-Pong block 1 (0x6ca0000) marked bad: erase failed (-5), 1 of 2", r["log"])

    def test_a_block_that_fails_to_program_moves_to_the_next(self):
        # Page 10 of block 3 fails; the whole image block goes into block 4 from its first page.
        r = self.run_write(faults=[("w", 3, 10 * 0x800, "EIO")], slot="RT_PING_ROOTFS")
        self.assertWritten(r, [3])
        self.assertIn("NAND.File-System-Ping block 3 (0xce0000) marked bad: write failed (-5)", r["log"])

    def test_the_block_after_a_failed_one_can_fail_too(self):
        self.assertWritten(self.run_write(faults=[("w", 3, 0, "EIO"), ("e", 4, 0, "EIO")]), [3, 4])

    def test_a_block_after_the_image_that_does_not_erase_is_marked(self):
        # It could hold a volume header of the old image; marked bad, UBI never reads it.
        self.assertWritten(self.run_write(faults=[("e", self.IMAGE_BLOCKS + 5, 0, "EIO")]), [self.IMAGE_BLOCKS + 5])

    def test_marking_stops_at_two(self):
        r = self.run_write(faults=[("w", 3, 0, "EIO"), ("w", 4, 0, "EIO"), ("e", 5, 0, "EIO")])
        self.assertRefused(r, [3, 4])
        self.assertIn("NAND.File-System-Pong block 5: erase failed (-5); 2 blocks marked bad already, the most "
                      "allowed: not marked, nothing selected", r["log"])
        r = self.run_write(faults=[("e", 1, 0, "EIO"), ("e", 30, 0, "EIO"), ("e", 700, 0, "EIO")])
        self.assertRefused(r, [1, 30])

    def test_a_write_protected_chip_marks_nothing(self):
        # Every erase fails with -EIO, but the NAND names no block: fail_addr stays unknown.
        r = self.run_write(wp=True)
        self.assertRefused(r)
        self.assertIn("block 0: erase failed (-30), not a NAND failure; not marked bad", r["log"])

    def test_a_failure_that_is_not_the_nands_marks_nothing(self):
        for op in ("e", "w"):
            with self.subTest(op=op):
                self.assertRefused(self.run_write(faults=[(op, 2, 0, "ETIMEDOUT")]))

    def test_a_readback_that_fails_or_differs_marks_nothing(self):
        for fault in (("r", 2, 0, "EBADMSG"), ("c", 2, 0, "EIO")):
            with self.subTest(fault=fault):
                self.assertRefused(self.run_write(faults=[fault]))
        # A corrected read is a good read.
        self.assertWritten(self.run_write(faults=[("r", 2, 0, "EUCLEAN")]), [])

    def test_a_mark_that_does_not_take_stops_the_write(self):
        r = self.run_write(faults=[("e", 2, 0, "EIO"), ("m", 2, 0, "EROFS")])
        self.assertRefused(r)
        self.assertIn("marking it bad failed (-30)", r["log"])

    def test_the_limit_is_the_installers(self):
        """One limit in two C sources that never link together, read from both."""
        mtd_h = (ROOT / "os/installer/module/mtd.h").read_text()
        per_part = int(re.search(r"^#define MTD_MARK_BAD_PER_PARTITION (\d+)$", mtd_h, re.M).group(1))
        self.assertEqual(define("RT_MARK_BAD_MAX"), per_part)


if __name__ == "__main__":
    unittest.main()
