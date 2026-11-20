# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Layer 1 of the install gate: what it expects and how its guest is set up."""
import hashlib
import importlib.util
import struct
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("build_stu", ROOT / "os/installer/build-stu.py")
build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build)
PEB = build.UBI_PEB
LAYOUT_VOL = 0x7FFFEFFF
VID_OFF, DATA_OFF = 2048, 4096


def peb(vol, lnum, sqnum, data):
    """One physical eraseblock as ubinize writes it: EC header, VID header, data."""
    raw = bytearray(b"\xff" * PEB)
    raw[:4] = b"UBI#"
    struct.pack_into(">II", raw, 16, VID_OFF, DATA_OFF)
    raw[VID_OFF:VID_OFF + 4] = b"UBI!"
    struct.pack_into(">II", raw, VID_OFF + 8, vol, lnum)
    struct.pack_into(">Q", raw, VID_OFF + 40, sqnum)
    raw[DATA_OFF:DATA_OFF + len(data)] = data
    return bytes(raw)


class StuExpectation(unittest.TestCase):
    """os/support/stu_expect.py derives what the guest compares NAND with, from the package
    alone. Run here on a package whose answer is known."""

    def test_expectation_comes_from_the_package(self):
        leb = PEB - DATA_OFF
        reserved = 4
        table = struct.pack(">I", reserved) + b"\0" * 168
        rootfs = b"".join((
            peb(LAYOUT_VOL, 0, 0, table),
            peb(0, 1, 1, b"stale copy of LEB 1"),
            peb(0, 0, 2, b"LEB 0"),
            peb(0, 1, 5, b"LEB 1 rewritten"),  # the higher sequence number wins
        ))
        volume = bytearray(b"\xff" * (reserved * leb))
        volume[:5] = b"LEB 0"
        volume[leb:leb + 15] = b"LEB 1 rewritten"
        payloads = {name: f"payload of {name}".encode() * 3 for name in build.SOUNDTAP_TRI_MODULES}
        payloads["soundtap-rootfs"] = rootfs
        with tempfile.TemporaryDirectory() as tmp:
            package = Path(tmp) / "Update.stu"
            build.assemble_self_contained_stu(payloads, package, "soundtap-tri")
            out = subprocess.run([sys.executable, "-B", str(ROOT / "os/support/stu_expect.py"),
                                  str(package), "rhino", "binky", "spotty", "ginger"],
                                 check=True, capture_output=True, text=True).stdout
            data = package.read_bytes()
        got = dict(line.split("=", 1) for line in out.splitlines())
        for variant, module in (("rhino", "rhino"), ("binky", "binky"), ("spotty", "spotty"),
                                ("ginger", "binky")):  # the ST300 takes the WLA's kernel
            kernel = payloads[f"soundtap-kernel.{module}"]
            self.assertEqual(got[f"EXP_KERNEL_SHA_{variant}"], hashlib.sha256(kernel).hexdigest())
            self.assertEqual(got[f"EXP_KERNEL_LEN_{variant}"], str(len(kernel)))
        self.assertEqual(got["EXP_VOL_SHA"], hashlib.sha256(volume).hexdigest())
        self.assertEqual(got["EXP_VOL_LEN"], str(len(volume)))
        start, length = int(got["EXP_ROOTFS_OFF"]), int(got["EXP_ROOTFS_LEN"])
        self.assertEqual(data[start:start + length], rootfs)


class GuestSetup(unittest.TestCase):
    """nandsim-install-test.sh needs Docker, QEMU and a built package, so it runs in every
    Build rather than here. These check its pins and its hermetic setup as text: a pinned
    image digest or archive date is a fact in the file, and no run on a checkout could show
    what a run on a differently configured runner would fetch."""

    script = (ROOT / "os/tests/installer/nandsim-install-test.sh").read_text()

    def test_guest_kernel_ships_nandsim_on_both_arches(self):
        # Debian's arm64 kernel has no nandsim; Ubuntu's modules-extra has it.
        self.assertIn("RUNNER_IMAGE=ubuntu:24.04@sha256:", self.script)
        container = self.script[self.script.index("container.sh"):]
        self.assertIn('"linux-modules-extra-$KVER"', container)
        self.assertIn("aarch64|arm64) GUEST_ARCH=arm64", self.script)
        self.assertNotIn("--platform", self.script)

    def test_qemu_needs_no_network_rom_and_docker_output_is_removable(self):
        self.assertEqual(self.script.count("-nic none"), 2)
        self.assertIn('chown -R "$HOST_UID:$HOST_GID" /work', self.script)
        self.assertIn("npm ci --silent && SOUNDTAP_WEB_OUT_DIR=/out/web npm run build", self.script)
        # The work directory is in the checkout, where any docker daemon can mount it.
        self.assertIn('WORK=$(mktemp -d "$REPO/output/nandsim.XXXXXX")', self.script)

    def test_guest_packages_come_from_a_frozen_archive(self):
        self.assertRegex(self.script, r"APT_SNAPSHOT=\d{8}T\d{6}Z")
        self.assertIn("https://snapshot.ubuntu.com/ubuntu/$APT_SNAPSHOT/", self.script)

    def test_guest_packages_are_fetched_once_per_setup(self):
        self.assertIn('GUEST_IMAGE=soundtap-nandsim-guest:$APT_SNAPSHOT-$GUEST_ARCH-$(sha256sum',
                      self.script)
        self.assertIn('docker image inspect "$GUEST_IMAGE"', self.script)
        self.assertIn('"$GUEST_IMAGE" sh /work/container.sh', self.script)
        container = self.script[self.script.index("cat >\"$WORK/container.sh\""):
                                self.script.index("\nCONTAINER\n")]
        self.assertNotIn("apt-get", container)


if __name__ == "__main__":
    unittest.main()
