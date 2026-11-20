#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("product_spl", ROOT / "os/support/check_wla_product_spl.py")
product_spl = importlib.util.module_from_spec(spec)
spec.loader.exec_module(product_spl)


def stu_with_mlo(mlo: bytes) -> bytes:
    header = bytearray(product_spl.stu_check.HDR)
    header[0x18:0x24] = b"soundtap-spl"
    header[product_spl.stu_check.SIZE_OFF:product_spl.stu_check.SIZE_OFF + 4] = (len(mlo) + 2 * product_spl.stu_check.OVERLAP).to_bytes(4, "big")
    return bytes(header) + mlo + b"\xff" * (2 * product_spl.stu_check.OVERLAP)


class ProductSplProvenanceTest(unittest.TestCase):
    def test_measured_rom_ceiling(self):
        self.assertEqual(product_spl.check_rom_ceiling(0x7428, 15 * 2048), 15)
        with self.assertRaisesRegex(SystemExit, "GP payload 0x7429"):
            product_spl.check_rom_ceiling(0x7429, 15 * 2048)
        with self.assertRaisesRegex(SystemExit, "needs 16 NAND pages"):
            product_spl.check_rom_ceiling(0x7428, 15 * 2048 + 1)

    def test_stu_mlo_and_provenance_must_match_product_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            mlo, config = b"\x0f\0\0\xeaMLO", b"CONFIG_SPL_NAND_INIT=y\n"
            (out / "MLO").write_bytes(mlo)
            (out / ".config").write_bytes(config)
            (out / "Update-wla.stu").write_bytes(stu_with_mlo(mlo))
            provenance = f"mlo_sha256={hashlib.sha256(mlo).hexdigest()}\nconfig_sha256={hashlib.sha256(config).hexdigest()}\n"
            product_spl.check_packaged_mlo(out, out / "Update-wla.stu", provenance)
            (out / "Update-wla.stu").write_bytes(stu_with_mlo(mlo + b"!"))
            with self.assertRaisesRegex(SystemExit, "STU MLO payload"):
                product_spl.check_packaged_mlo(out, out / "Update-wla.stu", provenance)


if __name__ == "__main__":
    unittest.main()
