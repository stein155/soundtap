# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import importlib.util
import hashlib
import random
import struct
import tempfile
import unittest
import zlib
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[3]

SPEC = importlib.util.spec_from_file_location("bstu", ROOT / "os/installer/build-stu.py")
bstu = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(bstu)
CHECK_SPEC = importlib.util.spec_from_file_location("checker", ROOT / "os/support/stu_check.py")
checker = importlib.util.module_from_spec(CHECK_SPEC)
CHECK_SPEC.loader.exec_module(checker)


def header(name, image):
    h = bytearray(bstu.HDR)
    h[0x18:0x18 + len(name)] = name.encode()
    struct.pack_into(">I", h, bstu.SIZE_OFF, len(image))
    struct.pack_into(">I", h, bstu.IMGCRC_OFF, zlib.crc32(image) & 0xFFFFFFFF)
    struct.pack_into(">I", h, bstu.PKG_HCRC2_OFF,
                     zlib.crc32(h[0x14:bstu.PKG_HCRC2_OFF]) & 0xFFFFFFFF)
    return h


def valid_stu(module_names=bstu.EXPECTED_MODULES, payloads=None):
    payloads = payloads or {}
    data = bytearray()
    for name in reversed(module_names):
        payload = payloads.get(name, (name.encode() + b"x" * 32)[:32])
        image = payload + data[:bstu.OVERLAP] if data else payload
        data = header(name, image) + payload + data
    struct.pack_into(">I", data, bstu.PKG_SIZE_OFF, len(data))
    data[4:8] = b"RTCH" if module_names == bstu.WLA_MODULES else b"BOSE"
    struct.pack_into(">I", data, bstu.PKG_HCRC_OFF,
                     zlib.crc32(data[:bstu.PKG_HCRC_OFF]) & 0xFFFFFFFF)
    return data


def valid_mlo(payload=b"spl"):
    return struct.pack("<II", len(payload), 0x402F0400) + payload


def valid_uboot(payload=b"u-boot"):
    header = bytearray(64)
    struct.pack_into(">IIIIIII", header, 0, 0x27051956, 0, 0, len(payload),
                     0x80800000, 0x80800000, zlib.crc32(payload) & 0xFFFFFFFF)
    struct.pack_into(">I", header, 4, zlib.crc32(header) & 0xFFFFFFFF)
    return bytes(header) + payload


def valid_kernel(model):
    payload = b"kernel"
    image = bytearray(64 + len(payload))
    struct.pack_into(">IIIIIII", image, 0, 0x27051956, 0, 0, len(payload),
                     0x80008000, 0x80008000, zlib.crc32(payload) & 0xFFFFFFFF)
    name = f"Linux-6.12 SoundTap {model}".encode()
    image[32:32 + len(name)] = name
    image[64:] = payload
    header_bytes = bytearray(image[:64])
    struct.pack_into(">I", header_bytes, 4, 0)
    struct.pack_into(">I", image, 4, zlib.crc32(header_bytes) & 0xFFFFFFFF)
    return image


class ValidateSTUTest(unittest.TestCase):
    def test_extract_ubi_excludes_next_header_overlap(self):
        ubi = b"UBI#" + b"x" * (checker.UBI_PEB - 4)
        data = valid_stu(payloads={"ubi.img": ubi})
        with tempfile.TemporaryDirectory() as tmp:
            source, output = Path(tmp) / "Update.stu", Path(tmp) / "ubi.img"
            source.write_bytes(data)
            self.assertTrue(checker.extract_ubi(source, output, "wla-stock"))
            self.assertEqual(output.read_bytes(), ubi)
            self.assertEqual(output.stat().st_size % checker.UBI_PEB, 0)
            self.assertEqual(hashlib.sha256(output.read_bytes()).hexdigest(),
                             hashlib.sha256(ubi).hexdigest())

    def test_valid_complete_chain(self):
        data = valid_stu()
        self.assertEqual(len(bstu.validate_stu(data, "st10")), len(bstu.EXPECTED_MODULES))
        self.assertEqual(len(checker.validate(data, "st10")), len(bstu.EXPECTED_MODULES))

    def test_eight_module_chain_requires_explicit_target(self):
        data = valid_stu()
        with self.assertRaisesRegex(RuntimeError, "module chain mismatch"):
            bstu.validate_stu(data)
        with self.assertRaisesRegex(ValueError, "module chain mismatch"):
            checker.validate(data)
        self.assertEqual(len(bstu.validate_stu(data, "wla-stock")), len(bstu.EXPECTED_MODULES))
        self.assertEqual(len(checker.validate(data, "wla-stock")), len(bstu.EXPECTED_MODULES))

    def test_st20_complete_chain_is_accepted_but_not_as_st10(self):
        data = valid_stu(bstu.ST20_MODULES)
        self.assertEqual(len(bstu.validate_stu(data, "st20")), 13)
        self.assertEqual(len(checker.validate(data, "st20")), 13)
        with self.assertRaisesRegex(RuntimeError, "module chain mismatch"):
            bstu.validate_stu(data, "st10")
        with self.assertRaisesRegex(ValueError, "module chain mismatch"):
            checker.validate(data, "st10")

    def test_tri_model_chain_and_shared_rootfs_limit(self):
        data = valid_stu(bstu.SOUNDTAP_TRI_MODULES)
        self.assertEqual(len(bstu.validate_stu(data, "soundtap-tri")), 8)
        self.assertEqual(len(checker.validate(data, "soundtap-tri")), 8)
        with mock.patch.dict(bstu.PROFILES["soundtap-tri"], {"stock_ubi_module_size": 31}), \
                self.assertRaisesRegex(RuntimeError, "soundtap-rootfs.*exceeds"):
            bstu.validate_stu(data, "soundtap-tri")
        with mock.patch.dict(checker.PROFILES, {"soundtap-tri":
                (checker.SOUNDTAP_TRI_MODULES, 31, 95_906_836)}), \
                self.assertRaisesRegex(ValueError, "soundtap-rootfs exceeds"):
            checker.validate(data, "soundtap-tri")

    def test_duo_chain_carries_no_st20_payload(self):
        """ST10 + WLA: six modules, and an ST20 speaker finds nothing of its own in it."""
        data = valid_stu(bstu.SOUNDTAP_DUO_MODULES)
        self.assertEqual(len(bstu.validate_stu(data, "soundtap-duo")), 6)
        self.assertEqual(len(checker.validate(data, "soundtap-duo")), 6)
        self.assertNotIn("soundtap-kernel.spotty", bstu.SOUNDTAP_DUO_MODULES)
        self.assertEqual(bstu.SOUNDTAP_DUO_MODULES, checker.SOUNDTAP_DUO_MODULES)
        with self.assertRaisesRegex(RuntimeError, "module chain mismatch"):
            bstu.validate_stu(data, "soundtap-tri")
        with self.assertRaisesRegex(ValueError, "module chain mismatch"):
            checker.validate(data, "soundtap-tri")

    def test_duo_rootfs_budget_is_the_smaller_of_the_two_models(self):
        self.assertEqual(bstu.PROFILES["soundtap-duo"]["stock_ubi_module_size"],
                         bstu.PROFILES["st10"]["stock_ubi_module_size"])
        self.assertEqual(checker.PROFILES["soundtap-duo"][1], checker.PROFILES["st10"][1])

    def test_tri_model_assembler_writes_exact_crc_valid_chain(self):
        payloads = {name: (name.encode() + b"x" * 64) for name in bstu.SOUNDTAP_TRI_MODULES}
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / "Update.stu"
            bstu.assemble_self_contained_stu(payloads, output, "soundtap-tri")
            found = checker.validate(output.read_bytes(), "soundtap-tri")
        self.assertEqual([module[3] for module in found], bstu.SOUNDTAP_TRI_MODULES)

    def test_preview_package_differs_only_in_its_installer(self):
        """ADR 0133: the preview package is the universal one with module 0 swapped; a
        different payload anywhere after it is refused."""
        payloads = {name: (name.encode() + b"x" * 64) for name in bstu.SOUNDTAP_TRI_MODULES}
        with tempfile.TemporaryDirectory() as tmp:
            main, preview = Path(tmp) / "main.stu", Path(tmp) / "preview.stu"
            bstu.assemble_self_contained_stu(payloads, main, "soundtap-tri")
            # A longer installer moves every later header; that alone is no difference.
            bstu.assemble_self_contained_stu(
                {**payloads, "soundtap-installer": b"preview installer" * 9}, preview, "soundtap-tri")
            self.assertTrue(checker.same_payloads(main, preview, "soundtap-tri"))
            for name in ("soundtap-rootfs", "soundtap-spl.spotty"):
                bstu.assemble_self_contained_stu(
                    {**payloads, "soundtap-installer": b"preview", name: payloads[name] + b"y"},
                    preview, "soundtap-tri")
                with self.assertRaisesRegex(ValueError, "after the installer differs", msg=name):
                    checker.validate_same_payloads(main.read_bytes(), preview.read_bytes())
                self.assertFalse(checker.same_payloads(main, preview, "soundtap-tri"))

    def test_tri_model_uses_each_models_existing_rootfs_limit(self):
        self.assertEqual(bstu.MODEL_ROOTFS_LIMITS, {
            "rhino": bstu.PROFILES["st10"]["stock_ubi_module_size"],
            "binky": bstu.PROFILES["wla"]["stock_ubi_module_size"],
            "spotty": bstu.PROFILES["st20"]["stock_ubi_module_size"],
        })
        self.assertEqual(checker.MODEL_ROOTFS_LIMITS, {
            "rhino": checker.PROFILES["st10"][1],
            "binky": checker.PROFILES["wla"][1],
            "spotty": checker.PROFILES["st20"][1],
        })

    def test_tri_model_kernel_inputs_cannot_be_crossed(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "uImage"
            path.write_bytes(valid_kernel("st10"))
            bstu.require_kernel_model(path, "st10")
            with self.assertRaisesRegex(RuntimeError, "expected model 'binky'"):
                bstu.require_kernel_model(path, "binky")

    def test_st300_chain_is_offline_validation_only(self):
        data = valid_stu(checker.ST300_MODULES)
        self.assertEqual(len(checker.validate(data, "st300")), 9)
        with self.assertRaisesRegex(ValueError, "module chain mismatch"):
            checker.validate(data, "st10")

    def test_recovery_rejects_cross_model_candidate(self):
        with self.assertRaisesRegex(RuntimeError, "module chain mismatch"):
            bstu.validate_recovery_payloads(
                valid_stu(), valid_stu(bstu.ST20_MODULES), target="st10"
            )
        with self.assertRaisesRegex(ValueError, "module chain mismatch"):
            checker.validate_recovery(
                valid_stu(), valid_stu(bstu.ST20_MODULES), target="st10"
            )

    def test_rejects_corruption_and_trailing_data(self):
        good = valid_stu()
        mutations = []
        b = bytearray(good); b[bstu.PKG_SIZE_OFF] ^= 1; mutations.append(b)
        b = bytearray(good); b[0x20] ^= 1; mutations.append(b)
        first_image = bstu.HDR
        b = bytearray(good); b[first_image] ^= 1; mutations.append(b)
        mutations.append(bytearray(good) + b"garbage")
        mutations.append(bytearray(good[:-1]))
        for broken in mutations:
            with self.assertRaises(RuntimeError):
                bstu.validate_stu(broken, "st10")
            with self.assertRaises(ValueError):
                checker.validate(broken, "st10")

    def test_recovery_payloads_must_stay_stock(self):
        stock = valid_stu()
        bstu.validate_recovery_payloads(stock, bytearray(stock), target="st10")
        candidate = bytearray(stock)
        modules = list(bstu.walk_modules(candidate))
        _h, image, _size, _name = modules[1]
        candidate[image] ^= 1
        with self.assertRaisesRegex(RuntimeError, "stock boot/recovery payload changed"):
            bstu.validate_recovery_payloads(stock, candidate, target="st10")
        with self.assertRaises(ValueError):
            checker.validate_recovery(stock, candidate, target="st10")

        candidate = bytearray(stock)
        header_offset = modules[1][0]
        candidate[header_offset + 0x50] ^= 1
        header_bytes = candidate[header_offset:header_offset + bstu.HDR]
        struct.pack_into(
            ">I", candidate, header_offset + bstu.PKG_HCRC2_OFF,
            zlib.crc32(header_bytes[0x14:bstu.PKG_HCRC2_OFF]) & 0xFFFFFFFF,
        )
        with self.assertRaisesRegex(RuntimeError, "module header changed"):
            bstu.validate_recovery_payloads(stock, candidate, target="st10")
        with self.assertRaisesRegex(ValueError, "module header changed"):
            checker.validate_recovery(stock, candidate, target="st10")

        candidate = bytearray(stock)
        ubi_header = next(module[0] for module in modules if module[3] == "ubi.img")
        candidate[ubi_header + 0x50] ^= 1
        header_bytes = candidate[ubi_header:ubi_header + bstu.HDR]
        struct.pack_into(
            ">I", candidate, ubi_header + bstu.PKG_HCRC2_OFF,
            zlib.crc32(header_bytes[0x14:bstu.PKG_HCRC2_OFF]) & 0xFFFFFFFF,
        )
        with self.assertRaisesRegex(RuntimeError, "module header changed"):
            bstu.validate_recovery_payloads(stock, candidate, target="st10")
        with self.assertRaisesRegex(ValueError, "module header changed"):
            checker.validate_recovery(stock, candidate, target="st10")

    def test_random_single_bit_corruption_is_always_rejected(self):
        good = valid_stu()
        rng = random.Random(0)
        for _ in range(500):
            broken = bytearray(good)
            offset = rng.randrange(len(broken))
            broken[offset] ^= 1 << rng.randrange(8)
            with self.assertRaises(ValueError):
                checker.validate(broken, "st10")

    def test_package_cannot_exceed_stock_streamer_budget(self):
        data = valid_stu()
        modules, ubi_size, _stu_size = checker.PROFILES["wla-stock"]
        with mock.patch.dict(checker.PROFILES, {
            "wla-stock": (modules, ubi_size, len(data) - 1),
        }), self.assertRaisesRegex(ValueError, "package exceeds"):
            checker.validate(data, "wla-stock")

        profile = dict(bstu.PROFILES["wla-stock"], stock_stu_bytes=len(data) - 1)
        with mock.patch.dict(bstu.PROFILES, {"wla-stock": profile}), \
                self.assertRaisesRegex(RuntimeError, "package .* exceeds"):
            bstu.validate_stu(data, "wla-stock")

    def test_mainline_kernel_uses_reserved_slot_budget(self):
        found = []
        for name in checker.WLA_MODULES:
            size = checker.MAX_KERNEL_BYTES + checker.OVERLAP + 1 \
                if name == "linuxPatched" else checker.OVERLAP + 1
            found.append((0, 0, size, name))
        with self.assertRaisesRegex(ValueError, "kernel is invalid or too large"):
            checker.validate_mainline_kernel(
                bytearray(checker.MAX_KERNEL_BYTES + 1), found, "wla"
            )

    def test_mainline_st10_kernel_validates_after_fixsig_marker_restore(self):
        payload = bytearray(64 + 8)
        struct.pack_into(">I", payload, 0, 0x27051956)
        struct.pack_into(">I", payload, 12, 8)
        payload[32:32 + len(b"Linux-6.12 SoundTap st10")] = b"Linux-6.12 SoundTap st10"
        payload[64:72] = b"kernel!!"
        struct.pack_into(">I", payload, 24, zlib.crc32(payload[64:]) & 0xFFFFFFFF)
        header = bytearray(payload[:64])
        struct.pack_into(">I", header, 4, 0)
        struct.pack_into(">I", payload, 4, zlib.crc32(header) & 0xFFFFFFFF)
        payload[32:37] = b"Linub"
        payload += b"\xff" * checker.OVERLAP
        found = [(0, 0, len(payload), "linuxPatched")]
        with mock.patch.object(
            checker, "profile_for_names", return_value=("st10", (["linuxPatched"], 0, 0))
        ):
            checker.validate_mainline_kernel(payload, found, "st10")

    def test_independent_bootloader_validation(self):
        checker.validate_mlo(valid_mlo())
        checker.validate_raw_mlo(b"\x0f\x00\x00\xeaSPL")
        checker.validate_uboot(valid_uboot())
        with self.assertRaisesRegex(ValueError, "GP/SPL"):
            checker.validate_mlo(valid_mlo() + b"x")
        with self.assertRaisesRegex(ValueError, "legacy uImage"):
            checker.validate_uboot(valid_uboot() + b"x")
        with self.assertRaisesRegex(ValueError, "128 KiB"):
            checker.validate_mlo(b"x" * (checker.NAND_SPL_SIZE + 1))
        with self.assertRaisesRegex(ValueError, "1.875 MiB"):
            checker.validate_uboot(b"x" * (checker.NAND_UBOOT_SIZE + 1))
        with self.assertRaisesRegex(ValueError, "ARM SPL vectors"):
            checker.validate_raw_mlo(valid_mlo())

    def test_bootloader_gate_requires_changed_sane_images(self):
        stock = valid_stu(payloads={"MLO": valid_mlo(b"stock"),
                                    "u-boot.img": valid_uboot(b"stock")})
        candidate = bstu.replace_module(stock, "MLO", valid_mlo(b"mainline"))
        candidate = bstu.replace_module(candidate, "u-boot.img", valid_uboot(b"mainline"))
        candidate = bstu.replace_module(
            candidate, "ubi.img",
            b"U" * (checker.UBI_PEB - checker.OVERLAP) + b"\xff" * checker.OVERLAP,
            True, True,
        )
        struct.pack_into(">I", candidate, bstu.PKG_SIZE_OFF, len(candidate))
        struct.pack_into(">I", candidate, bstu.PKG_HCRC_OFF,
                         zlib.crc32(candidate[:bstu.PKG_HCRC_OFF]) & 0xFFFFFFFF)
        with tempfile.TemporaryDirectory() as tmp:
            stock_path, candidate_path = Path(tmp) / "stock", Path(tmp) / "candidate"
            stock_path.write_bytes(stock)
            candidate_path.write_bytes(candidate)
            with mock.patch.object(checker, "validate_ubi"), \
                    mock.patch.object(checker, "validate_mainline_kernel"):
                # Every model that shares this chain, not just the Wireless Link:
                # replacing the bootloader is allowed everywhere now.
                for target in ("wla-stock", "st10"):
                    with self.subTest(target=target):
                        self.assertTrue(checker.recovery_mainline_bootloaders(
                            candidate_path, stock_path, target
                        ))
                unchanged = bstu.replace_module(candidate, "MLO", valid_mlo(b"stock"))
                struct.pack_into(">I", unchanged, bstu.PKG_SIZE_OFF, len(unchanged))
                struct.pack_into(">I", unchanged, bstu.PKG_HCRC_OFF,
                                 zlib.crc32(unchanged[:bstu.PKG_HCRC_OFF]) & 0xFFFFFFFF)
                candidate_path.write_bytes(unchanged)
                self.assertFalse(checker.recovery_mainline_bootloaders(
                    candidate_path, stock_path, "wla-stock"
                ))

    def test_independent_ubi_geometry_validation(self):
        def ubi_crc(chunk):
            return (zlib.crc32(chunk) ^ 0xFFFFFFFF) & 0xFFFFFFFF

        data = bytearray(b"\xff" * (checker.UBI_PEB * 3))
        for peb in range(3):
            ec = peb * checker.UBI_PEB
            data[ec:ec + 4] = b"UBI#"
            data[ec + 4] = 1
            struct.pack_into(">II", data, ec + 16, checker.UBI_MIN_IO,
                             checker.UBI_MIN_IO * 2)
        record = checker.UBI_MIN_IO * 2
        struct.pack_into(">III", data, record, checker.UBI_VOLUME_LEBS, 1, 0)
        data[record + 12] = 1
        struct.pack_into(">H", data, record + 14, 6)
        data[record + 16:record + 22] = b"rootfs"
        data[record + 144] = 1
        vid = checker.UBI_PEB * 2 + checker.UBI_MIN_IO
        data[vid:vid + 5] = b"UBI!\x01"
        struct.pack_into(">II", data, vid + 8, 0, 0)
        superblock = checker.UBI_PEB * 2 + checker.UBI_MIN_IO * 2
        data[superblock:superblock + 4] = b"1\x18\x10\x06"
        struct.pack_into("<I", data, superblock + 16, 64)
        struct.pack_into("<I", data, superblock + 28, checker.UBIFS_FLG_SPACE_FIXUP)
        struct.pack_into("<II", data, superblock + 40, 640,
                          checker.UBI_VOLUME_LEBS)
        for peb in range(3):
            ec = peb * checker.UBI_PEB
            struct.pack_into(">I", data, ec + 60, ubi_crc(data[ec:ec + 60]))
            current_vid = ec + checker.UBI_MIN_IO
            if data[current_vid:current_vid + 4] != b"UBI!":
                data[current_vid:current_vid + 5] = b"UBI!\x01"
            struct.pack_into(">I", data, current_vid + 60,
                             ubi_crc(data[current_vid:current_vid + 60]))
        struct.pack_into(">I", data, record + 168,
                         ubi_crc(data[record:record + 168]))
        struct.pack_into("<I", data, superblock + 4,
                         ubi_crc(data[superblock + 8:superblock + 64]))
        checker.validate_ubi(data)

        missing_fixup = bytearray(data)
        struct.pack_into("<I", missing_fixup, superblock + 28, 0)
        struct.pack_into("<I", missing_fixup, superblock + 4,
                         ubi_crc(missing_fixup[superblock + 8:superblock + 64]))
        with self.assertRaisesRegex(ValueError, "differs from NAND contract"):
            checker.validate_ubi(missing_fixup)

        corruptions = (
            (60, "EC header CRC"),
            (checker.UBI_MIN_IO + 60, "VID header CRC"),
            (record + 168, "volume table CRC"),
            (superblock + 4, "superblock CRC"),
        )
        for offset, message in corruptions:
            broken = bytearray(data)
            broken[offset] ^= 1
            with self.subTest(message=message), self.assertRaisesRegex(ValueError, message):
                checker.validate_ubi(broken)

    def test_ubi_capacity_contract(self):
        geometry = checker.NAND_GEOMETRY
        overhead = (geometry["layout_pebs"] + geometry["wear_leveling_pebs"]
                    + geometry["atomic_leb_change_pebs"])
        self.assertEqual(geometry["chip_pebs"] * geometry["beb_limit_per_1024"] // 1024,
                         geometry["bad_peb_reserve"])
        self.assertEqual(geometry["partition_pebs"] - overhead
                         - geometry["bad_peb_reserve"], geometry["volume_lebs"])


if __name__ == "__main__":
    unittest.main()
