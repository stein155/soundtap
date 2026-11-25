# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The WLA's lab U-Boot for RAM boots: its builder, patches and check_wla_uboot_ram.py."""
import os
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest
import zlib
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CHECK = ROOT / "os/support/check_wla_uboot_ram.py"
DTS_CHECK = ROOT / "os/support/check_wla_uboot_dts.py"
BOOTCOMMAND = __import__("runpy").run_path(CHECK)["BOOTCOMMAND"]
CONFIG = """CONFIG_DEFAULT_DEVICE_TREE="am335x-soundtap-wla"
CONFIG_SPL=y
CONFIG_SPL_NET=y
CONFIG_SPL_USB_GADGET=y
CONFIG_SPL_USB_ETHER=y
CONFIG_USB_ETHER=y
CONFIG_USB_ETH_RNDIS=y
CONFIG_CMD_DHCP=y
CONFIG_SPL_TEXT_BASE=0x402f0400
CONFIG_SPL_SIZE_LIMIT=0xfc00
CONFIG_SPL_SYS_MALLOC_F_LEN=0x2000
# CONFIG_SPL_SYS_MALLOC is not set
CONFIG_USB_GADGET_DOWNLOAD=y
CONFIG_USB_FUNCTION_ACM=y
CONFIG_CMD_NAND=y
CONFIG_CMD_MTDPARTS=y
CONFIG_MTD_PARTITIONS=y
CONFIG_CMD_UBI=y
CONFIG_CMD_UBIFS=y
CONFIG_MTD_UBI=y
CONFIG_SYS_PROMPT="WLA> "
CONFIG_BOOTDELAY=0
CONFIG_ENV_IS_NOWHERE=y
# CONFIG_ENV_IS_IN_NAND is not set
CONFIG_BOOTCOMMAND="%s"
"""


class WlaUBootRamCheckTest(unittest.TestCase):
    """The lab U-Boot's builder, patches and checker helpers."""

    def test_builder_accepts_valid_archive_revision_and_rejects_invalid(self) -> None:
        """Archive builds have no .git, so supplied revision must be validated first."""
        with tempfile.TemporaryDirectory() as tmp:
            archive = Path(tmp) / "archive"
            builder = archive / "os/bootloader/build-wla-uboot.sh"
            builder.parent.mkdir(parents=True)
            shutil.copy2(ROOT / "os/bootloader/build-wla-uboot.sh", builder)
            fake_docker = Path(tmp) / "docker"
            fake_docker.write_text('#!/bin/sh\ncase "$1" in build) exit 0;; image) printf fake;; run) exit 47;; esac\n')
            fake_docker.chmod(0o755)
            env = {**os.environ, "PATH": f"{tmp}:{os.environ['PATH']}",
                   "SOUNDTAP_REPO_REVISION": "a" * 40,
                   "SOURCE_DATE_EPOCH": "1784143890"}
            result = subprocess.run(["sh", "os/bootloader/build-wla-uboot.sh"], check=False, cwd=archive,
                                    env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode, 47, result.stderr)
            self.assertNotIn("fatal: not a git repository", result.stderr)

            env["SOUNDTAP_REPO_REVISION"] = "a" * 39
            result = subprocess.run(["sh", "os/bootloader/build-wla-uboot.sh"], check=False, cwd=archive,
                                    env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode, 2)
            self.assertIn("SOUNDTAP_REPO_REVISION must be exactly 40 hexadecimal characters", result.stderr)

    def test_builder_selects_the_legacy_handoff(self) -> None:
        """The SPL hands over to a legacy uImage at 0x80100000, and the build stops when olddefconfig drops the parser.

        Read as text: these are Kconfig switches inside the Docker cross build, which only a
        full U-Boot build executes; the checker run below holds the built result to the same
        addresses.
        """
        builder = (ROOT / "os/bootloader/build-wla-uboot.sh").read_text()
        self.assertIn("--disable SPL_LOAD_FIT --enable SPL_LEGACY_IMAGE_FORMAT", builder)
        self.assertEqual(builder.count("--enable SPL_LEGACY_IMAGE_FORMAT"), 1)
        self.assertNotIn("--disable SPL_LEGACY_IMAGE_FORMAT", builder)
        self.assertLess(builder.index("make olddefconfig < /dev/null"),
                        builder.index('grep -qx "CONFIG_SPL_LEGACY_IMAGE_FORMAT=y" .config'))
        self.assertIn("--set-val TEXT_BASE 0x80100000", builder)
        self.assertIn("-a 0x80100000 -e 0x80100000", builder)

    def test_builder_registers_dts_after_final_config(self) -> None:
        """Build wiring inside the Docker cross build, read as text for the same reason."""
        builder = (ROOT / "os/bootloader/build-wla-uboot.sh").read_text()
        dts = (ROOT / "os/bootloader/uboot/uboot-am335x-soundtap-wla.dts").read_text()
        self.assertIn("--disable OF_UPSTREAM", builder)
        self.assertLess(builder.index("make olddefconfig"), builder.index("cp /repo/os/bootloader/uboot/uboot-am335x-soundtap-wla.dts"))
        self.assertIn("check_wla_uboot_dts.py .", builder)
        self.assertIn("make dtbs", builder)
        self.assertIn("dd if=MLO of=/out/MLO bs=1 skip=520 status=none", builder)
        self.assertNotIn("--enable UBIFS", builder)
        self.assertIn("--disable EFI_LOADER --disable BOOTSTD --disable DISTRO_DEFAULTS", builder)
        self.assertIn("--disable SPI_FLASH --disable SPI", builder)
        self.assertIn("--set-val SYS_TEXT_BASE 0x80100000 --set-val TEXT_BASE 0x80100000", builder)
        self.assertIn("cp u-boot u-boot.map u-boot-dtb.bin", builder)
        self.assertIn("-T firmware -C none", builder)
        self.assertIn("-d u-boot-dtb.bin /out/u-boot.img", builder)
        self.assertIn('&usb0 {\n\tdr_mode = "peripheral";', dts)

    def test_post_env_reset_is_opt_in_and_immediately_after_env(self) -> None:
        """The post-env reset is opt-in and comes right after initr_env.

        Builder wiring and a patch only the cross build applies, read as text; the checker
        run below holds the built result.
        """
        builder = (ROOT / "os/bootloader/build-wla-uboot.sh").read_text()
        patch = (ROOT / "os/bootloader/uboot/wla-uboot-post-env-reset.patch").read_text()
        self.assertIn("POST_ENV_RESET=${WLA_UBOOT_POST_ENV_RESET:-}", builder)
        self.assertIn("git apply --unidiff-zero /repo/os/bootloader/uboot/wla-uboot-post-env-reset.patch", builder)
        self.assertIn("+\tINITCALL(initr_env);\n+\treset_cpu();", patch)

    def test_watchdog_stays_a_separate_knob(self) -> None:
        """The watchdog has its own knob, not bundled with the NAND/MMC removal.

        The watchdog is the only negative indicator this board gives; disabling it alone is
        the control for whether a reset came from it. Builder wiring, read as text; the
        checker run holds the gate.
        """
        builder = (ROOT / "os/bootloader/build-wla-uboot.sh").read_text()
        self.assertIn("NO_WATCHDOG=${WLA_UBOOT_NO_WATCHDOG:-}", builder)
        self.assertIn("--disable WATCHDOG_AUTOSTART", builder)
        self.assertIn("WLA_UBOOT_NO_WATCHDOG needs exactly one reset checkpoint", builder)

    def test_rejects_wrong_dts_path_or_config(self) -> None:
        """The DTS check passes a tree with OF_UPSTREAM off and refuses one without that line."""
        with tempfile.TemporaryDirectory() as temporary:
            tree = Path(temporary)
            dts = tree / "arch/arm/dts"
            dts.mkdir(parents=True)
            (tree / ".config").write_text(
                'CONFIG_DEFAULT_DEVICE_TREE="am335x-soundtap-wla"\n# CONFIG_OF_UPSTREAM is not set\n')
            (dts / "am335x-soundtap-wla.dts").write_text("/dts-v1/;\n")
            (dts / "Makefile").write_text("dtb-y += am335x-soundtap-wla.dtb\n")
            subprocess.run(["python3", DTS_CHECK, tree], check=True)
            (tree / ".config").write_text('CONFIG_DEFAULT_DEVICE_TREE="am335x-soundtap-wla"\n')
            self.assertNotEqual(subprocess.run(["python3", DTS_CHECK, tree], check=False).returncode, 0)

    def test_bringup_patch_keeps_its_ddr_init(self) -> None:
        """The lab SPL's DDR bring-up keeps its values.

        It lives in a U-Boot patch that only the cross build compiles and only the board
        runs, so its values are read as text.
        """
        patch = (ROOT / "os/bootloader/uboot/wla-am335x-bringup.patch").read_text()
        self.assertIn("unsigned int timeout = 100000", patch)
        self.assertIn("static void config_vtp", patch)
        self.assertIn("config_ddr(303, &ioregs_evmsk, fast_bin ? &fast : &slow, &cmd, &emif, 0);", patch)
        self.assertIn("CM_CLKMODE_DPLL_DPLL_EN_MASK", patch)
        self.assertIn("for (volatile unsigned int delay = 0; delay < 300000; delay++)", patch)
        self.assertIn("fast_bin", patch)
        # The MPU runs at the 600 MHz every board is fused for, not the ROM's 500.
        self.assertIn("(600 << CM_CLKSEL_DPLL_M_SHIFT)", patch)
        self.assertIn("writel(temp, CM_WKUP + 0x2c);", patch)

    def test_spl_pid_hunk_keeps_u_boot_product_fallback(self) -> None:
        """A patch only the cross build applies, so its structure is read as text."""
        # The lab SPL hardcodes the ROM PID so the loader can find stage 2; U-Boot proper must
        # keep its configured PID. Assert that structure, not one literal line: the hunk nests
        # the original fallback inside #else, so the CONFIG_USB_GADGET_* line stays an
        # unchanged context line and asserting "#elif" here went stale.
        patch = (ROOT / "os/bootloader/uboot/wla-am335x-bringup.patch").read_text()
        self.assertIn("+#if defined(CONFIG_XPL_BUILD)", patch)
        self.assertIn("__constant_cpu_to_le16(0x6141)", patch)
        xpl = patch.index("+#if defined(CONFIG_XPL_BUILD)")
        rom_pid = patch.index("__constant_cpu_to_le16(0x6141)", xpl)
        alt = patch.index("+#else", rom_pid)
        fallback = patch.index(
            " #if defined(CONFIG_USB_GADGET_VENDOR_NUM) && defined(CONFIG_USB_GADGET_PRODUCT_NUM)",
            alt)
        self.assertLess(rom_pid, alt)
        self.assertLess(alt, fallback)
        self.assertIn("__constant_cpu_to_le16(CONFIG_USB_GADGET_VENDOR_NUM)", patch)
        self.assertIn("+#endif\n #endif\n \t\tsprintf(product_desc", patch)

    def test_lab_spl_keeps_simple_malloc_disabled_with_8k_early_pool(self) -> None:
        """The checker requires the 8 KiB early pool and refuses SPL_SYS_MALLOC."""
        values = __import__("runpy").run_path(CHECK)
        values["require"](CONFIG, "CONFIG_SPL_SYS_MALLOC_F_LEN=0x2000")
        values["reject"](CONFIG, "CONFIG_SPL_SYS_MALLOC=y")

    def test_bootcommand_is_inert(self) -> None:
        """The checker's expected boot command does nothing."""
        self.assertEqual(BOOTCOMMAND, "true")

    def test_product_stu_selects_fixed_ubi_boot_chain(self) -> None:
        """The product STU selects the fixed UBI boot chain.

        Build wiring across the image steps, the U-Boot builder and its checker; only a
        full build runs them, so they are read as text.
        """
        builder = (ROOT / "os/bootloader/build-wla-uboot.sh").read_text()
        checker = (ROOT / "os/support/check_wla_uboot_nand.py").read_text()
        post_build = (ROOT / "os/board/image/post-build.sh").read_text()
        # uImage-binky is built from zImage alone, not copied from $BINARIES_DIR/uImage:
        # that one carries an appended device tree whose own bootargs would win over the
        # slot U-Boot chose (CONFIG_ARM_APPENDED_DTB=y).
        self.assertIn('-d "$BINARIES_DIR/zImage" "$target/boot/uImage-rhino"', post_build)
        self.assertIn('ln -f "$target/boot/uImage-rhino" "$target/boot/uImage-$variant"', post_build)
        self.assertIn("for variant in binky ginger; do", post_build)
        self.assertIn("ubi part NAND.File-System-Pong", builder)
        self.assertIn("ubifsload 0x82000000 /boot/uImage-${rt_v}", builder)
        self.assertIn("ubifsload 0x83000000 /boot/${rt_v}_${rt_m}.dtb", builder)
        board = (ROOT / "os/bootloader/uboot/uboot-2026.07-scm-nand-wp.patch").read_text()
        self.assertIn('hextoul(raw, NULL) >= 0x3100 ? "rhino" : "binky"', board)
        self.assertIn("check_wla_uboot_nand.py", builder)
        self.assertIn("CONFIG_ENV_IS_NOWHERE=y", checker)
        self.assertIn('b"fixsig"', checker)
        self.assertIn("} > /out/PROVENANCE", builder)
        self.assertIn('printf "source_sha256=', builder)
        self.assertIn('printf "config_sha256=', builder)

    def test_legacy_uimage_rejects_bad_magic_load_and_dtb(self) -> None:
        """check_legacy_uimage accepts a good header and refuses a bad magic or load address."""
        check = __import__("runpy").run_path(CHECK)["check_legacy_uimage"]
        payload = b"soundtap-nand-pins"
        header = bytearray(64)
        __import__("struct").pack_into(">7I4B32s", header, 0, 0x27051956, 0, 0,
                                        len(payload), 0x80100000, 0x80100000,
                                        zlib.crc32(payload), 17, 2, 5, 0, b"WLA")
        __import__("struct").pack_into(">I", header, 4, zlib.crc32(header))
        check(bytes(header) + payload, payload)
        header[0] ^= 1
        with self.assertRaisesRegex(SystemExit, "legacy uImage"):
            check(bytes(header) + payload, payload)
        header[0] ^= 1
        __import__("struct").pack_into(">I", header, 16, 0x80800000)
        __import__("struct").pack_into(">I", header, 4, 0)
        __import__("struct").pack_into(">I", header, 4, zlib.crc32(header))
        with self.assertRaisesRegex(SystemExit, "load/entry"):
            check(bytes(header) + payload, payload)

    def test_link_checker_rejects_elf_map_or_header_mismatch(self) -> None:
        """check_uboot_link accepts matching ELF, map and header and refuses a map that differs."""
        check = __import__("runpy").run_path(CHECK)["check_uboot_link"]
        elf = bytearray(52)
        elf[:6] = b"\x7fELF\x01\x01"
        __import__("struct").pack_into("<I", elf, 24, 0x80100000)
        payload = b"x"
        header = bytearray(64)
        __import__("struct").pack_into(">7I4B32s", header, 0, 0x27051956, 0, 0, 1,
                                        0x80100000, 0x80100000, zlib.crc32(payload),
                                        17, 2, 5, 0, b"WLA")
        __import__("struct").pack_into(">I", header, 4, zlib.crc32(header))
        check(bytes(elf), "  80100000 __image_copy_start\n", bytes(header) + payload)
        with self.assertRaisesRegex(SystemExit, "ELF/map link"):
            check(bytes(elf), "  80800000 __image_copy_start\n", bytes(header) + payload)


def fdt_usb0(dr_mode: str) -> bytes:
    """Build a flattened device tree with one node, usb@47401000, and its dr_mode."""
    strings = b"dr_mode\0"
    value = dr_mode.encode() + b"\0"

    def pad(data: bytes) -> bytes:
        return data + b"\0" * (-len(data) % 4)

    structure = (struct.pack(">I", 1) + pad(b"\0")
                 + struct.pack(">I", 1) + pad(b"usb@47401000\0")
                 + struct.pack(">III", 3, len(value), 0) + pad(value)
                 + struct.pack(">III", 2, 2, 9))
    total = 40 + len(structure) + len(strings)
    return struct.pack(">10I", 0xD00DFEED, total, 40, 40 + len(structure), 0, 17, 16, 0,
                       len(strings), len(structure)) + structure + strings


def uimage(payload: bytes, load: int = 0x80100000) -> bytes:
    """Wrap payload in a legacy uImage header loading and entering at load."""
    header = bytearray(64)
    struct.pack_into(">7I4B32s", header, 0, 0x27051956, 0, 0, len(payload), load, load,
                     zlib.crc32(payload), 17, 2, 5, 0, b"WLA")
    struct.pack_into(">I", header, 4, zlib.crc32(header))
    return bytes(header) + payload


GOOD_CONFIG = """CONFIG_DEFAULT_DEVICE_TREE="am335x-soundtap-wla"
CONFIG_SPL=y
CONFIG_SPL_NET=y
CONFIG_SPL_USB_GADGET=y
CONFIG_SPL_USB_ETHER=y
CONFIG_SPL_LEGACY_IMAGE_FORMAT=y
CONFIG_TEXT_BASE=0x80100000
CONFIG_SPL_TEXT_BASE=0x402f0400
CONFIG_SPL_SIZE_LIMIT=0xfc00
CONFIG_SPL_SYS_MALLOC_F_LEN=0x2000
# CONFIG_SPL_SYS_MALLOC is not set
CONFIG_USB_ETHER=y
CONFIG_USB_ETH_RNDIS=y
CONFIG_CMD_DHCP=y
CONFIG_SYS_PROMPT="WLA> "
CONFIG_BOOTDELAY=0
CONFIG_ENV_IS_NOWHERE=y
# CONFIG_ENV_IS_IN_NAND is not set
CONFIG_BOOTCOMMAND="true"
CONFIG_CMD_NAND=y
CONFIG_CMD_MTDPARTS=y
CONFIG_MTD_PARTITIONS=y
CONFIG_CMD_UBI=y
CONFIG_CMD_UBIFS=y
CONFIG_MTD_UBI=y
CONFIG_WATCHDOG_AUTOSTART=y
CONFIG_USB_GADGET_DOWNLOAD=y
CONFIG_USB_FUNCTION_ACM=y
"""
GOOD_SYMBOLS = """402f0500 T spl_parse_legacy_header
402f0600 T s_init
402f0700 T board_init_f
402f0800 T sdram_init
402f0900 T spl_net_load_image_usb
"""


@dataclass(frozen=True)
class FakeBuild:
    """What a faked build output holds where a test changes it."""

    config: str = GOOD_CONFIG
    symbols: str = GOOD_SYMBOLS
    mlo_size: int = 0x8000
    load: int = 0x80100000
    uboot_disasm: str = "<board_nand_pinmux>:\n"


class CheckerRunTest(unittest.TestCase):
    """check_wla_uboot_ram.py run against the output of a build, faked file by file.

    The good tree passes, and each property the RAM proof rests on fails it on its own.
    """

    def check(self, env: dict[str, str] | None = None, **build: str | int) -> tuple[int, str]:
        """Run the checker on a faked build with env and these FakeBuild fields; return (exit code, output)."""
        fake = FakeBuild(**build)
        config, symbols, mlo_size, load, uboot_disasm = fake.config, fake.symbols, fake.mlo_size, fake.load, fake.uboot_disasm
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            payload = b"soundtap-nand-pins CDC Ethernet RNDIS " + fdt_usb0("peripheral")
            elf = bytearray(52)
            elf[:6] = b"\x7fELF\x01\x01"
            struct.pack_into("<I", elf, 24, 0x80100000)
            files = {
                ".config": config.encode(), "MLO": b"\x0f\x00\x00\xea".ljust(mlo_size, b"\0"),
                "u-boot.img": uimage(payload, load), "u-boot-dtb.bin": payload,
                "u-boot": bytes(elf), "u-boot.map": b"  80100000 __image_copy_start\n",
                "spl-symbols.txt": symbols.encode(),
                "spl-disasm.txt": b"<spl_relocate_stack_gd>:\n  mov r0, #0x100000\n\n",
                "u-boot-disasm.txt": uboot_disasm.encode(), "spl-size.txt": b".text .bss\n",
            }
            for name, data in files.items():
                (out / name).write_bytes(data)
            clean = {k: v for k, v in os.environ.items()
                     if not k.startswith(("WLA_UBOOT_", "ST10_UBOOT_"))}
            # The MLO here is not the one rom-usb-loader.c pins; the pin is a hash, not a property.
            clean.update(WLA_UBOOT_LEGACY_SPL="1", **(env or {}))
            done = subprocess.run([sys.executable, "-B", str(CHECK), str(out)], check=False, env=clean,
                                  capture_output=True, text=True)
            return done.returncode, done.stdout + done.stderr

    def test_a_good_build_passes(self) -> None:
        """A good build passes."""
        code, out = self.check()
        self.assertEqual(code, 0, out)
        self.assertIn("WLA U-Boot RAM properties passed", out)

    def test_the_spl_must_parse_a_legacy_uimage(self) -> None:
        """An SPL without the legacy uImage format or its parser fails."""
        code, out = self.check(config=GOOD_CONFIG.replace("CONFIG_SPL_LEGACY_IMAGE_FORMAT=y\n", ""))
        self.assertEqual((code, out.strip()), (1, "missing CONFIG_SPL_LEGACY_IMAGE_FORMAT=y"))
        code, out = self.check(symbols=GOOD_SYMBOLS.replace("spl_parse_legacy_header", "other"))
        self.assertEqual((code, out.strip()), (1, "SPL legacy uImage parser is absent"))

    def test_uboot_loads_where_the_spl_jumps(self) -> None:
        """A U-Boot that loads elsewhere than the SPL jumps fails."""
        code, out = self.check(load=0x80800000)
        self.assertEqual(code, 1)
        self.assertIn("load/entry", out)

    def test_the_spl_fits_the_rom_window(self) -> None:
        """An SPL past SRAM or with a pre-DDR symbol outside the ROM window fails."""
        code, out = self.check(mlo_size=0xFC01)
        self.assertEqual(code, 1)
        self.assertIn("exceeds SRAM limit", out)
        code, out = self.check(symbols=GOOD_SYMBOLS.replace("402f0800 T sdram_init",
                                                            "40300000 T sdram_init"))
        self.assertEqual((code, out.strip()), (1, "SPL pre-DDR symbol outside ROM window: sdram_init"))

    def test_the_watchdog_follows_its_knob(self) -> None:
        """The watchdog is off exactly when its knob asks for it."""
        code, out = self.check(env={"WLA_UBOOT_NO_WATCHDOG": "1"})
        self.assertEqual((code, out.strip()), (1, "missing # CONFIG_WATCHDOG_AUTOSTART is not set"))
        quiet = GOOD_CONFIG.replace("CONFIG_WATCHDOG_AUTOSTART=y", "# CONFIG_WATCHDOG_AUTOSTART is not set")
        self.assertEqual(self.check(env={"WLA_UBOOT_NO_WATCHDOG": "1"}, config=quiet)[0], 0)
        self.assertEqual(self.check(config=quiet)[0], 1)

    def test_post_env_reset_must_be_in_board_init_r(self) -> None:
        """With the post-env reset on, board_init_r must call reset_cpu."""
        env = {"WLA_UBOOT_POST_ENV_RESET": "1"}
        code, out = self.check(env=env)
        self.assertEqual((code, out.strip()), (1, "post-env reset call absent from board_init_r"))
        disasm = "<board_nand_pinmux>:\n\n<board_init_r>:\n  bl <reset_cpu>\n"
        self.assertEqual(self.check(env=env, uboot_disasm=disasm)[0], 0)


if __name__ == "__main__":
    unittest.main()
