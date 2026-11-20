# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Offline gate for the WLA update installer.

Plays stock's half of the handshake against the real installer binary, built for this host, and
checks it does what the board proved stock requires: speak first, pull in 16 KiB records from
sequence 2, verify every module's CRC out of the stream, and refuse a package whose CRC is
wrong. No board, no Docker, no NAND.
"""
import importlib.util
import os
import socket
import re
import struct
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent.parent
SOURCE = REPO / "os/installer/module"

spec = importlib.util.spec_from_file_location("build_stu", REPO / "os/installer/build-stu.py")
build_stu = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build_stu)

spec = importlib.util.spec_from_file_location("stu_check", REPO / "os/support/stu_check.py")
stu_check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stu_check)

spec = importlib.util.spec_from_file_location("wla_env", REPO / "os" / "support" / "uboot_recipes.py")
wla_env = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wla_env)

spec = importlib.util.spec_from_file_location("uboot_env", REPO / "os" / "support" / "uboot_env.py")
uboot_env = importlib.util.module_from_spec(spec)
spec.loader.exec_module(uboot_env)

CHUNK = 16384


def free_port():
    """A port this run can have to itself. The installed binary always listens on :40015
    because that is where stock calls it, but two offline gates on one runner cannot both
    have it -- that collision is what "installer never listened" means."""
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


# Every compiled installer gets its own port. One per module was not enough: the binary
# from the previous class can still be holding it when the next one starts listening,
# which is the same "installer never listened" this was meant to end.
PORT = free_port()  # the default for helpers that build their own one-off installer


def varint(value):
    out = bytearray()
    while value >= 0x80:
        out.append((value & 0x7F) | 0x80)
        value >>= 7
    out.append(value)
    return bytes(out)


def field_varint(field, value):
    return varint(field << 3) + varint(value)


def field_bytes(field, value):
    return varint(field << 3 | 2) + varint(len(value)) + value


def envelope(type_, message_id, contents, type_name, sequence=None):
    body = field_varint(1, type_) + field_varint(2, 0) + field_varint(3, message_id)
    body += field_bytes(6, contents)
    if sequence is not None:
        body += field_varint(7, sequence)
    body += field_bytes(8, type_name.encode())
    return struct.pack(">I", len(body)) + body


def parse(body):
    """Return (type, message_id, sequence, contents, type_name) from one envelope body."""
    out = {"type": 0, "message_id": 0, "sequence": None, "contents": b"", "type_name": ""}
    at = 0
    while at < len(body):
        key, at = read_varint(body, at)
        field, wire = key >> 3, key & 7
        if wire == 0:
            value, at = read_varint(body, at)
            if field == 1:
                out["type"] = value
            elif field == 3:
                out["message_id"] = value
            elif field == 7:
                out["sequence"] = value
        elif wire == 2:
            size, at = read_varint(body, at)
            value, at = body[at:at + size], at + size
            if field == 6:
                out["contents"] = value
            elif field == 8:
                out["type_name"] = value.decode()
        else:
            break
    return out


def read_varint(body, at):
    value, shift = 0, 0
    while at < len(body):
        c = body[at]
        at += 1
        value |= (c & 0x7F) << shift
        if not c & 0x80:
            break
        shift += 7
    return value, at


class Coordinator:
    """Stock's half: connect, stay silent until the installer speaks, then feed the package."""

    def __init__(self, payload, corrupt_at=None, hangup_at_eos=False, declared=None):
        self.payload = bytearray(payload)
        self.declared = len(payload) if declared is None else declared
        self.hangup_at_eos = hangup_at_eos
        if corrupt_at is not None:
            self.payload[corrupt_at] ^= 0xFF
        self.sent = 0
        self.status_states = []
        self.requests = []
        self.error = None

    def run(self, conn):
        conn.settimeout(30)
        first = self.read_envelope(conn)
        if first["type_name"] != "SoftwareUpdateStatus":
            self.error = f"installer did not speak first: {first!r}"
            return
        self.status_states.append(status_state(first["contents"]))
        start = field_bytes(1, b"bonjour//") + field_varint(2, 0) + field_varint(3, self.declared)
        conn.sendall(envelope(1, 0, start, "SoftwareUpdateStart", sequence=1))

        while True:
            message = self.read_envelope(conn)
            if message is None:
                return
            if message["type_name"] == "SoftwareUpdateStatus":
                self.status_states.append(status_state(message["contents"]))
                continue
            if message["message_id"] != 100:
                # Anything else here is the installer's terminal message; record and stop.
                self.status_states.append(("terminal", message["type_name"]))
                return
            self.requests.append(message["sequence"])
            count, _ = read_varint(message["contents"], 1)
            chunk = bytes(self.payload[self.sent:self.sent + min(count, CHUNK)])
            self.sent += len(chunk)
            eos = self.sent >= len(self.payload)
            contents = field_varint(1, len(chunk)) + field_bytes(2, chunk)
            if eos:
                contents += field_varint(3, 1)
            conn.sendall(envelope(3, 100, contents, "SoftwareUpdateDataResponse",
                                  sequence=message["sequence"]))
            if eos and self.hangup_at_eos:
                # Stock has nothing left to send and drops the link. Everything the installer
                # still has to do -- write the rootfs, activate the kernel, switch the slot,
                # reboot -- happens after this point, so a hangup here must change nothing.
                conn.shutdown(socket.SHUT_RDWR)
                conn.close()
                return

    def read_envelope(self, conn):
        head = b""
        while len(head) < 4:
            part = conn.recv(4 - len(head))
            if not part:
                return None
            head += part
        size = struct.unpack(">I", head)[0]
        body = b""
        while len(body) < size:
            part = conn.recv(size - len(body))
            if not part:
                return None
            body += part
        return parse(body)


def status_state(contents):
    value, _ = read_varint(contents, 1)
    return value


CC_IMAGE = "soundtap-cc:local"
SCRATCH = REPO / "output" / "wla-installer-gate"


def scratch_dir():
    """Temporary directory the container build can also see. A self-hosted runner's Docker
    cannot necessarily bind-mount the runner's own /tmp, but the workspace always works."""
    import shutil
    if shutil.which("cc"):
        return tempfile.TemporaryDirectory()
    SCRATCH.mkdir(parents=True, exist_ok=True)
    return tempfile.TemporaryDirectory(dir=SCRATCH)


def compile_with(command, tmp):
    """Compile on the host when it has a compiler, otherwise in a small pinned container: the
    self-hosted runner has Docker and no toolchain. The container build copies the sources next
    to the output so nothing depends on how host paths map into it."""
    import shutil
    if shutil.which("cc"):
        subprocess.run(command, check=True, capture_output=True)
        return
    docker = shutil.which("docker")
    if not docker:
        raise unittest.SkipTest("no C compiler and no Docker to borrow one from")
    if subprocess.run([docker, "image", "inspect", CC_IMAGE],
                      capture_output=True).returncode != 0:
        subprocess.run([docker, "build", "-q", "-t", CC_IMAGE, str(REPO / "os/tests/installer/cc-image")],
                       check=True, capture_output=True)
    work = Path(tmp)
    for source in SOURCE.glob("*.[ch]"):
        shutil.copy(source, work / source.name)
    inside = ["/work/" + Path(arg).name if arg.startswith((str(SOURCE), str(work)))
              else arg for arg in command]
    result = subprocess.run([docker, "run", "--rm", "-v", f"{work}:/work", "-w", "/work", CC_IMAGE, *inside], capture_output=True, text=True)
    if result.returncode != 0:
        raise AssertionError("container build failed:\n" + result.stdout + result.stderr)


def await_listener(process, port, deadline=20.0):
    """Connect to the installer once it is listening, or give up after `deadline` seconds.

    This used to be a bare `for _ in range(200)`, which reads like a generous wait and is
    not one: a refused connection on loopback comes back immediately, so two hundred
    attempts can be spent in a few milliseconds. On a busy runner -- several of these
    classes each compiling an installer in a container -- the process had not reached
    bind() yet and the test failed with "installer never listened", which was diagnosed
    twice as a port collision and was never that. Bound the wait in seconds, and sleep
    between attempts so the loop is a wait rather than a spin.
    """
    end = time.monotonic() + deadline
    while time.monotonic() < end:
        try:
            return socket.create_connection(("127.0.0.1", port), 0.2)
        except OSError:
            if process.poll() is not None:
                return None
            time.sleep(0.025)
    return None


def build_installer(tmp, nand=False, bootloaders=False, port=None):
    """Build the real installer for this host. The NAND build talks to the same code the board
    runs; with no /proc/mtd around it falls back to the file backend, which is what lets the
    transaction be tested without hardware."""
    binary = Path(tmp) / ("wla-installer-nand" if nand else "wla-installer")
    port = port or PORT
    command = ["cc", "-O2", "-Wall", "-Wextra", f"-DLISTEN_PORT={port}", "-o", str(binary),
               str(SOURCE / "installer.c"), str(SOURCE / "swupdate.c")]
    if nand:
        command.insert(1, "-DSOUNDTAP_INSTALL_NAND")
        command.append(str(SOURCE / "mtd.c"))
    if bootloaders:
        command.insert(1, "-DSOUNDTAP_INSTALL_BOOTLOADERS")
    compile_with(command, tmp)
    return binary


def uimage_head(name):
    """Just enough of a uImage header for the installer's model check: magic and name."""
    return struct.pack(">I", 0x27051956) + b"\0" * 28 + name.ljust(32, b"\0")


def make_package(tmp, sizes=None, names=None, target="wla-probe", heads=None):
    """A structurally real STU whose payloads are ours, and the stream stock would hand over.
    `heads` replaces the first bytes of a payload, e.g. with a uImage header."""
    names = names or build_stu.WLA_PROBE_MODULES
    payloads = {}
    for name in names:
        seed = b"soundtap-wla-installer-offline-gate:%s " % name.encode()
        want = (sizes or {}).get(name, len(seed) * 40)
        payload = (seed * (want // len(seed) + 1))[:want]
        head = (heads or {}).get(name, b"")
        payloads[name] = head + payload[len(head):]
    package = Path(tmp) / "probe.stu"
    versions = {
        "linuxPatched": "27.0.7.46330.5043500",
        "ubi.img": "27.0.7.46330.5043500@sm2/spotty+mojo+binky",
        "MLO": "U-Boot 2014.07-05722-g93f9891",
        "u-boot.img": "U-Boot 2014.07-05722-g93f9891",
    }
    build_stu.assemble_self_contained_stu(payloads, package, target,
                                          {name: versions[name] for name in names if name in versions})
    data = package.read_bytes()
    # Stock keeps module 1 and streams the rest, starting exactly at module 2's header. In the
    # file that header follows module 1's payload bytes, which exclude the shared 20-byte tail.
    second_header = build_stu.HDR + len(payloads[names[0]])
    return data[second_header:], payloads


class TestModelSuffix(unittest.TestCase):
    """One package for every speaker means a module can name the model it is for. Stock never
    validated the chain -- it runs module 1 and streams the rest in -- so the shape of the file
    was only ever our own rule, and the installer is what has to pick correctly. Taking another
    model's rootfs would write a filesystem the board cannot boot; discarding its own would
    leave the slot empty."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = scratch_dir()
        # This binary never listens -- every call exits on --which-module -- so it takes
        # whatever port the default is and holds none of them.
        cls.binary = build_installer(cls.tmp.name)

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def decide(self, name, variant):
        out = subprocess.run([str(self.binary), "--which-module", name], capture_output=True,
                             text=True, env=dict(os.environ, SOUNDTAP_VARIANT=variant))
        self.assertEqual(out.returncode, 0, out.stderr)
        return out.stdout.strip()

    def test_unsuffixed_modules_are_for_every_model(self):
        for name in ("linuxPatched", "ubi.img", "MLO", "u-boot.img"):
            for variant in ("binky", "rhino", "spotty"):
                self.assertEqual(self.decide(name, variant), name)

    def test_a_module_for_this_model_loses_its_suffix(self):
        self.assertEqual(self.decide("ubi.img.binky", "binky"), "ubi.img")
        self.assertEqual(self.decide("linuxPatched.spotty", "spotty"), "linuxPatched")

    def test_a_module_for_another_model_is_discarded(self):
        self.assertEqual(self.decide("ubi.img.spotty", "binky"), "(another model)")
        self.assertEqual(self.decide("ubi.img.binky", "rhino"), "(another model)")

    def test_a_board_that_does_not_know_its_variant_takes_only_the_shared_modules(self):
        self.assertEqual(self.decide("ubi.img", ""), "ubi.img")
        self.assertEqual(self.decide("ubi.img.binky", ""), "(another model)")

    def test_a_dot_that_is_not_a_model_is_part_of_the_name(self):
        """u-boot.img and ubi.img end in a dotted word already; only a known model counts."""
        for name in ("u-boot.img", "ubi.img", "App_BD_DMP 3.x-3_8_0-9423_ER.bcd"):
            self.assertEqual(self.decide(name, "binky"), name)


class TestTriModelDispatch(TestModelSuffix):
    def test_every_model_takes_only_its_payloads(self):
        for variant in ("binky", "rhino", "spotty"):
            for kind in ("soundtap-kernel",):
                for model in ("binky", "rhino", "spotty"):
                    want = kind if model == variant else "(another model)"
                    self.assertEqual(self.decide(f"{kind}.{model}", variant), want)

    def test_every_model_takes_shared_rootfs(self):
        for variant in ("binky", "rhino", "spotty"):
            self.assertEqual(self.decide("soundtap-rootfs", variant), "soundtap-rootfs")

    def test_each_boot_chain_goes_to_its_own_model_only(self):
        """The binky chain is the sm2 chain: the ST10 (rhino) is the same module and takes it,
        and the ST20 (spotty) takes its U-Boot behind an SPL of its own."""
        takes = {("soundtap-spl", "binky"): ("binky", "rhino"), ("soundtap-spl", "spotty"): ("spotty",),
                 ("soundtap-uboot", "binky"): ("binky", "rhino", "spotty")}
        for (kind, model), variants in takes.items():
            for variant in ("binky", "rhino", "spotty"):
                want = kind if variant in variants else "(another model)"
                self.assertEqual(self.decide(f"{kind}.{model}", variant), want)

    def test_only_the_boot_chain_is_shared_with_rhino(self):
        for name in ("soundtap-kernel.binky", "soundtap-rootfs.binky", "ubi.img.binky"):
            self.assertEqual(self.decide(name, "rhino"), "(another model)")

    def test_ginger_takes_everything_named_for_binky(self):
        """The ST300 is the Wireless Link's module: same kernel, same package entries."""
        for kind in ("soundtap-kernel", "soundtap-spl", "soundtap-uboot"):
            self.assertEqual(self.decide(f"{kind}.binky", "ginger"), kind)
            for model in ("rhino", "spotty"):
                self.assertEqual(self.decide(f"{kind}.{model}", "ginger"), "(another model)")
        self.assertEqual(self.decide("soundtap-rootfs", "ginger"), "soundtap-rootfs")


class TestStockWifiKey(unittest.TestCase):
    """On a first install the installer takes the key and IV for stock's saved Wi-Fi out of
    stock's own NetManager: the only two runs of 12+ ASCII digits, 32 and 16 long.
    The images here are made up; stock's bytes never enter the repo."""

    KEY = b"01234567890123456789012345678901"
    IV = b"9876543210987654"

    @classmethod
    def setUpClass(cls):
        cls.tmp = scratch_dir()
        cls.binary = build_installer(cls.tmp.name)

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def find(self, image):
        path = Path(self.tmp.name) / "NetManager"
        path.write_bytes(image)
        out = subprocess.run([str(self.binary), "--stock-wifi-key", str(path)],
                             capture_output=True, text=True)
        self.assertEqual(out.returncode, 0, out.stderr)
        return out.stdout.strip()

    def test_key_and_iv_are_found_in_either_order(self):
        filler = b"\x7fELF" + b"\x00" * 64 + b"v27.0.6 build 46330\x00" + b"\x01" * 32
        want = f"{self.KEY.decode()} {self.IV.decode()}"
        self.assertEqual(self.find(filler + self.KEY + b"\x00" + self.IV + b"\x00" + filler), want)
        self.assertEqual(self.find(filler + self.IV + b"\x00\x00\x00" + self.KEY + filler), want)

    def test_anything_else_gives_nothing(self):
        for image in (
            b"\x7fELF" + b"\x00" * 64,                                     # neither
            self.KEY + b"\x00",                                           # key only
            self.KEY + b"\x00" + self.IV + b"\x00" + self.IV + b"\x00",   # two IVs
            self.KEY + b"\x00" + self.IV + b"\x00" + b"1" * 20 + b"\x00",  # a third long run
            self.KEY + b"1" + b"\x00" + self.IV + b"\x00",                 # 33 digits
        ):
            self.assertEqual(self.find(image), "(none)", image)

    def test_a_missing_file_gives_nothing(self):
        out = subprocess.run([str(self.binary), "--stock-wifi-key", "/nonexistent/NetManager"],
                             capture_output=True, text=True)
        self.assertEqual(out.stdout.strip(), "(none)")


class TestEnvelopeParser(unittest.TestCase):
    """Malformed coordinator frames must be refused cleanly, never reach memcpy."""

    HARNESS = r"""
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>
#include "swupdate.h"
int main(void)
{
    /* Field 8 (type name), length 0xffffffff, in a 7-byte envelope. */
    static const unsigned char frame[] = {0, 0, 0, 7, 0x42, 0xff, 0xff, 0xff, 0xff, 0x0f, 0};
    int sv[2];
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, sv)) return 2;
    if (write(sv[1], frame, sizeof frame) != (ssize_t)sizeof frame) return 2;
    struct swu_link link = {-1, sv[0]};
    struct swu_message m;
    int n = swu_receive(&link, &m);
    struct swu_start start;
    swu_parse_start(frame + 4, 7, &start);
    unsigned long long count, eos;
    const unsigned char *data;
    int data_len;
    static const unsigned char data_frame[] = {0x12, 0xff, 0xff, 0xff, 0xff, 0x0f};
    swu_parse_data_response(data_frame, sizeof data_frame, &count, &data, &data_len, &eos);
    printf("%d [%s] %d\n", n, m.type_name, data_len);
    return 0;
}
"""

    def test_oversized_length_is_refused(self):
        with scratch_dir() as tmp:
            source = Path(tmp) / "envelope_harness.c"
            binary = Path(tmp) / "envelope-harness"
            source.write_text(self.HARNESS)
            compile_with(["cc", "-O1", "-Wall", "-I" + str(SOURCE), "-o", str(binary), str(source),
                          str(SOURCE / "swupdate.c")], tmp)
            result = subprocess.run([str(binary)], capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "7 [] 0")


class TestInstallerOffline(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = scratch_dir()
        cls.port = free_port()
        cls.binary = build_installer(cls.tmp.name, port=cls.port)
        cls.stream, cls.payloads = make_package(cls.tmp.name)

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def drive(self, corrupt_at=None, stream=None):
        coordinator = Coordinator(self.stream if stream is None else stream, corrupt_at,
                                  declared=len(self.stream))
        env = dict(os.environ, TMPDIR=self.tmp.name)
        process = subprocess.Popen([str(self.binary)], stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, env=env)
        conn = None
        try:
            conn = await_listener(process, getattr(self, "port", PORT))
            self.assertIsNotNone(conn, "installer never listened on :%d" % getattr(self, "port", PORT))
            thread = threading.Thread(target=coordinator.run, args=(conn,))
            thread.start()
            thread.join(60)
            process.wait(30)
        finally:
            if conn:
                conn.close()
            if process.poll() is None:
                process.kill()
        return coordinator, process.returncode, process.stdout.read().decode("utf-8", "replace")

    def test_installer_verifies_every_module(self):
        coordinator, code, log = self.drive()
        self.assertIsNone(coordinator.error, coordinator.error)
        self.assertEqual(code, 0, log)
        self.assertIn("module chain verified", log)
        self.assertIn("no partition was written", log)
        for name in build_stu.WLA_PROBE_MODULES[1:]:
            self.assertIn(f"{name}: ", log)

    def test_pull_framing_matches_stock(self):
        coordinator, _code, log = self.drive()
        self.assertEqual(coordinator.requests[0], 2, log)
        self.assertEqual(coordinator.requests, list(range(2, 2 + len(coordinator.requests))))

    def test_corrupt_payload_is_refused_without_writing(self):
        # Flip a byte inside the second module's payload: the header CRC still matches, the
        # image CRC does not, and the installer must stop there.
        coordinator, code, log = self.drive(corrupt_at=build_stu.HDR + 32)
        self.assertNotEqual(code, 0, log)
        self.assertIn("does not match header", log)
        self.assertNotIn("module chain verified", log)
        del coordinator

    def test_a_stream_cut_at_a_module_boundary_is_refused(self):
        # Stop right after the first module and its shared tail, the one place where the
        # installer sees a clean end of stream: the size stock declared must still be met.
        size = struct.unpack(">I", self.stream[build_stu.SIZE_OFF:build_stu.SIZE_OFF + 4])[0]
        _coordinator, code, log = self.drive(stream=self.stream[:build_stu.HDR + size])
        self.assertNotEqual(code, 0, log)
        self.assertIn("module chain ended at %d of %d declared bytes"
                      % (build_stu.HDR + size, len(self.stream)), log)
        self.assertNotIn("module chain verified", log)




ENV_SIZE = 0x2000       # CONFIG_ENV_SIZE on this board, measured against its own mtd5 dump
ENV_PARTITION = 0x20000


def env_image(partition_size=ENV_PARTITION, bootcmd=b"bootcmd=run nand_boot;"):
    """A U-Boot environment the installer has to parse and rewrite: CRC32 over an 8 KiB body of
    NUL-separated entries, then the rest of the partition erased. The size matters -- a CRC over
    the whole partition is what made the board ignore the first real slot switch."""
    import zlib
    entries = b"\x00".join([
        b"baudrate=115200", bootcmd,
        b"nand_args2=run bootargs_defaults;setenv bootargs ${bootargs} ubi.mtd=9,2048",
        b"bootdelay=1"]) + b"\x00\x00"
    data = entries + b"\xff" * (ENV_SIZE - 4 - len(entries))
    return (struct.pack("<I", zlib.crc32(data) & 0xFFFFFFFF) + data
            + b"\xff" * (partition_size - ENV_SIZE))


def env_bootcmd(image):
    body = image[4:]
    for entry in body.split(b"\x00"):
        if entry.startswith(b"bootcmd="):
            return entry
    return None


def env_entry(image, name):
    for entry in image[4:].split(b"\x00"):
        if entry.startswith(name + b"="):
            return entry
    return None


class TestInstallerTransaction(unittest.TestCase):
    """The NAND build, driven end to end against the file backend: erase, stream, verify,
    activate, switch the bootloader. Same source the board runs."""

    KERNEL = 640 * 1024
    ROOTFS = 2 * 1024 * 1024

    @classmethod
    def setUpClass(cls):
        cls.tmp = scratch_dir()
        cls.port = free_port()
        cls.binary = build_installer(cls.tmp.name, nand=True, port=cls.port)
        cls.stream, cls.payloads = make_package(cls.tmp.name, {
            "linuxPatched": cls.KERNEL, "ubi.img": cls.ROOTFS})

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def flash(self):
        """A fresh set of partition files, pre-filled the way erased NAND reads."""
        root = Path(tempfile.mkdtemp(dir=self.tmp.name))
        (root / "NAND.Kernel-Pong").write_bytes(b"\x00" * (5 * 1024 * 1024))
        (root / "NAND.File-System-Pong").write_bytes(b"\x00" * (4 * 1024 * 1024))
        (root / "NAND.U-Boot-Env").write_bytes(env_image())
        return root

    def drive(self, root, corrupt_at=None, hangup_at_eos=False, variant="binky", candidate="9"):
        coordinator = Coordinator(self.stream, corrupt_at, hangup_at_eos)
        env = dict(os.environ, SOUNDTAP_MTD_DIR=str(root), SOUNDTAP_CANDIDATE_ROOTFS=candidate,
                   SOUNDTAP_NO_REBOOT="1")
        if variant is not None:
            env["SOUNDTAP_VARIANT"] = variant
        process = subprocess.Popen([str(self.binary)], stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, env=env)
        conn = None
        try:
            conn = await_listener(process, getattr(self, "port", PORT))
            self.assertIsNotNone(conn, "installer never listened on :%d" % getattr(self, "port", PORT))
            thread = threading.Thread(target=coordinator.run, args=(conn,))
            thread.start()
            thread.join(120)
            process.wait(60)
        finally:
            if conn:
                conn.close()
            if process.poll() is None:
                process.kill()
            log = process.stdout.read().decode("utf-8", "replace")
            process.stdout.close()
        return process.returncode, log

    def test_install_writes_verifies_and_switches_slot(self):
        root = self.flash()
        code, log = self.drive(root)
        self.assertEqual(code, 0, log)
        kernel = (root / "NAND.Kernel-Pong").read_bytes()
        rootfs = (root / "NAND.File-System-Pong").read_bytes()
        self.assertEqual(kernel[:self.KERNEL], self.payloads["linuxPatched"], "kernel mismatch")
        self.assertEqual(rootfs[:self.ROOTFS], self.payloads["ubi.img"], "rootfs mismatch")
        self.assertIn("readback verified", log)
        self.assertEqual(env_bootcmd((root / "NAND.U-Boot-Env").read_bytes()),
                          b"bootcmd=run nand_boot2;", log)
        self.assertEqual(env_entry((root / "NAND.U-Boot-Env").read_bytes(), b"nand_args2"),
                         b"nand_args2=run bootargs_defaults;setenv bootargs ${bootargs} ubi.mtd=9,2048"
                         b" soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048",
                         log)

    def test_bad_blocks_are_skipped_on_write_and_on_readback(self):
        """A bad first kernel block moves the deferred block's home; a bad rootfs block past the
        first verify chunk used to make the readback read from a logical offset."""
        root = self.flash()
        (root / "NAND.Kernel-Pong.bad").write_text("0\n")
        (root / "NAND.File-System-Pong.bad").write_text("2\n")
        code, log = self.drive(root)
        self.assertEqual(code, 0, log)
        self.assertIn("readback verified", log)
        block = 0x20000
        kernel = (root / "NAND.Kernel-Pong").read_bytes()
        self.assertEqual(kernel[:block], b"\x00" * block, "a bad block must not be touched")
        self.assertEqual(kernel[block:block + self.KERNEL], self.payloads["linuxPatched"])
        rootfs = (root / "NAND.File-System-Pong").read_bytes()
        payload = self.payloads["ubi.img"]
        self.assertEqual(rootfs[:2 * block], payload[:2 * block])
        self.assertEqual(rootfs[2 * block:3 * block], b"\x00" * block)
        self.assertEqual(rootfs[3 * block:block + self.ROOTFS], payload[2 * block:])

    def test_a_coordinator_that_hangs_up_does_not_abort_the_install(self):
        """The link is written with plain write(), so a closed socket used to deliver SIGPIPE
        and kill the installer where it stood. On 2026-09-16 that happened on the real board
        after the slot had already been switched: the install was complete and the speaker sat
        on its old firmware until it was power-cycled by hand. Everything past the last data
        record has to survive the coordinator going away."""
        root = self.flash()
        code, log = self.drive(root, hangup_at_eos=True)
        self.assertEqual(code, 0, log)
        self.assertIn("readback verified", log)
        self.assertEqual(env_bootcmd((root / "NAND.U-Boot-Env").read_bytes()),
                         b"bootcmd=run nand_boot2;", log)
        # SOUNDTAP_NO_REBOOT is set for the harness; reaching that line is the proof that the
        # real build would have reached reboot().
        self.assertIn("reboot suppressed by SOUNDTAP_NO_REBOOT", log)

    def test_untouched_entries_survive_the_env_rewrite(self):
        root = self.flash()
        self.drive(root)
        image = (root / "NAND.U-Boot-Env").read_bytes()
        body = image[4:].split(b"\xff")[0]
        self.assertIn(b"baudrate=115200", body)
        self.assertIn(b"bootdelay=1", body)
        # Bose's U-Boot keeps no boot count in its environment.
        self.assertNotIn(b"upgrade_available", body)
        import zlib
        self.assertEqual(struct.unpack("<I", image[:4])[0],
                         zlib.crc32(image[4:ENV_SIZE]) & 0xFFFFFFFF,
                         "environment CRC must cover exactly the 8 KiB environment")
        self.assertEqual(image[ENV_SIZE:ENV_SIZE + 64], b"\xff" * 64,
                         "the rest of the environment block must stay erased")

    def test_an_environment_with_a_bad_crc_is_not_rewritten(self):
        root = self.flash()
        image = bytearray(env_image())
        image[0] ^= 0xFF
        (root / "NAND.U-Boot-Env").write_bytes(image)
        code, log = self.drive(root)
        self.assertNotEqual(code, 0, log)
        self.assertIn("environment CRC does not match", log)
        self.assertEqual((root / "NAND.U-Boot-Env").read_bytes(), bytes(image), log)

    def test_an_unterminated_environment_entry_is_refused(self):
        """strlen ran past the 8 KiB buffer when no NUL followed the last entry."""
        import zlib
        data = b"A" * (ENV_SIZE - 4)
        image = (struct.pack("<I", zlib.crc32(data) & 0xFFFFFFFF) + data
                 + b"\xff" * (ENV_PARTITION - ENV_SIZE))
        root = self.flash()
        (root / "NAND.U-Boot-Env").write_bytes(image)
        code, log = self.drive(root)
        self.assertNotEqual(code, 0, log)
        self.assertIn("has no terminator", log)
        self.assertEqual((root / "NAND.U-Boot-Env").read_bytes(), image, log)

    def test_corrupt_rootfs_leaves_the_candidate_kernel_unbootable(self):
        # Flip a byte inside ubi.img: the kernel is already streamed at that point, so this is
        # exactly the case the deferred first block exists for.
        rootfs_at = self.stream.index(self.payloads["ubi.img"][:64])
        root = self.flash()
        code, log = self.drive(root, corrupt_at=rootfs_at + 128)
        self.assertNotEqual(code, 0, log)
        kernel = (root / "NAND.Kernel-Pong").read_bytes()
        self.assertEqual(kernel[:0x20000], b"\xff" * 0x20000,
                         "first kernel block must stay erased when the package is bad")
        self.assertEqual(env_bootcmd((root / "NAND.U-Boot-Env").read_bytes()),
                          b"bootcmd=run nand_boot;", "a failed install must not switch slots")

    def test_a_kernel_built_for_another_model_is_never_made_bootable(self):
        """The suffix picks the slot and the CRC only proves the bytes arrived; the uImage name is
        what says whose kernel it is. An ST20 kernel on a Wireless Link must stay unbootable."""
        stream, _ = make_package(self.tmp.name, {"linuxPatched": self.KERNEL, "ubi.img": self.ROOTFS},
                                 heads={"linuxPatched": uimage_head(b"Linub-6.12 SoundTap st20")})
        root = self.flash()
        saved, self.stream = self.stream, stream
        try:
            code, log = self.drive(root)
        finally:
            self.stream = saved
        self.assertNotEqual(code, 0, log)
        self.assertIn("built for another model", log)
        kernel = (root / "NAND.Kernel-Pong").read_bytes()
        self.assertEqual(kernel[:0x20000], b"\xff" * 0x20000, log)
        self.assertEqual(env_bootcmd((root / "NAND.U-Boot-Env").read_bytes()),
                         b"bootcmd=run nand_boot;", log)

    def test_the_shared_st10_kernel_installs_on_a_wireless_link(self):
        stream, payloads = make_package(self.tmp.name, {"linuxPatched": self.KERNEL, "ubi.img": self.ROOTFS},
                                        heads={"linuxPatched": uimage_head(b"Linub-6.12 SoundTap st10")})
        root = self.flash()
        saved, self.stream = self.stream, stream
        try:
            code, log = self.drive(root)
        finally:
            self.stream = saved
        self.assertEqual(code, 0, log)
        kernel = (root / "NAND.Kernel-Pong").read_bytes()
        self.assertEqual(kernel[:self.KERNEL], payloads["linuxPatched"], log)


BLOCK = 0x20000


def marked(root, name):
    """The blocks of a partition file marked bad, factory marks included, as the backend keeps
    them in <name>.bad."""
    path = root / f"{name}.bad"
    return [int(n) for n in path.read_text().split()] if path.exists() else []


def skip_bad(data, bad, length):
    """What a reader that skips bad blocks (U-Boot's nand read, UBI, the installer's own
    readback) gets from the first `length` bytes of good blocks."""
    good = b"".join(data[i:i + BLOCK] for i in range(0, len(data), BLOCK) if i // BLOCK not in bad)
    return good[:length]


class TestBadBlockMarking(unittest.TestCase):
    """A block of the candidate slot whose erase or program the driver fails with EIO is marked
    bad and written around, as ubiformat and nandwrite -m do; nothing else ever marks a block.
    The file backend fails what <partition>.fail names and keeps marks in <partition>.bad. The
    kernel is five blocks, the rootfs sixteen of a 32-block partition."""

    KERNEL = TestInstallerTransaction.KERNEL
    ROOTFS = TestInstallerTransaction.ROOTFS
    flash_files = TestInstallerTransaction.flash
    drive = TestInstallerTransaction.drive

    @classmethod
    def setUpClass(cls):
        cls.tmp = scratch_dir()
        cls.port = free_port()
        cls.binary = build_installer(cls.tmp.name, nand=True, port=cls.port)
        cls.stream, cls.payloads = make_package(cls.tmp.name, {
            "linuxPatched": cls.KERNEL, "ubi.img": cls.ROOTFS})

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def flash(self, faults):
        """faults: partition file -> the lines of its .fail file."""
        root = self.flash_files()
        for name, lines in faults.items():
            (root / f"{name}.fail").write_text("\n".join(lines) + "\n")
        return root

    def kernel_and_rootfs(self, root):
        kernel = (root / "NAND.Kernel-Pong").read_bytes()
        rootfs = (root / "NAND.File-System-Pong").read_bytes()
        return (skip_bad(kernel, marked(root, "NAND.Kernel-Pong"), self.KERNEL),
                skip_bad(rootfs, marked(root, "NAND.File-System-Pong"), self.ROOTFS))

    def assertInstalled(self, root, code, log):
        self.assertEqual(code, 0, log)
        kernel, rootfs = self.kernel_and_rootfs(root)
        self.assertEqual(kernel, self.payloads["linuxPatched"], log)
        self.assertEqual(rootfs, self.payloads["ubi.img"], log)
        self.assertEqual(env_bootcmd((root / "NAND.U-Boot-Env").read_bytes()), b"bootcmd=run nand_boot2;", log)

    def assertNothingSelected(self, root, code, log):
        self.assertNotEqual(code, 0, log)
        self.assertEqual(env_bootcmd((root / "NAND.U-Boot-Env").read_bytes()), b"bootcmd=run nand_boot;", log)
        self.assertNotIn("slot images verified", log)

    def test_a_block_that_fails_to_erase_is_marked_and_skipped(self):
        root = self.flash({"NAND.File-System-Pong": ["erase 1"]})
        code, log = self.drive(root)
        self.assertEqual(marked(root, "NAND.File-System-Pong"), [1], log)
        self.assertIn("NAND.File-System-Pong block 1 (offset 131072) marked bad: erase failed errno=5", log)
        self.assertInstalled(root, code, log)

    def test_a_block_that_fails_to_program_goes_to_the_next_good_block(self):
        # Block 3 takes 64 KiB, then fails: those 64 KiB go into block 4 again, and the rest follows.
        root = self.flash({"NAND.File-System-Pong": ["write 3 EIO 65536"]})
        code, log = self.drive(root)
        self.assertEqual(marked(root, "NAND.File-System-Pong"), [3], log)
        self.assertIn("NAND.File-System-Pong block 3 (offset 393216) marked bad: write failed errno=5", log)
        self.assertIn("NAND.File-System-Pong block 4 takes over from block 3, 65536 bytes written again", log)
        self.assertInstalled(root, code, log)
        rootfs = (root / "NAND.File-System-Pong").read_bytes()
        payload = self.payloads["ubi.img"]
        self.assertEqual(rootfs[4 * BLOCK:5 * BLOCK], payload[3 * BLOCK:4 * BLOCK])

    def test_a_kernel_block_that_fails_to_program_goes_to_the_next_good_block(self):
        root = self.flash({"NAND.Kernel-Pong": ["write 2 EIO 32768"]})
        code, log = self.drive(root)
        self.assertEqual(marked(root, "NAND.Kernel-Pong"), [2], log)
        self.assertInstalled(root, code, log)

    def test_a_block_failing_after_a_failed_block_is_marked_too(self):
        # Block 3 fails after 16 KiB, and block 4 fails as those 16 KiB go into it.
        root = self.flash({"NAND.File-System-Pong": ["write 3 EIO 16384", "write 4"]})
        code, log = self.drive(root)
        self.assertEqual(marked(root, "NAND.File-System-Pong"), [3, 4], log)
        self.assertInstalled(root, code, log)

    def test_the_kernels_first_block_is_marked_and_the_slot_stays_unbootable(self):
        # The deferred first block goes in last, behind data already on flash: it cannot move.
        root = self.flash({"NAND.Kernel-Pong": ["write 0"]})
        code, log = self.drive(root)
        self.assertNothingSelected(root, code, log)
        self.assertEqual(marked(root, "NAND.Kernel-Pong"), [0], log)
        self.assertIn("is not written again elsewhere", log)
        kernel = (root / "NAND.Kernel-Pong").read_bytes()
        self.assertEqual(kernel[BLOCK:self.KERNEL], self.payloads["linuxPatched"][BLOCK:],
                         "the blocks behind it are left as written")
        self.assertNotEqual(kernel[BLOCK:BLOCK + 4], b"\x27\x05\x19\x56",
                            "the first good block now holds no uImage header: nothing boots it")

    def test_a_failure_that_is_not_eio_marks_nothing(self):
        for fault in ("write 3 ETIMEDOUT", "erase 2 ETIMEDOUT", "erase 2 EROFS"):
            with self.subTest(fault=fault):
                root = self.flash({"NAND.File-System-Pong": [fault]})
                code, log = self.drive(root)
                self.assertNothingSelected(root, code, log)
                self.assertEqual(marked(root, "NAND.File-System-Pong"), [], log)
                self.assertNotIn("marked bad:", log)

    def test_an_ecc_error_on_readback_marks_nothing(self):
        root = self.flash({"NAND.File-System-Pong": ["read 2"]})
        code, log = self.drive(root)
        self.assertNothingSelected(root, code, log)
        self.assertIn("read of", log)
        self.assertEqual(marked(root, "NAND.File-System-Pong"), [], log)

    def test_marking_stops_at_two_blocks_of_a_partition(self):
        root = self.flash({"NAND.File-System-Pong": ["write 3", "write 4", "write 5"]})
        code, log = self.drive(root)
        self.assertNothingSelected(root, code, log)
        self.assertEqual(marked(root, "NAND.File-System-Pong"), [3, 4], log)
        self.assertIn("NAND.File-System-Pong block 5: write failed errno=5; 2 marked bad in this "
                      "partition and 2 in this install already, the most allowed (2, 3)", log)

    def test_marking_stops_at_three_blocks_of_an_install(self):
        root = self.flash({"NAND.Kernel-Pong": ["write 2", "write 3"], "NAND.File-System-Pong": ["write 5", "write 6"]})
        code, log = self.drive(root)
        self.assertNothingSelected(root, code, log)
        self.assertEqual(marked(root, "NAND.Kernel-Pong"), [2, 3], log)
        self.assertEqual(marked(root, "NAND.File-System-Pong"), [5], log)
        self.assertIn("the most allowed (2, 3)", log)

    def test_factory_bad_blocks_do_not_count(self):
        root = self.flash({"NAND.File-System-Pong": ["write 6", "write 7"]})
        (root / "NAND.File-System-Pong.bad").write_text("2\n3\n4\n")
        code, log = self.drive(root)
        self.assertEqual(marked(root, "NAND.File-System-Pong"), [2, 3, 4, 6, 7], log)
        self.assertInstalled(root, code, log)

    def test_a_mark_that_does_not_take_stops_the_install(self):
        # A write-protected chip fails the erase and refuses the mark (EROFS).
        root = self.flash({"NAND.File-System-Pong": ["erase 2", "markbad 2"]})
        code, log = self.drive(root)
        self.assertNothingSelected(root, code, log)
        self.assertEqual(marked(root, "NAND.File-System-Pong"), [], log)
        self.assertIn("and marking it bad failed errno=", log)

    def test_the_environment_partition_is_never_marked(self):
        root = self.flash({"NAND.U-Boot-Env": ["write 0"]})
        code, log = self.drive(root)
        self.assertNotEqual(code, 0, log)
        self.assertEqual(marked(root, "NAND.U-Boot-Env"), [], log)
        self.assertNotIn("marked bad:", log)


class TestMarkBadPartitions(unittest.TestCase):
    """mtd_allow_mark_bad takes the four slot partitions and nothing else; a partition it was
    not given marks nothing whatever fails. mtd10-mtd12 are never written by an install, so this
    holds that rule apart from any install."""

    HARNESS = r'''
#include <stdio.h>
#include "mtd.h"
int main(int argc, char **argv)
{
    for (int i = 1; i < argc; i++) {
        struct mtd_part part;
        if (mtd_open(&part, argv[i], 1) != 0) return 2;
        int allowed = mtd_allow_mark_bad(&part) == 0;
        int erased = mtd_erase_all(&part);
        printf("%s allowed=%d erased=%d\n", argv[i], allowed, erased);
        mtd_close(&part);
    }
    printf("marked=%u\n", mtd_marked_this_run());
    return 0;
}
'''
    SLOTS = ("NAND.Kernel-Ping", "NAND.Kernel-Pong", "NAND.File-System-Ping", "NAND.File-System-Pong")
    OTHERS = ("NAND.Persistent-Storage", "NAND.Update-Staging", "NAND.Manufacturing-Data", "NAND.SPL",
              "NAND.SPL.backup1", "NAND.U-Boot", "NAND.U-Boot-Env")

    def test_only_slot_partitions_mark(self):
        with scratch_dir() as tmp:
            tmp = Path(tmp)
            (tmp / "harness.c").write_text(self.HARNESS)
            binary = tmp / "harness"
            compile_with(["cc", "-O1", "-Wall", "-Wextra", "-I", str(SOURCE), "-o", str(binary),
                          str(tmp / "harness.c"), str(SOURCE / "mtd.c"), str(SOURCE / "swupdate.c")], tmp)
            for name in self.SLOTS + self.OTHERS:
                (tmp / name).write_bytes(b"\0" * (4 * BLOCK))
                (tmp / f"{name}.fail").write_text("erase 1\n")
            out = subprocess.run([str(binary), *self.OTHERS, self.SLOTS[0]], env=dict(os.environ, SOUNDTAP_MTD_DIR=str(tmp)),
                                 capture_output=True, text=True, check=True).stdout
            for name in self.OTHERS:
                self.assertIn(f"{name} allowed=0 erased=-1", out)
                self.assertFalse((tmp / f"{name}.bad").exists(), name)
            self.assertIn(f"{self.SLOTS[0]} allowed=1 erased=3", out)
            self.assertIn("marked=1", out)
            out = subprocess.run([str(binary), *self.SLOTS[1:]], env=dict(os.environ, SOUNDTAP_MTD_DIR=str(tmp)),
                                 capture_output=True, text=True, check=True).stdout
            for name in self.SLOTS[1:]:
                self.assertIn(f"{name} allowed=1 erased=3", out)


def bose_uboot(size=300 * 1024):
    """A U-Boot that is not ours in the first copy, as stock leaves NAND.U-Boot: a legacy uImage
    header and its data, no spare behind it."""
    data = bytes(range(256)) * (size // 256)
    header = struct.pack(">IIIIIIIBBBB32s", 0x27051956, 0, 0, len(data), 0x80800000, 0x80800000,
                         0, 5, 2, 5, 0, b"Bose U-Boot")
    return header + data


def boot_chain_writes(log):
    """The boot partitions the installer wrote, in order: (partition, offset)."""
    out = []
    for line in log.splitlines():
        m = re.search(r"wla-installer: (NAND\.(?:SPL[.\w]*|U-Boot)): \d+ bytes at (\d+) written and verified", line)
        if m:
            out.append((m.group(1), int(m.group(2))))
    return out


# The ROM and the SPLs as uboot-2026.07-spl-second-uboot.patch has them: the ROM runs the first
# whole SPL of NAND.SPL and its backups; Bose's SPL boots the first U-Boot copy; ours boots the
# first copy if it is whole and ours, else the spare if that is whole and ours, else a whole
# first copy. Returns (spl, uboot) of the chain that runs, or None.
def booted(state):
    for spl in ("NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"):
        if state[spl] in ("ours", "bose"):
            first, spare = state[("NAND.U-Boot", 0)], state[("NAND.U-Boot", 0x100000)]
            if state[spl] == "bose":
                return ("bose", first) if first in ("ours", "bose") else None
            if first == "ours":
                return ("ours", first)
            if spare == "ours":
                return ("ours", spare)
            return ("ours", first) if first == "bose" else None
    return None


def redundant_env(test, root):
    """The entries of the redundant environment an install with a boot chain leaves: both copies
    check out, hold the same entries, and the primary is the newer."""
    primary = (root / "NAND.U-Boot-Env").read_bytes()
    copy = (root / "NAND.U-Boot").read_bytes()[uboot_env.REDUNDANT_OFFSET:]
    entries, flag = uboot_env.parse(primary)
    test.assertEqual(flag, uboot_env.FRESH_PRIMARY_FLAG)
    test.assertEqual(uboot_env.parse(copy), (entries, uboot_env.FRESH_REDUNDANT_FLAG))
    # Byte for byte what internal/ubootenv and the recovery tools write for these entries.
    test.assertEqual(primary[:uboot_env.SIZE], uboot_env.encode_copy(entries, uboot_env.FRESH_PRIMARY_FLAG))
    return entries


class TestBootloaderPhase(unittest.TestCase):
    """The boot chain replacement: U-Boot first, the SPL backups next, and the copy the mask ROM
    reads last, each read back before the next one starts."""

    KERNEL = 320 * 1024
    ROOTFS = 1024 * 1024
    SPL = 17 * 1024
    UBOOT = 520 * 1024
    running_uboot = bose_uboot()  # what NAND.U-Boot holds before the install

    @classmethod
    def setUpClass(cls):
        cls.tmp = scratch_dir()
        cls.port = free_port()
        cls.binary = build_installer(cls.tmp.name, nand=True, bootloaders=True, port=cls.port)
        cls.stream, cls.payloads = make_package(cls.tmp.name, {
            "soundtap-kernel.rhino": cls.KERNEL,
            "soundtap-kernel.binky": cls.KERNEL,
            "soundtap-kernel.spotty": cls.KERNEL,
            "soundtap-rootfs": cls.ROOTFS,
            "soundtap-spl.binky": cls.SPL,
            "soundtap-uboot.binky": cls.UBOOT,
            "soundtap-spl.spotty": cls.SPL,
        }, build_stu.SOUNDTAP_TRI_MODULES, "soundtap-tri")

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def run_install(self, variant="binky", compatible=None, bad=None, faults=None):
        """bad maps a partition file to the blocks of it that are marked bad, faults to the
        lines of its .fail file (TestBadBlockMarking)."""
        root = Path(tempfile.mkdtemp(dir=self.tmp.name))
        (root / "NAND.Kernel-Pong").write_bytes(b"\x00" * (5 * 1024 * 1024))
        (root / "NAND.File-System-Pong").write_bytes(b"\x00" * (2 * 1024 * 1024))
        (root / "NAND.U-Boot-Env").write_bytes(env_image())
        (root / "NAND.U-Boot").write_bytes(self.running_uboot.ljust(0x1e0000, b"\x00"))
        for name in ("NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"):
            (root / name).write_bytes(b"\x00" * 0x20000)
        for name, blocks in (bad or {}).items():
            (root / f"{name}.bad").write_text("".join(f"{b}\n" for b in blocks))
        for name, lines in (faults or {}).items():
            (root / f"{name}.fail").write_text("\n".join(lines) + "\n")
        coordinator = Coordinator(self.stream)
        env = dict(os.environ, SOUNDTAP_MTD_DIR=str(root), SOUNDTAP_CANDIDATE_ROOTFS="9",
                   SOUNDTAP_NO_REBOOT="1", SOUNDTAP_VARIANT=variant,
                   SOUNDTAP_DT_COMPATIBLE=str(root / "compatible"))
        if compatible is not None:
            (root / "compatible").write_bytes(compatible)
        process = subprocess.Popen([str(self.binary)], stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, env=env)
        conn = None
        try:
            conn = await_listener(process, getattr(self, "port", PORT))
            self.assertIsNotNone(conn, "installer never listened on :%d" % getattr(self, "port", PORT))
            thread = threading.Thread(target=coordinator.run, args=(conn,))
            thread.start()
            thread.join(120)
            process.wait(60)
        finally:
            if conn:
                conn.close()
            if process.poll() is None:
                process.kill()
            log = process.stdout.read().decode("utf-8", "replace")
            process.stdout.close()
        return root, process.returncode, log

    def test_every_boot_partition_gets_the_image(self):
        root, code, log = self.run_install()
        self.assertEqual(code, 0, log)
        spl = self.payloads["soundtap-spl.binky"]
        for name in ("NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"):
            self.assertEqual((root / name).read_bytes()[:self.SPL], spl, f"{name} mismatch")
        uboot = (root / "NAND.U-Boot").read_bytes()
        self.assertEqual(uboot[:self.UBOOT], self.payloads["soundtap-uboot.binky"])
        self.assertEqual(uboot[0x100000:0x100000 + self.UBOOT], self.payloads["soundtap-uboot.binky"],
                         "the spare U-Boot behind the primary")

    def test_the_first_uboot_copy_is_written_last(self):
        """soundtapd's order (ADR 0009): the spare, the SPL backups, NAND.SPL, the first copy."""
        _root, code, log = self.run_install()
        self.assertEqual(code, 0, log)
        self.assertEqual(boot_chain_writes(log), [
            ("NAND.U-Boot", 0x100000), ("NAND.SPL.backup1", 0), ("NAND.SPL.backup2", 0),
            ("NAND.SPL.backup3", 0), ("NAND.SPL", 0), ("NAND.U-Boot", 0)], log)

    def test_every_cut_in_the_boot_chain_write_boots(self):
        """Replays the installer's own write order with a cut after every half write. Every state
        boots, and our SPL never runs Bose's U-Boot: it has our verified spare by then."""
        _root, code, log = self.run_install()
        self.assertEqual(code, 0, log)
        state = {"NAND.SPL": "bose", "NAND.SPL.backup1": None, "NAND.SPL.backup2": None,
                 "NAND.SPL.backup3": None, ("NAND.U-Boot", 0): "bose", ("NAND.U-Boot", 0x100000): None}
        for part, offset in boot_chain_writes(log):
            slot = (part, offset) if part == "NAND.U-Boot" else part
            for content in ("torn", "ours"):
                state[slot] = content
                chain = booted(state)
                self.assertIsNotNone(chain, f"a cut in {slot} leaves {state} without a chain that boots")
                self.assertNotEqual(chain, ("ours", "bose"), f"our SPL runs Bose's U-Boot at {state}")
        self.assertEqual(booted(state), ("ours", "ours"))

    def test_a_running_uboot_that_reaches_the_spare_is_refused(self):
        """The spare goes in while Bose's U-Boot still boots from the first copy, so that copy has
        to end before it. One that does not, or no uImage at all, is refused before any boot
        partition is touched."""
        for running in (bose_uboot(0x100000), b"\xff" * 64):
            saved, self.running_uboot = self.running_uboot, running
            try:
                root, code, log = self.run_install()
            finally:
                self.running_uboot = saved
            self.assertNotEqual(code, 0, log)
            self.assertEqual(boot_chain_writes(log), [], log)
            self.assertEqual((root / "NAND.U-Boot").read_bytes()[:len(running)], running, log)
            self.assertIn("the boot chain is left as it is", log)

    def test_bad_blocks_in_the_first_half_never_push_uboot_into_the_spare(self):
        """Four bad blocks among the first copy's eight leave four for a five-block U-Boot. The
        write used to skip on into block 8 and only then notice it had passed its range: the
        spare, written and verified a moment before, was programmed over without an erase."""
        root, code, log = self.run_install(bad={"NAND.U-Boot": [4, 5, 6, 7]})
        self.assertNotEqual(code, 0, log)
        uboot = (root / "NAND.U-Boot").read_bytes()
        self.assertEqual(uboot[0x100000:0x100000 + self.UBOOT], self.payloads["soundtap-uboot.binky"],
                         "the spare must stay whole")
        self.assertIn("ran out of space before 1048576", log)
        self.assertEqual(boot_chain_writes(log), [
            ("NAND.U-Boot", 0x100000), ("NAND.SPL.backup1", 0), ("NAND.SPL.backup2", 0),
            ("NAND.SPL.backup3", 0), ("NAND.SPL", 0)], log)
        self.assertEqual(env_bootcmd((root / "NAND.U-Boot-Env").read_bytes()), b"bootcmd=run nand_boot;", log)

    def test_a_boot_partition_that_fails_to_program_is_never_marked(self):
        """The ROM and the SPL read the boot chain at fixed places: a copy whose block fails is
        refused, as before, and the block is left unmarked. The slot is not selected."""
        for name in ("NAND.SPL.backup1", "NAND.SPL", "NAND.U-Boot"):
            with self.subTest(partition=name):
                root, code, log = self.run_install(faults={name: ["write 0", "erase 0"] if name == "NAND.SPL"
                                                           else ["write 0"]})
                self.assertNotEqual(code, 0, log)
                self.assertNotIn("marked bad:", log)
                self.assertFalse((root / f"{name}.bad").exists(), log)
                self.assertEqual(env_bootcmd((root / "NAND.U-Boot-Env").read_bytes()), b"bootcmd=run nand_boot;", log)

    def test_the_slot_is_installed_before_any_boot_partition(self):
        _root, _code, log = self.run_install()
        lines = log.splitlines()
        slot = next(i for i, line in enumerate(lines) if "slot images verified" in line)
        boot = next(i for i, line in enumerate(lines) if "written and verified" in line)
        self.assertLess(slot, boot, "the boot chain must never be touched before the slot is in")

    def test_recipes_go_in_before_the_boot_chain(self):
        """Our U-Boot cannot run Bose's nand_boot, so the recipes (with bootcmd still on the
        running slot) are written before the first boot partition, and the switch comes last."""
        _root, code, log = self.run_install()
        self.assertEqual(code, 0, log)
        lines = log.splitlines()
        switches = [i for i, line in enumerate(lines) if "bootcmd ->" in line]
        boot = [i for i, line in enumerate(lines) if "written and verified" in line]
        self.assertEqual(len(switches), 2, log)
        self.assertIn('"bootcmd=run nand_boot;": written', lines[switches[0]], log)
        self.assertIn('"bootcmd=run nand_boot2;": written', lines[switches[1]], log)
        self.assertLess(switches[0], boot[0], log)
        self.assertGreater(switches[1], boot[-1], log)

    def test_rhino_takes_the_sm2_boot_chain(self):
        """The ST10 gets the Wireless Link's SPL in all four copies and its U-Boot."""
        root, code, log = self.run_install("rhino")
        self.assertEqual(code, 0, log)
        self.assertEqual((root / "NAND.Kernel-Pong").read_bytes()[:self.KERNEL],
                         self.payloads["soundtap-kernel.rhino"])
        self.assertEqual((root / "NAND.File-System-Pong").read_bytes()[:self.ROOTFS],
                         self.payloads["soundtap-rootfs"])
        for name in ("NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"):
            self.assertEqual((root / name).read_bytes()[:self.SPL],
                             self.payloads["soundtap-spl.binky"], name)
        self.assertEqual((root / "NAND.U-Boot").read_bytes()[:self.UBOOT],
                         self.payloads["soundtap-uboot.binky"])

    def test_ginger_takes_the_sm2_boot_chain(self):
        """The ST300 is the Wireless Link's module in a soundbar: its kernel, rootfs, SPL in all
        four copies and U-Boot, and the slot selected through a plain bootcmd. Our U-Boot counts
        boots, so no one-shot back to stock (decided 2026-11-04: our boot chain always ships)."""
        # Stock 27.0.6 on our ST300 reads exactly this root compatible (2026-11-04).
        root, code, log = self.run_install("ginger", compatible=b"bose,sm2\x00")
        self.assertEqual(code, 0, log)
        self.assertEqual((root / "NAND.Kernel-Pong").read_bytes()[:self.KERNEL],
                         self.payloads["soundtap-kernel.binky"])
        self.assertEqual((root / "NAND.File-System-Pong").read_bytes()[:self.ROOTFS],
                         self.payloads["soundtap-rootfs"])
        for name in ("NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"):
            self.assertEqual((root / name).read_bytes()[:self.SPL],
                             self.payloads["soundtap-spl.binky"], name)
        self.assertEqual((root / "NAND.U-Boot").read_bytes()[:self.UBOOT],
                         self.payloads["soundtap-uboot.binky"])
        self.assertIn('"bootcmd=run nand_boot2;": written', log)
        env = redundant_env(self, root)
        self.assertFalse([e for e in env if b"saveenv" in e], log)
        # A first boot that never comes up rolls back to what ran before the install.
        self.assertIn(b"altbootcmd=run nand_boot;", env, log)
        self.assertIn(b"bootlimit=4", env, log)
        self.assertEqual(len([e for e in env if e.startswith(b"altbootcmd=")]), 1, log)
        # Our U-Boot's console is UART0, the soundbar processor's link on the ST300: a byte from
        # it inside the factory bootdelay=1 would stop autoboot, so the delay check goes away.
        self.assertIn(b"bootdelay=-2", env, log)
        self.assertEqual(len([e for e in env if e.startswith(b"bootdelay=")]), 1, log)

    def test_the_boot_chain_leaves_the_redundant_environment(self):
        """Our U-Boot reads two 32 KiB copies (ADR 0010): the one in NAND.U-Boot's last block is
        erased before any U-Boot write, while Bose's U-Boot still boots from the 8 KiB copy, and
        only once the boot chain is verified are both written, that one first. No U-Boot write
        reaches its block."""
        root, code, log = self.run_install()
        self.assertEqual(code, 0, log)
        env = redundant_env(self, root)
        self.assertIn(b"bootcmd=run nand_boot2;", env, log)
        # The selection is unconfirmed: our U-Boot keeps its count in this environment until
        # soundtapd's boot confirm clears it, in the same write as bootcmd.
        self.assertIn(b"upgrade_available=1", env, log)
        self.assertFalse([e for e in env if e.startswith(b"bootcount=")], log)
        lines = log.splitlines()
        dropped = next(i for i, line in enumerate(lines) if "redundant environment block erased" in line)
        first_uboot = next(i for i, line in enumerate(lines) if "NAND.U-Boot: " in line and "written and verified" in line)
        self.assertIn("at 1048576", lines[first_uboot], log)
        chain = next(i for i, line in enumerate(lines) if "boot chain replaced" in line)
        copy_at = next(i for i, line in enumerate(lines) if "NAND.U-Boot: environment copy at 1835008" in line)
        primary_at = next(i for i, line in enumerate(lines) if "NAND.U-Boot-Env: environment copy at 0" in line)
        self.assertLess(dropped, first_uboot, log)
        self.assertLess(chain, copy_at, log)
        self.assertLess(copy_at, primary_at, log)
        uboot = (root / "NAND.U-Boot").read_bytes()
        spare = uboot[0x100000:uboot_env.REDUNDANT_OFFSET]
        self.assertEqual(spare[:self.UBOOT], self.payloads["soundtap-uboot.binky"])

    def test_ginger_leaves_the_soundbar_processor_alone(self):
        """The processor keeps its own firmware: the package carries none and the installer has
        no route to the link. Its calibration and the bass module depend on it."""
        names = stu_check.SOUNDTAP_MODULES + stu_check.SOUNDTAP_TRI_MODULES
        self.assertFalse([n for n in names if n == "STP_Update" or n.endswith(".bos")])
        # An absence: no install run can show a path the code lacks, so the source is read.
        source = (REPO / "os/installer/module/installer.c").read_text()
        self.assertNotIn("/dev/tty", source)
        root, code, log = self.run_install("ginger", compatible=b"bose,sm2\x00")
        self.assertEqual(code, 0, log)
        self.assertEqual([p.name for p in root.iterdir() if "tty" in p.name], [], log)

    def test_spotty_takes_its_spl_and_the_shared_uboot(self):
        """ST20: its own SPL in all four copies and the Wireless Link's U-Boot, written in the
        same order as on the other two, and the slot selected through bootcmd like theirs."""
        root, code, log = self.run_install("spotty")
        self.assertEqual(code, 0, log)
        self.assertEqual((root / "NAND.Kernel-Pong").read_bytes()[:self.KERNEL],
                         self.payloads["soundtap-kernel.spotty"])
        for name in ("NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"):
            self.assertEqual((root / name).read_bytes()[:self.SPL],
                             self.payloads["soundtap-spl.spotty"], name)
        self.assertEqual((root / "NAND.U-Boot").read_bytes()[:self.UBOOT],
                         self.payloads["soundtap-uboot.binky"])
        self.assertEqual([part for part, _ in boot_chain_writes(log)],
                         ["NAND.U-Boot", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3",
                          "NAND.SPL", "NAND.U-Boot"], log)
        self.assertIn('"bootcmd=run nand_boot2;": written', log)

    def test_mojo_installs_as_the_st20_with_its_own_name(self):
        """SoundTouch 30 Series I: the ST20's board, so the ST20's payloads, with "mojo" kept
        on the command line for its own codec script."""
        root, code, log = self.run_install("mojo")
        self.assertEqual(code, 0, log)
        self.assertEqual((root / "NAND.Kernel-Pong").read_bytes()[:self.KERNEL],
                         self.payloads["soundtap-kernel.spotty"])
        self.assertEqual((root / "NAND.SPL").read_bytes()[:self.SPL],
                         self.payloads["soundtap-spl.spotty"])
        env = (root / "NAND.U-Boot-Env").read_bytes()
        self.assertIn(b"variant=mojo module_type=scm", env, log)
        self.assertIn(b"/boot/spotty_scm.dtb", env, log)

    def test_series_iii_installs_as_the_st10_board_under_its_own_name(self):
        """An ST20 or ST30 Series III says variant=spotty or mojo like the Series I but carries
        the ST10's Shelby 2 module: it takes the ST10's payloads, never the Series I's boot
        chain, and boots as spotty3 or mojo3 with its own tree."""
        for variant, name in (("spotty", b"spotty3"), ("mojo", b"mojo3")):
            root, code, log = self.run_install(variant, compatible=b"bose,sm2\x00")
            self.assertEqual(code, 0, log)
            self.assertEqual((root / "NAND.Kernel-Pong").read_bytes()[:self.KERNEL],
                             self.payloads["soundtap-kernel.rhino"], variant)
            self.assertEqual((root / "NAND.SPL").read_bytes()[:self.SPL],
                             self.payloads["soundtap-spl.binky"], variant)
            env = (root / "NAND.U-Boot-Env").read_bytes()
            self.assertIn(b"variant=" + name + b" module_type=sm2", env, log)
            self.assertIn(b"/boot/spotty3_sm2.dtb", env, log)

    def test_module_mismatch_is_refused_before_any_nand_mutation(self):
        root, code, log = self.run_install("rhino", compatible=b"bose,scm\x00")
        self.assertNotEqual(code, 0, log)
        self.assertIn("on a scm module", log)
        self.assertEqual((root / "NAND.U-Boot-Env").read_bytes(), env_image())
        self.assertEqual(set((root / "NAND.SPL").read_bytes()), {0})

    def test_series_i_module_installs(self):
        _root, code, log = self.run_install("spotty", compatible=b"bose,scm\x00")
        self.assertEqual(code, 0, log)
        _root, code, log = self.run_install("rhino", compatible=b"bose,rhino-sm2\x00ti,am33xx\x00")
        self.assertEqual(code, 0, log)

    def test_unknown_variant_fails_closed_before_any_nand_mutation(self):
        root, code, log = self.run_install("unknown")
        self.assertNotEqual(code, 0, log)
        for path in root.iterdir():
            if path.name == "NAND.U-Boot-Env":
                self.assertEqual(path.read_bytes(), env_image(), path.name)
            elif path.name == "NAND.U-Boot":
                self.assertEqual(path.read_bytes(), self.running_uboot.ljust(0x1e0000, b"\x00"), path.name)
            else:
                self.assertEqual(set(path.read_bytes()), {0}, path.name)


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)


class TestEnvRecipesPerModel(unittest.TestCase):
    """The U-Boot environment is the only model-specific thing left in an install: the NAND
    wiring, the GPMC timings and all thirteen partitions are identical on binky, rhino and
    spotty. So the recipes carry the device tree, the kernel image, the variant and the module
    type per model -- and the installer's rendering has to agree with the Python tool's byte
    for byte, or a speaker boots differently depending on which route flashed it."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = scratch_dir()
        cls.binary = build_installer(cls.tmp.name, nand=True, bootloaders=True,
                                     port=free_port())

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def rendered(self, variant, success=True):
        out = subprocess.run([str(self.binary), "--print-env"], capture_output=True,
                             env=dict(os.environ, SOUNDTAP_VARIANT=variant))
        self.assertEqual(out.returncode == 0, success, out.stderr)
        return out.stdout.splitlines()

    def test_every_model_gets_its_own_names(self):
        for variant in (b"binky", b"rhino", b"spotty", b"ginger"):
            self.assertEqual(self.rendered(variant.decode()), list(wla_env.recipes(variant)),
                             f"{variant.decode()} recipes differ from uboot_recipes.py")

    def test_a_board_that_cannot_read_its_variant_fails_closed(self):
        self.assertEqual(self.rendered("", success=False), [])
