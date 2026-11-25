# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The WLA product SPL: its builder and check_wla_product_spl.py against a faked build output."""
import contextlib
import hashlib
import importlib.util
import io
import struct
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("product_spl", ROOT / "os/support/check_wla_product_spl.py")
product_spl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(product_spl)

DISASSEMBLY = """<spl_nand_load_image>:
  bl <nand_init>
  bl <spl_nand_load_element>

<spl_nand_load_element>:
  bl <spl_parse_image_header>

"""
SYMBOLS = "402f1000 T spl_nand_load_image\n402f2000 T spl_parse_image_header\n"


def product_out(out: Path, config: str | None = None, symbols: str = SYMBOLS, disassembly: str = DISASSEMBLY, spl_size: int = 0x6000) -> None:
    """Write what build-wla-product-spl.sh leaves behind, with an SPL of spl_size bytes."""
    spl = bytes.fromhex("0f0000ea").ljust(spl_size, b"\0")
    ch = bytearray(b"\0" * product_spl.GP_HEADER_OFFSET)
    ch[:4] = bytes.fromhex("40000000")
    ch[20:30] = b"CHSETTINGS"
    mlo = bytes(ch) + struct.pack("<II", len(spl) + 8, product_spl.SPL_LOAD_ADDRESS) + spl
    config = "\n".join(product_spl.REQUIRED_CONFIG) + "\n" if config is None else config
    files = {
        "MLO": mlo, "u-boot-spl.bin": spl, ".config": config.encode(),
        "MLO.sha256": f"{hashlib.sha256(mlo).hexdigest()}  MLO\n".encode(),
        "spl-symbols.txt": symbols.encode(), "spl-disassembly.txt": disassembly.encode(),
        "u-boot-spl.map": b"map\n", "spl-size.txt": b".text .bss\n",
        "PROVENANCE": (b"source_revision=ece349ade2973e220f524ce59e59711cc919263f\n"
                       b"source_sha256=78e8bfc382fe388f9b55aa1daf8c563522a037779b5d4c349d1415e381f1243e\n"
                       b"container=debian:bookworm-slim@sha256:0\nconfig_sha256=0\nmlo_sha256=0\n"),
    }
    for name, data in files.items():
        (out / name).write_bytes(data)


class ProductSplTest(unittest.TestCase):
    """The product SPL's builder."""

    def test_nand_marker_diagnostic_static_contract(self) -> None:
        """The NAND marker check passes on the cached U-Boot tree that only a WLA SPL build leaves behind."""
        if not (ROOT / "os/bootloader/build/cache-wla-uboot/u-boot-2026.07").is_dir():
            self.skipTest("no cached WLA U-Boot tree; run build-wla-product-spl.sh first")
        subprocess.run(
            ["python3", str(ROOT / "os/support/check_wla_product_spl_markers.py")],
            check=True,
        )

    def test_builder_is_nand_only(self) -> None:
        """The builder asks for a NAND-only SPL.

        Kconfig switches inside the Docker cross build, which only a full U-Boot build
        executes, so they are read as text; the checker run below holds the built result
        to the NAND-only boundary. CONFIG_SPL_NAND_INIT is asked for but not required of
        the result: it does not survive into a 2026.07 .config.
        """
        builder = (ROOT / "os/bootloader/build-wla-product-spl.sh").read_text()
        self.assertIn("VERSION=2026.07", builder)
        self.assertIn("--enable SPL_NAND_IDENT --enable SPL_NAND_AM33XX_BCH", builder)
        self.assertIn("--enable SPL_NAND_INIT --enable SPL_LEGACY_IMAGE_FORMAT", builder)
        self.assertIn("--disable SPL_USB_GADGET --disable SPL_USB_ETHER --disable SPL_NET", builder)
        self.assertIn("--disable SPL_MMC --disable MMC --disable USB --disable USB_GADGET --disable NET", builder)
        self.assertIn("--set-str DEFAULT_DEVICE_TREE am335x-soundtap-wla", builder)
        self.assertIn("--set-val SPL_TEXT_BASE 0x402f0400 --set-val SPL_SIZE_LIMIT 0x7420", builder)
        self.assertIn("CONFIG_USB_MAX_CONTROLLER_COUNT=0", builder)


class ProductSplCheckerRun(unittest.TestCase):
    """check_wla_product_spl.py against a faked build output.

    The good tree passes, and a lab loader, a NAND loader that does not parse the U-Boot
    header or an SPL past the ROM's measured ceiling each fail it.
    """

    def check(self, **kwargs: str | int | None) -> str:
        """Run the checker on a faked build output made with these product_out arguments; return its verdict."""
        with tempfile.TemporaryDirectory() as tmp:
            product_out(Path(tmp), **kwargs)
            try:
                with contextlib.redirect_stdout(io.StringIO()):
                    product_spl.main(Path(tmp))
            except SystemExit as stop:
                return str(stop)
            return "passed"

    def test_a_nand_only_spl_passes(self) -> None:
        """A NAND-only SPL passes."""
        self.assertEqual(self.check(), "passed")

    def test_lab_loaders_fail(self) -> None:
        """A lab loader symbol or lab config option fails."""
        for symbol in ("spl_net_load_image_usb", "spl_usb_load", "spl_mmc_load"):
            with self.subTest(symbol):
                self.assertIn("SPL retains lab loader", self.check(symbols=SYMBOLS + f"402f3000 T {symbol}\n"))
        lab = "\n".join(product_spl.REQUIRED_CONFIG) + "\nCONFIG_SPL_NET=y\n"
        self.assertEqual(self.check(config=lab), "unexpected lab feature CONFIG_SPL_NET=y")

    def test_the_nand_loader_must_init_nand_and_parse_the_header(self) -> None:
        """A NAND loader that does not init NAND or parse the header, or no NAND loader, fails."""
        self.assertEqual(self.check(disassembly=DISASSEMBLY.replace("  bl <nand_init>\n", "")),
                         "NAND loader does not call <nand_init>")
        self.assertEqual(self.check(disassembly=DISASSEMBLY.replace("  bl <spl_parse_image_header>\n", "")),
                         "NAND loader does not parse U-Boot image header")
        self.assertEqual(self.check(symbols=SYMBOLS.replace(" spl_nand_load_image", " other")),
                         "SPL lacks NAND U-Boot loader")

    def test_an_spl_past_the_rom_ceiling_fails(self) -> None:
        """An SPL past the ROM's measured ceiling fails."""
        self.assertIn("exceeds measured stock ROM ceiling", self.check(spl_size=product_spl.ROM_GP_SIZE_LIMIT))


if __name__ == "__main__":
    unittest.main()
