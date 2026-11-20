# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import importlib.util
import struct
import sys
import tempfile
import unittest
import zlib
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[3]

SPEC = importlib.util.spec_from_file_location("build_stu", ROOT / "os/installer/build-stu.py")
build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build)


def ubi_image(path):
    data = bytearray(build.UBI_PEB * 3)
    record = build.UBI_MIN_IO * 2
    struct.pack_into(">I", data, record, build.UBI_VOLUME_LEBS)
    data[record + 144] = 1
    vid = build.UBI_PEB * 2 + build.UBI_MIN_IO
    data[vid:vid + 4] = b"UBI!"
    struct.pack_into(">II", data, vid + 8, 0, 0)
    superblock = build.UBI_PEB * 2 + build.UBI_MIN_IO * 2
    data[superblock:superblock + 4] = b"1\x18\x10\x06"
    struct.pack_into("<I", data, superblock + 28, build.UBIFS_FLG_SPACE_FIXUP)
    struct.pack_into("<II", data, superblock + 40,
                     build.UBI_VOLUME_LEBS, build.UBI_MAX_LEBS)
    path.write_bytes(data)
    return data, record, superblock


def valid_mlo(payload=b"spl"):
    """The shape the mask ROM reads from NAND, as found on a stock WLA's mtd0:
    a TI configuration header, then a GP header at 0x200 whose size covers
    everything after that header, then the SPL."""
    ch = bytearray(b"\xff" * 0x200)
    ch[0:8] = struct.pack("<II", 0x40, 0x0c)
    ch[20:30] = b"CHSETTINGS"
    return bytes(ch) + struct.pack("<II", len(payload) + 8, 0x402F0400) + payload


def valid_uboot(payload=b"u-boot"):
    header = bytearray(64)
    struct.pack_into(">IIIIIII", header, 0, 0x27051956, 0, 0, len(payload),
                     0x80800000, 0x80800000, zlib.crc32(payload) & 0xFFFFFFFF)
    struct.pack_into(">I", header, 4, zlib.crc32(header) & 0xFFFFFFFF)
    return bytes(header) + payload


def valid_kernel(payload=b"kernel"):
    header = bytearray(64)
    header[32:32 + len(b"Linux-6.12 SoundTap binky")] = b"Linux-6.12 SoundTap binky"
    struct.pack_into(">IIIIIII", header, 0, 0x27051956, 0, 0, len(payload),
                     0x80008000, 0x80008000, zlib.crc32(payload) & 0xFFFFFFFF)
    struct.pack_into(">I", header, 4, zlib.crc32(header) & 0xFFFFFFFF)
    return bytes(header) + payload


class UbiValidationTest(unittest.TestCase):
    def test_pack_ubifs_enables_space_fixup(self):
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(build, "sh") as run:
            rootfs = Path(tmp) / "rootfs"
            rootfs.mkdir()
            out = Path(tmp) / "rootfs.ubifs"
            out.touch()
            build.pack_ubifs(rootfs, out)
        self.assertIn("-F", run.call_args.args[0])

    def test_source_date_epoch_normalizes_rootfs_before_packing(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "rootfs"
            root.mkdir()
            regular = root / "file"
            regular.write_text("data")
            symlink = root / "link"
            symlink.symlink_to("file")
            out = Path(tmp) / "rootfs.ubifs"
            with mock.patch.dict("os.environ", {"SOURCE_DATE_EPOCH": "123"}), \
                    mock.patch.object(build, "sh", side_effect=lambda _command: out.touch()):
                build.pack_ubifs(root, out)
            self.assertEqual(int(root.stat().st_mtime), 123)
            self.assertEqual(int(regular.stat().st_mtime), 123)
            self.assertEqual(int(symlink.lstat().st_mtime), 123)

    def test_invalid_source_date_epoch_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            for value in ("not-an-integer", "0"):
                with self.subTest(value=value), mock.patch.dict(
                    "os.environ", {"SOURCE_DATE_EPOCH": value}
                ), self.assertRaisesRegex(RuntimeError, "SOURCE_DATE_EPOCH"):
                    build.pack_ubifs(Path(tmp), Path(tmp) / "rootfs.ubifs")

    def test_geometry_and_autoresize_contract(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "ubi.img"
            data, record, superblock = ubi_image(path)
            build.validate_ubi(path)

            smaller = bytearray(data)
            struct.pack_into("<I", smaller, superblock + 40, 686)
            path.write_bytes(smaller)
            build.validate_ubi(path)

            for offset, value, message in (
                (record + 144, 0, "stock autoresize semantics"),
                (superblock + 44, build.UBI_MAX_LEBS - 1, "unsafe UBI geometry"),
                (superblock + 44, build.UBI_MAX_LEBS + 1, "unsafe UBI geometry"),
                (superblock + 40, 0, "unsafe UBI geometry"),
                (superblock + 40, build.UBI_VOLUME_LEBS + 1, "unsafe UBI geometry"),
            ):
                broken = bytearray(data)
                if offset == record + 144:
                    broken[offset] = value
                else:
                    struct.pack_into("<I", broken, offset, value)
                path.write_bytes(broken)
                with self.subTest(message=message), self.assertRaisesRegex(RuntimeError, message):
                    build.validate_ubi(path)

    def test_ubi_capacity_contract(self):
        geometry = build.NAND_GEOMETRY
        overhead = (geometry["layout_pebs"] + geometry["wear_leveling_pebs"]
                    + geometry["atomic_leb_change_pebs"])
        self.assertEqual(geometry["chip_pebs"] * geometry["beb_limit_per_1024"] // 1024,
                         geometry["bad_peb_reserve"])
        self.assertEqual(geometry["partition_pebs"] - overhead
                         - geometry["bad_peb_reserve"], geometry["volume_lebs"])

    def test_missing_space_fixup_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "ubi.img"
            data, _record, superblock = ubi_image(path)
            struct.pack_into("<I", data, superblock + 28, 0)
            path.write_bytes(data)
            with self.assertRaisesRegex(RuntimeError, "differs from NAND contract"):
                build.validate_ubi(path)


class MainlineStuTest(unittest.TestCase):
    def test_prebuilt_ubi_rejects_rootfs_override(self):
        with tempfile.TemporaryDirectory() as tmp:
            argv = ["build-stu.py", "--ubi", f"{tmp}/rootfs.ubi", "--rootfs", tmp]
            with mock.patch.object(sys, "argv", argv), self.assertRaises(SystemExit):
                build.main()

    def test_bose_kernel_marker_keeps_canonical_crc(self):
        payload = b"kernel"
        header = bytearray(64)
        struct.pack_into(">IIIIIII4B", header, 0, 0x27051956, 0, 0,
                         len(payload), 0x80008000, 0x80008000,
                         zlib.crc32(payload) & 0xFFFFFFFF, 5, 2, 2, 0)
        header[32:64] = b"Linux-6.12 SoundTap st10".ljust(32, b"\0")
        struct.pack_into(">I", header, 4, zlib.crc32(header) & 0xFFFFFFFF)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "uImage"
            path.write_bytes(header + payload)
            marked = build.bose_kernel_payload(path)
        self.assertEqual(marked[32:37], b"Linub")
        self.assertEqual(len(marked) % build.UBI_MIN_IO, 0)
        repaired = bytearray(marked[:64])
        repaired[32:37] = b"Linux"
        stored = struct.unpack_from(">I", repaired, 4)[0]
        struct.pack_into(">I", repaired, 4, 0)
        self.assertEqual(zlib.crc32(repaired) & 0xFFFFFFFF, stored)

    def test_bootloader_header_and_partition_checks(self):
        build.validate_mlo(valid_mlo())
        build.validate_uboot(valid_uboot())
        for image, validator, message in (
            # Trailing bytes are allowed -- the stock image has them -- but a GP
            # header claiming more than is present is not.
            (valid_mlo()[:-1], build.validate_mlo, "declares more"),
            (b"x" * (build.NAND_SPL + 1), build.validate_mlo, "mtd0"),
            # The raw vectors-at-zero form is the USB path's and must never reach mtd0.
            (b"\x0f\x00\x00\xea" + b"spl" * 200, build.validate_mlo,
             "TI configuration header"),
            (valid_uboot() + b"x", build.validate_uboot, "legacy uImage"),
            (b"x" * (build.NAND_UBOOT + 1), build.validate_uboot, "mtd4"),
        ):
            with self.subTest(message=message), self.assertRaisesRegex(RuntimeError, message):
                validator(image)
        build.validate_mlo(valid_mlo() + b"x")

    def test_self_contained_wla_chain_is_explicit_and_valid(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "Update-wla.stu"
            payloads = {name: f"repo-built:{name}".encode() for name in build.WLA_MODULES}
            build.assemble_self_contained_stu(payloads, out)
            modules = build.validate_stu(bytearray(out.read_bytes()), "wla")
        self.assertEqual([module[3] for module in modules], build.WLA_MODULES)

    def test_self_contained_wla_headers_use_native_magic(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "Update-wla.stu"
            payloads = {name: f"repo-built:{name}".encode() for name in build.WLA_MODULES}
            build.assemble_self_contained_stu(payloads, out)
            data = out.read_bytes()
        self.assertEqual(struct.unpack_from(">I4sII", data), (build.OVERLAP, b"RTCH", len(data), 0x124))
        for index, (header, _image, size, _name) in enumerate(build.walk_modules(data)):
            self.assertEqual(struct.unpack_from(">I", data, header + 0x9C)[0], size)
            self.assertEqual(struct.unpack_from(">I", data, header + 0xA0)[0], index + 1)

    def test_offline_wla_probe_uses_stock_module_names_without_stock_input(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            payloads = {name: f"repo-built-probe:{name}".encode() for name in build.WLA_PROBE_MODULES}
            build.assemble_self_contained_stu(payloads, tmp / "probe.stu", "wla-probe")
            data = (tmp / "probe.stu").read_bytes()
            modules = build.validate_stu(bytearray(data), "wla-probe")
        self.assertEqual(data[4:8], b"BOSE")
        self.assertEqual([module[3] for module in modules], build.WLA_PROBE_MODULES)
        self.assertEqual([struct.unpack_from(">I", data, h + 0xA0)[0] for h, *_ in modules], list(build.WLA_STOCK_ORDINALS))
        self.assertEqual([struct.unpack_from(">I", data, h + 0x130)[0] for h, *_ in modules], [0, 2, 2, 0, 0, 0, 0, 0])

    def test_release_cli_rejects_offline_wla_probe_profile(self):
        """wla-probe is offline-only; the self-contained route needs the WLA target and all
        three boot payloads. Each is refused before anything is packed."""
        with tempfile.TemporaryDirectory() as tmp:
            given = [f"--kernel={tmp}/k", f"--mlo={tmp}/m", f"--u-boot={tmp}/u"]
            for argv in (["--target", "wla-probe", "--self-contained", *given],
                         ["--target", "st10", "--self-contained", *given],
                         ["--target", "wla", "--self-contained", *given[:2]]):
                with self.subTest(argv=argv), \
                        mock.patch.object(sys, "argv", ["build-stu.py", *argv]), \
                        mock.patch.object(build, "preflight_tools") as preflight, \
                        mock.patch.object(build, "assemble_self_contained_stu") as assemble, \
                        mock.patch("sys.stderr"), self.assertRaises(SystemExit) as stop:
                    build.main()
                self.assertEqual(stop.exception.code, 2)
                preflight.assert_not_called()
                assemble.assert_not_called()

    def test_self_contained_wla_chain_needs_no_firmware_base(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            payloads = {name: f"repo-built:{name}".encode() for name in build.WLA_MODULES}
            missing_repo = tmp / "repo-without-base"
            self.assertFalse((missing_repo / "os/stock").exists())
            with mock.patch.object(build, "REPO", missing_repo):
                build.assemble_self_contained_stu(payloads, tmp / "Update-wla.stu")
            modules = build.validate_stu(bytearray((tmp / "Update-wla.stu").read_bytes()), "wla")
        self.assertEqual([module[3] for module in modules], build.WLA_MODULES)

    def test_self_contained_command_reaches_assembler_with_four_native_modules(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            rootfs = tmp / "rootfs"
            required = [
                "bin/busybox", "opt/soundtap/soundtapd", "usr/bin/librespot", "etc/init.d/soundtap",
                "etc/asound.conf.bus", "etc/shairport-sync.conf", "opt/soundtap/airplay-source.sh",
                "opt/soundtap/bt.sh", "opt/soundtap/bt-ctl.sh", "opt/soundtap/bt-source.sh",
                "opt/soundtap/spotify-source.sh", "boot/binky_sm2.dtb", "usr/sbin/hostapd",
            ]
            for name in required:
                path = rootfs / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.touch()
            for name in ("bin/busybox", "opt/soundtap/soundtapd", "usr/bin/librespot", "etc/init.d/soundtap",
                         "opt/soundtap/airplay-source.sh", "opt/soundtap/bt.sh", "opt/soundtap/bt-ctl.sh",
                         "opt/soundtap/bt-source.sh", "opt/soundtap/spotify-source.sh", "usr/sbin/hostapd"):
                (rootfs / name).chmod(0o755)
            (rootfs / "init").symlink_to("/bin/busybox")
            (rootfs / "sbin").mkdir()
            (rootfs / "sbin/init").symlink_to("/bin/busybox")
            kernel, mlo, uboot = tmp / "kernel", tmp / "MLO", tmp / "u-boot"
            kernel.write_bytes(valid_kernel())
            mlo.write_bytes(valid_mlo(b"\x0f\x00\x00\xeaSPL"))
            uboot.write_bytes(valid_uboot())
            captured = {}
            def assemble(payloads, out, _target):
                captured.update(payloads)
                out.write_bytes(b"stu")
            args = ["build-stu.py", "--target", "wla", "--self-contained", "--rootfs", str(rootfs),
                    "--kernel", str(kernel), "--mlo", str(mlo), "--u-boot", str(uboot),
                    "--out", str(tmp / "Update.stu"), "--work-dir", str(tmp / "work")]
            with mock.patch("sys.argv", args), mock.patch.object(build, "preflight_tools"), \
                    mock.patch.object(build, "pack_ubifs"), \
                    mock.patch.object(build, "pack_ubi", side_effect=lambda _ubifs, out: out.write_bytes(b"ubi")), \
                    mock.patch.object(build, "assemble_self_contained_stu", side_effect=assemble):
                build.main()
            self.assertEqual(list(captured), build.WLA_MODULES)
            self.assertEqual(captured["soundtap-kernel"][:64], valid_kernel()[:64])
            self.assertEqual(captured["soundtap-rootfs"], b"ubi")


if __name__ == "__main__":
    unittest.main()
