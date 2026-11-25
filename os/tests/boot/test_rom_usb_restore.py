# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Test tools/recovery/rom-usb-restore.py: its plan, its refusals and the script it puts on the board.

The plan in dry run per model, its refusals, and the script it puts on the board, run against a
fake NAND by a small model of U-Boot's hush.
"""
import hashlib
import importlib.util
import itertools
import json
import os
import re
import struct
import subprocess
import sys
import tempfile
import unittest
import zlib
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import Never

ROOT = Path(__file__).resolve().parents[3]
TOOL = ROOT / "tools/recovery/rom-usb-restore.py"
_spec = importlib.util.spec_from_file_location("rom_usb_restore", TOOL)
R = importlib.util.module_from_spec(_spec)
sys.modules["rom_usb_restore"] = R   # dataclasses look their module up there
_spec.loader.exec_module(R)

BLOCK = 0x20000
MODELS = ("st10", "st20", "wla", "st300")
UIMAGE_MAGIC = 0x27051956
IH_TYPE_SCRIPT = 6
CRC_HEX_DIGITS = 8
ITEST_UNSET_WORDS = 3            # `itest == 1` once an unset ${ok} expands to nothing
ENV_REDUNDANT_START = 0x240000   # NAND.U-Boot block 14, the redundant environment
PONG_BAD_BLOCK = 4               # the padded bad block of the Kernel-Pong backup


def crc(data: bytes) -> int:
    """Return the unsigned crc32 of data."""
    return zlib.crc32(data) & 0xFFFFFFFF


def make_mlo(seed: int, size: int = 0x5000) -> bytes:
    """Build a NAND-form SPL whose body starts with a branch, as mkimage makes one."""
    body = bytes([0x0F, 0x00, 0x00, 0xEA]) + bytes((seed + i) & 0xFF for i in range(size - 4))
    head = bytearray(b"\xff" * 0x200)
    struct.pack_into("<I", head, 0, 0x40)
    head[0x14:0x1E] = b"CHSETTINGS"
    # As mkimage writes it: the size counts the 8-byte GP header too.
    return bytes(head) + struct.pack("<II", len(body) + 8, 0x402F0400) + body


def make_uboot(size: int = 0x81234) -> bytes:
    """Build a legacy U-Boot uImage of size bytes with correct CRCs."""
    data = bytes((i * 7) & 0xFF for i in range(size - 64))
    def header(hcrc: int) -> bytes:
        return struct.pack(">7I4B32s", UIMAGE_MAGIC, hcrc, 0, len(data), 0x80100000, 0x80100000,
                           crc(data), 17, 2, 5, 0, b"U-Boot WLA RAM net")
    return header(crc(header(0))) + data


def write_images(root: Path, wla_mlo: bytes, st20_mlo: bytes, uboot: bytes, *, sums: bool = True) -> None:
    """Write a built images directory, with its SHA256SUMS unless sums is false."""
    files = {"wla-spl/MLO": wla_mlo, "st20-spl/MLO": st20_mlo, "wla-uboot/u-boot.img": uboot}
    for rel, data in files.items():
        (root / rel).parent.mkdir(parents=True, exist_ok=True)
        (root / rel).write_bytes(data)
    if sums:
        (root / "SHA256SUMS").write_text("".join(
            f"{hashlib.sha256(d).hexdigest()}  {rel}\n" for rel, d in files.items()))


def make_stu(modules: list[tuple[str, bytes]]) -> bytes:
    """Build the universal STU's module chain.

    Every module but the last declares 0x14 bytes more than its payload, which the next header
    then occupies (build-stu.py).
    """
    out = bytearray()
    for index, (name, payload) in enumerate(modules):
        header = bytearray(0x138)
        header[0x18:0x18 + len(name)] = name.encode()
        size = len(payload) + (0 if index == len(modules) - 1 else 0x14)
        struct.pack_into(">II", header, 0x98, size, size)
        out += header + payload
    return bytes(out)


def oob_dump(data: bytes) -> bytes:
    """Return data in nand-backup-ssh.sh's form: every 2 KiB page followed by 64 OOB bytes."""
    return b"".join(data[i:i + 0x800] + b"\xff" * 0x40 for i in range(0, len(data), 0x800))


# --- a small model of the U-Boot the script runs on ------------------------------------------

class BoardResetError(Exception):
    """The script ran `reset`: the board stops running it."""


class Board:
    """RAM, a NAND of 2 KiB pages with bad blocks, and the hush subset the script uses."""

    def __init__(self, nand: Mapping[int, bytearray] | None = None, bad: Iterable[int] = (),
                 fail_write: int | None = None) -> None:
        """Start with nand's blocks (absent ones erased), bad blocks, and an optional failing write."""
        self.ram = {}
        self.nand = dict(nand or {})        # block number -> bytearray(BLOCK); absent = erased
        self.bad = set(bad)
        self.fail_write = fail_write        # a flash offset whose program silently fails
        self.env = {}
        self.log = []
        self.echo = []

    PAGE = 0x10000

    def load(self, addr: int, data: bytes) -> None:
        """Put data into RAM at addr."""
        while data:
            base, off = addr - addr % self.PAGE, addr % self.PAGE
            page = self.ram.setdefault(base, bytearray(self.PAGE))
            n = min(len(data), self.PAGE - off)
            page[off:off + n] = data[:n]
            addr, data = addr + n, data[n:]

    def read(self, addr: int, n: int) -> bytes:
        """Return n bytes of RAM from addr; unwritten RAM reads as zero."""
        out = bytearray()
        while n:
            base, off = addr - addr % self.PAGE, addr % self.PAGE
            page = self.ram.get(base, bytes(self.PAGE))
            k = min(n, self.PAGE - off)
            out += page[off:off + k]
            addr, n = addr + k, n - k
        return bytes(out)

    def block(self, n: int) -> bytearray:
        """Return NAND block n; an erased block reads as 0xff."""
        return self.nand.get(n, bytearray(b"\xff" * BLOCK))

    def good_from(self, flash: int) -> int:
        """Return the first good block at or after flash, where a skipping write lands."""
        n = flash // BLOCK
        while n in self.bad:
            n += 1
        return n

    def cmd(self, text: str) -> bool:
        """Expand ${vars} in one command and run it; return whether it succeeded."""
        text = re.sub(r"\$\{(\w+)\}", lambda m: self.env.get(m.group(1), ""), text)
        w = text.split()
        key = " ".join(w[:2]) if w[0] == "nand" else w[0]
        handler = getattr(self, "do_" + key.replace(" ", "_").replace(".", "_"), None)
        if handler is None:
            msg = f"command the model does not know: {text}"
            raise AssertionError(msg)
        return handler(text, w)

    def do_setenv(self, _text: str, w: list[str]) -> bool:
        """Set a variable."""
        self.env[w[1]] = " ".join(w[2:])
        return True

    def do_echo(self, _text: str, w: list[str]) -> bool:
        """Record what the script echoes."""
        self.echo.append(" ".join(w[1:]))
        return True

    def do_reset(self, _text: str, _w: list[str]) -> Never:
        """Stop the script, as a reset does."""
        raise BoardResetError

    def do_itest(self, text: str, w: list[str]) -> bool:
        """Compare two hex numbers for equality."""
        if len(w) == ITEST_UNSET_WORDS:          # an unset variable expands to nothing
            return False
        if w[2] != "==":
            raise AssertionError(text)
        return int(w[1], 16) == int(w[3], 16)

    def do_itest_b(self, text: str, w: list[str]) -> bool:
        """Compare a byte of RAM with a hex number for inequality."""
        if not (w[1].startswith("*") and w[2] == "!="):
            raise AssertionError(text)
        return self.read(int(w[1][1:], 16), 1)[0] != int(w[3], 16)

    def do_crc32(self, text: str, w: list[str]) -> bool:
        """Verify the crc32 of a RAM range."""
        if not (w[1] == "-v" and len(w[4]) == CRC_HEX_DIGITS):
            raise AssertionError(text)
        return crc(self.read(int(w[2], 16), int(w[3], 16))) == int(w[4], 16)

    def do_nand_read_raw(self, _text: str, w: list[str]) -> bool:
        """Read pages with their OOB, the bad-block marker in its first byte, skipping nothing."""
        addr, flash, pages = int(w[2], 16), int(w[3], 16), int(w[4])
        n, data = flash // BLOCK, self.block(flash // BLOCK)
        for p in range(pages):
            off = flash % BLOCK + p * 0x800
            oob = (b"\x00" if n in self.bad else b"\xff") + b"\xff" * 0x3F
            self.load(addr + p * 0x840, bytes(data[off:off + 0x800]) + oob)
        return True

    def do_nand_erase(self, text: str, w: list[str]) -> bool:
        """Erase whole blocks, skipping bad ones."""
        flash, length = int(w[2], 16), int(w[3], 16)
        if not (flash % BLOCK == 0 and length % BLOCK == 0):
            raise AssertionError(text)
        self.log.append(("erase", flash, flash + length))
        for n in range(flash // BLOCK, (flash + length) // BLOCK):
            if n not in self.bad:        # skipped, and counted: an erase never spreads
                self.nand.pop(n, None)
        return True

    def do_nand_write_trimffs(self, text: str, w: list[str]) -> bool:
        """Program one block, moving past bad blocks as U-Boot does."""
        src, flash, length = (int(x, 16) for x in w[2:5])
        if not (length == BLOCK and flash % BLOCK == 0):
            raise AssertionError(text)
        n = self.good_from(flash)        # a write skips bad blocks and spreads
        self.log.append(("write", n * BLOCK))
        if self.fail_write == flash:
            return True
        new, old = self.read(src, BLOCK), self.block(n)
        self.nand[n] = bytearray(a & b for a, b in zip(old, new, strict=False))   # NAND only clears bits
        return True

    def do_nand_read(self, text: str, w: list[str]) -> bool:
        """Read one block into RAM, skipping bad blocks."""
        addr, flash, length = (int(x, 16) for x in w[2:5])
        if length != BLOCK:
            raise AssertionError(text)
        self.load(addr, bytes(self.block(self.good_from(flash))))
        return True

    def run(self, script: str) -> None:
        """Parse and run a hush script until it ends or resets."""
        stmts = [s.strip() for line in script.splitlines() for s in line.split(";") if s.strip()]
        nodes, i = self.parse(stmts, 0, ())
        if i != len(stmts):
            raise AssertionError
        try:
            self.exec(nodes)
        except BoardResetError:
            self.log.append(("reset",))

    def parse(self, stmts: list[str], i: int, stop: tuple[str, ...]) -> tuple[list[tuple], int]:
        """Parse statements from i up to one of stop into if/cmd nodes; return them and the index."""
        nodes = []
        while i < len(stmts):
            s = stmts[i]
            if s.split()[0] in stop:
                return nodes, i
            if s.startswith("if "):
                cond = s[3:]
                if not stmts[i + 1].startswith("then "):
                    raise AssertionError(stmts[i + 1])
                stmts[i + 1] = stmts[i + 1][5:]
                body, i = self.parse(stmts, i + 1, ("else", "fi"))
                other = []
                if stmts[i].startswith("else"):
                    rest = stmts[i][4:].strip()
                    if rest:
                        stmts[i] = rest
                    else:
                        i += 1
                    other, i = self.parse(stmts, i, ("fi",))
                if stmts[i] != "fi":
                    raise AssertionError(stmts[i])
                nodes.append(("if", cond, body, other))
                i += 1
            else:
                nodes.append(("cmd", s))
                i += 1
        return nodes, i

    def exec(self, nodes: list[tuple]) -> None:
        """Run parsed nodes."""
        for node in nodes:
            if node[0] == "cmd":
                self.cmd(node[1])
            else:
                self.exec(node[2] if self.cmd(node[1]) else node[3])

    def boot(self, blob: bytes) -> None:
        """Do what the RAM U-Boot's bootcmd does with the blob: check, then `source` the script."""
        self.load(R.LOAD, blob)
        magic, hcrc, _t, size, _l, _e, dcrc, _os, _arch, kind, _c = struct.unpack(">7I4B", blob[:32])
        header = bytearray(blob[:64])
        header[4:8] = b"\0\0\0\0"
        if not (magic == UIMAGE_MAGIC and kind == IH_TYPE_SCRIPT and crc(bytes(header)) == hcrc):
            raise AssertionError
        data = blob[64:64 + size]
        if crc(data) != dcrc:
            raise AssertionError
        length, end = struct.unpack(">II", data[:8])
        if not (end == 0 and length == size - 8):
            raise AssertionError
        self.run(data[8:].decode("ascii"))


# --- the tests -------------------------------------------------------------------------------

class RomUsbRestoreTest(unittest.TestCase):
    """The tool's plan, refusals and board script, with a docker that only records it ran."""

    def setUp(self) -> None:
        """Write release images and put a recording docker, sudo and make on PATH."""
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tmp = Path(tmp.name)
        self.images = self.tmp / "images"
        self.wla_mlo, self.st20_mlo, self.uboot = make_mlo(1), make_mlo(99, 0x6100), make_uboot()
        write_images(self.images, self.wla_mlo, self.st20_mlo, self.uboot)
        # A docker that records it ran: a dry run must never start one.
        self.bin = self.tmp / "bin"
        self.bin.mkdir()
        for name in ("docker", "sudo", "make"):
            (self.bin / name).write_text(f"#!/bin/sh\necho {name} >> {self.tmp}/ran\nexit 1\n")
            (self.bin / name).chmod(0o755)

    def tool(self, *args: str) -> subprocess.CompletedProcess[str]:
        """Run the tool with args and the recording PATH."""
        env = {**os.environ, "PATH": f"{self.bin}:{os.environ['PATH']}"}
        return subprocess.run([sys.executable, str(TOOL), *args], check=False, capture_output=True, text=True,
                              env=env, timeout=120)

    def plan(self, *args: str) -> dict:
        """Return the tool's --json plan for args, which must succeed."""
        r = self.tool(*args, "--json")
        self.assertEqual(r.returncode, 0, r.stderr)
        return json.loads(r.stdout)

    def spl_for(self, model: str) -> bytes:
        """Return the release SPL the model takes."""
        return self.st20_mlo if model == "st20" else self.wla_mlo

    def test_boot_chain_plan_per_model(self) -> None:
        """Each model's plan writes the spare U-Boot, the SPL copies, then the first U-Boot."""
        u_blocks = [0x180000 + i * BLOCK for i in range(5)]
        for model in MODELS:
            with self.subTest(model):
                info = self.plan("--model", model, "--images", str(self.images))
                self.assertEqual(len(info["sessions"]), 1)
                steps = info["sessions"][0]["steps"]
                erases = [(s["step"], s["start"], s["end"]) for s in steps if s["op"] == "erase"]
                self.assertEqual(erases, [
                    ("1-spare-uboot", 0x180000, 0x240000),
                    ("2-spl.backup1", 0x20000, 0x40000), ("3-spl.backup2", 0x40000, 0x60000),
                    ("4-spl.backup3", 0x60000, 0x80000), ("5-spl", 0x0, 0x20000),
                    ("6-first-uboot", 0x80000, 0x180000)])
                writes = [(s["step"], s["start"], s["end"]) for s in steps if s["op"] == "write"]
                # The U-Boot is 0x81234 bytes: five blocks, in each half.
                self.assertEqual([w[1] for w in writes],
                                 u_blocks + [0x20000, 0x40000, 0x60000, 0x0]
                                 + [0x80000 + i * BLOCK for i in range(5)])
                self.assertTrue(all(end - start == BLOCK for _, start, end in writes))
                # The ordering of the ops themselves: each range erased before its writes,
                # the spare first, the first U-Boot copy last.
                ops = [(s["op"], s.get("step")) for s in steps]
                self.assertEqual(ops[0][0], "check-bad-blocks")
                self.assertEqual(ops[-1][0], "verify")
                self.assertEqual(ops[1], ("erase", "1-spare-uboot"))
                self.assertEqual(ops[-2][1], "6-first-uboot")
                spl = self.spl_for(model).ljust(BLOCK, b"\xff")
                for w in steps:
                    if w["op"] == "write" and "spl" in w["step"]:
                        self.assertEqual(w["crc32"], f"{crc(spl):08x}")
                # Nothing at or past the redundant environment (NAND.U-Boot block 14).
                self.assertTrue(all(s["end"] <= ENV_REDUNDANT_START for s in steps if "end" in s))
                self.assertEqual(info["rom_stage"], "release SPL, raw" if model == "st20"
                                 else "RAM SPL of the session's U-Boot build")

    def test_dry_run_prints_ranges_and_touches_nothing(self) -> None:
        """A dry run prints every range and starts no command."""
        for model in MODELS:
            with self.subTest(model):
                r = self.tool("--model", model, "--images", str(self.images), "--dry-run")
                self.assertEqual(r.returncode, 0, r.stderr)
                self.assertIn("dry run, nothing is built, written or sent over USB", r.stdout)
                self.assertIn("erase   0x180000..0x240000     spare U-Boot (NAND.U-Boot+0x100000)", r.stdout)
                self.assertIn("write   0x0..0x20000           SPL, read back", r.stdout)
                self.assertIn("erase   0x80000..0x180000      first U-Boot (NAND.U-Boot+0x0)", r.stdout)
                self.assertIn("untouched: 0x240000..0x260000 (redundant environment", r.stdout)
                self.assertIn("untouched: 0x260000..0x280000 (NAND.U-Boot-Env)", r.stdout)
                self.assertRegex(r.stdout, r"WLA_UBOOT_NAND_SCRIPT=0x[0-9a-f]+:[0-9a-f]{8} ")
        self.assertFalse((self.tmp / "ran").exists(), "a dry run started a command")
        self.assertEqual(sorted(p.name for p in self.tmp.iterdir()), ["bin", "images"])

    def test_stu_gives_the_same_plan_as_the_images(self) -> None:
        """The release STU gives the same sessions as the images directory."""
        def spl(mlo: bytes) -> bytes:
            return mlo + b"\xff" * 0x14  # build-stu.py's native_spl_payload
        stu = self.tmp / "Update.stu"
        stu.write_bytes(make_stu([
            ("soundtap-installer", b"installer" * 10), ("soundtap-kernel.rhino", b"k" * 100),
            ("soundtap-rootfs", b"r" * 300), ("soundtap-spl.binky", spl(self.wla_mlo)),
            ("soundtap-uboot.binky", self.uboot), ("soundtap-spl.spotty", spl(self.st20_mlo))]))
        for model in MODELS:
            with self.subTest(model):
                a = self.plan("--model", model, "--images", str(self.images))
                b = self.plan("--model", model, "--stu", str(stu))
                self.assertEqual(a["sessions"], b["sessions"])

    def test_refusals(self) -> None:
        """Oversized, missing, unsummed or malformed images are refused before anything runs."""
        big = self.tmp / "big"
        write_images(big, self.wla_mlo, self.st20_mlo, make_uboot(0xC0001))
        exact = self.tmp / "exact"
        write_images(exact, self.wla_mlo, self.st20_mlo, make_uboot(0xC0000))
        self.assertEqual(self.tool("--model", "wla", "--images", str(exact), "--dry-run").returncode, 0)
        r = self.tool("--model", "wla", "--images", str(big), "--dry-run")
        self.assertEqual(r.returncode, 1)
        self.assertIn("over the 0xc0000", r.stderr)

        missing = self.tmp / "missing"
        write_images(missing, self.wla_mlo, self.st20_mlo, self.uboot)
        (missing / "wla-uboot/u-boot.img").unlink()
        r = self.tool("--model", "st10", "--images", str(missing), "--dry-run")
        self.assertEqual(r.returncode, 1)
        self.assertIn("u-boot.img is missing", r.stderr)
        r = self.tool("--model", "st20", "--dry-run")
        self.assertEqual(r.returncode, 1)
        self.assertIn("--images DIR or --stu FILE", r.stderr)

        nosums = self.tmp / "nosums"
        write_images(nosums, self.wla_mlo, self.st20_mlo, self.uboot, sums=False)
        self.assertIn("SHA256SUMS is missing",
                      self.tool("--model", "wla", "--images", str(nosums), "--dry-run").stderr)
        (self.images / "st20-spl/MLO").write_bytes(make_mlo(5))
        r = self.tool("--model", "st20", "--images", str(self.images), "--dry-run")
        self.assertIn("does not match", r.stderr)

        short = self.tmp / "short"
        write_images(short, self.wla_mlo[:-1], self.st20_mlo, self.uboot)
        self.assertIn("declares 20488 bytes but holds 20487",
                      self.tool("--model", "wla", "--images", str(short), "--dry-run").stderr)

        badmlo = self.tmp / "badmlo"
        write_images(badmlo, b"\0" * 0x300, self.st20_mlo, self.uboot)
        self.assertIn("no TI configuration header",
                      self.tool("--model", "wla", "--images", str(badmlo), "--dry-run").stderr)

        r = self.tool("--model", "st30", "--images", str(self.images), "--dry-run")
        self.assertEqual(r.returncode, 2)
        self.assertIn("invalid choice: 'st30'", r.stderr)
        self.assertFalse((self.tmp / "ran").exists())

    def board_for(self, model: str, bad: Iterable[int] = (), fail_write: int | None = None) -> tuple[Board, bytes]:
        """Run the model's boot-chain script on a fake board with an old chain; return it and the SPL."""
        mlo = self.spl_for(model)
        sessions = R.plan(R.Request(mlo=mlo, uboot=self.uboot))
        _script, blob = R.script_and_blob(sessions[0])
        # Whatever was there before: an old chain, and both environment copies.
        nand = {n: bytearray(bytes([0x5A]) * BLOCK) for n in range(0x280000 // BLOCK)}
        board = Board(nand, bad=bad, fail_write=fail_write)
        board.boot(blob)
        return board, mlo

    def test_script_writes_the_chain_in_order_on_a_fake_nand(self) -> None:
        """The board script writes the whole chain in order and leaves both environments alone."""
        for model in MODELS:
            with self.subTest(model):
                board, mlo = self.board_for(model)
                self.assertEqual(board.log[-1], ("reset",))
                self.assertIn("RU-complete", board.echo)
                def flash(off: int, n: int, board: Board = board) -> bytes:
                    return b"".join(bytes(board.block(blk))
                                    for blk in range(off // BLOCK, (off + n) // BLOCK))
                uboot = self.uboot.ljust(0xC0000, b"\xff")
                self.assertEqual(flash(0x180000, 0xC0000), uboot)
                self.assertEqual(flash(0x80000, 0x100000), uboot.ljust(0x100000, b"\xff"))
                for off in (0x0, 0x20000, 0x40000, 0x60000):
                    self.assertEqual(flash(off, BLOCK), mlo.ljust(BLOCK, b"\xff"))
                # Both environment copies are as they were.
                self.assertEqual(flash(0x240000, 0x40000), bytes([0x5A]) * 0x40000)
                order = [e[1] for e in board.log if e[0] == "erase"]
                self.assertEqual(order, [0x180000, 0x20000, 0x40000, 0x60000, 0x0, 0x80000])
                writes = [e[1] for e in board.log if e[0] == "write"]
                self.assertEqual(writes[:5], [0x180000 + i * BLOCK for i in range(5)])
                self.assertEqual(writes[-5:], [0x80000 + i * BLOCK for i in range(5)])

    def test_a_bad_block_under_data_stops_before_the_first_erase(self) -> None:
        """A bad block under data stops the run before the first erase.

        Block 2 of the first U-Boot copy: a write there would spread into block 8, the spare.
        """
        board, _ = self.board_for("st20", bad={(0x80000 + 2 * BLOCK) // BLOCK})
        self.assertEqual([e for e in board.log if e[0] in ("erase", "write")], [])
        self.assertIn("RU-failed-bad-blocks", board.echo)
        self.assertNotIn(("reset",), board.log)

    def test_a_bad_block_under_padding_is_fine(self) -> None:
        """A bad block under padding only is erased (skipped), never written, and the run completes."""
        board, _ = self.board_for("wla", bad={(0x80000 + 7 * BLOCK) // BLOCK})
        self.assertIn("RU-complete", board.echo)

    def test_a_failed_readback_stops_every_later_step(self) -> None:
        """A failed read-back stops every later step and names the step that failed."""
        board, _ = self.board_for("st10", fail_write=0x40000)
        self.assertIn("RU-failed-3-spl.backup2", board.echo)
        erased = [e[1] for e in board.log if e[0] == "erase"]
        self.assertEqual(erased, [0x180000, 0x20000, 0x40000])
        self.assertNotIn(("reset",), board.log)
        # NAND.SPL and the first U-Boot copy were never touched.
        self.assertEqual(bytes(board.block(0)), bytes([0x5A]) * BLOCK)
        self.assertEqual(bytes(board.block(4)), bytes([0x5A]) * BLOCK)

    def test_a_blob_with_another_crc_is_not_what_the_build_accepts(self) -> None:
        """The knob is the blob's length and crc32, so another blob gets another knob."""
        sessions = R.plan(R.Request(mlo=self.wla_mlo, uboot=self.uboot))
        _script, blob = R.script_and_blob(sessions[0])
        self.assertEqual(R.uboot_knob(blob), f"{len(blob):#x}:{crc(blob):08x}")
        self.assertNotEqual(R.uboot_knob(blob[:-1] + b"\x01"), R.uboot_knob(blob))

    def backup(self, parts: Mapping[int, bytes]) -> Path:
        """Write a nand-backup-ssh.sh directory holding parts (mtd index -> data)."""
        directory = self.tmp / "backup"
        directory.mkdir(exist_ok=True)
        sums = []
        for index, data in parts.items():
            label = R.PARTITIONS[index][0]
            name = f"mtd{index}-{label.removeprefix('NAND.')}.oob"
            raw = oob_dump(data)
            (directory / name).write_bytes(raw)
            sums.append(f"{hashlib.sha256(raw).hexdigest()}  ./{name}\n")
        (directory / "SHA256SUMS").write_text("".join(sums))
        return directory

    def test_backup_restore_in_place(self) -> None:
        """A backup goes back in place, its padded bad block erased and not written.

        Kernel-Pong: data in blocks 0-3, a padded bad block (all 0xff) at 4, data at 5.
        """
        pong = bytearray(b"\xff" * 0x500000)
        for n in (0, 1, 2, 3, 5):
            pong[n * BLOCK:(n + 1) * BLOCK] = bytes([n + 1]) * BLOCK
        env = bytes([0xE7]) * 0x8000 + b"\xff" * (BLOCK - 0x8000)
        backup = self.backup({7: bytes(pong), 5: env})
        info = self.plan("--model", "wla", "--backup-only", "--backup", str(backup),
                         "--partition", "mtd7", "--partition", "NAND.U-Boot-Env")
        self.assertNotIn("uboot", info)
        steps = info["sessions"][0]["steps"]
        erases = [(s["start"], s["end"]) for s in steps if s["op"] == "erase"]
        # The redundant copy first, so the restored primary is the environment that counts.
        self.assertEqual(erases, [(0x240000, 0x260000), (0x260000, 0x280000), (0x780000, 0xC80000)])
        writes = [s["start"] for s in steps if s["op"] == "write"]
        self.assertEqual(writes, [0x260000] + [0x780000 + n * BLOCK for n in (0, 1, 2, 3, 5)])

        sessions = R.plan(R.Request(backup=backup, labels=["NAND.Kernel-Pong"], backup_only=True))
        _script, blob = R.script_and_blob(sessions[0])
        # The board went bad at block 4 too; a write there would have moved block 5 into 6.
        board = Board({n: bytearray(b"\x11" * BLOCK) for n in range(0x780000 // BLOCK, 0xC80000 // BLOCK)},
                      bad={(0x780000 + PONG_BAD_BLOCK * BLOCK) // BLOCK})
        board.boot(blob)
        self.assertIn("RU-complete", board.echo)
        for n in range(40):
            got = bytes(board.block(0x780000 // BLOCK + n))
            want = bytes(pong[n * BLOCK:(n + 1) * BLOCK]) if n != PONG_BAD_BLOCK else b"\x11" * BLOCK
            self.assertEqual(got, want, f"block {n}")

    def test_backup_split_into_sessions_of_16_mib(self) -> None:
        """A large backup splits into contiguous sessions of 16 MiB: 304 distinct blocks are 126 + 126 + 52."""
        nv = b"".join(n.to_bytes(2, "big") * (BLOCK // 2) for n in range(0x2600000 // BLOCK))
        backup = self.backup({10: nv})
        sessions = R.plan(R.Request(backup=backup, labels=["NAND.Persistent-Storage"], backup_only=True))
        self.assertEqual([len(s.data_blocks()) for s in sessions], [126, 126, 52])
        covered = []
        for session in sessions:
            _script, blob = R.script_and_blob(session)
            self.assertLessEqual(len(blob), 0x1000000)
            covered.extend((unit.start, unit.end) for unit in session.units)
        # Contiguous, cut at block edges, the whole partition and nothing else.
        self.assertEqual(covered[0][0], 0xCC80000)
        self.assertEqual(covered[-1][1], 0xF280000)
        self.assertTrue(all(a[1] == b[0] for a, b in itertools.pairwise(covered)))

    def test_backup_refusals(self) -> None:
        """A backup's boot chain needs --backup-only, and bad sums or options are refused."""
        backup = self.backup({0: self.wla_mlo.ljust(BLOCK, b"\xff")})
        r = self.tool("--model", "wla", "--images", str(self.images), "--backup", str(backup),
                      "--partition", "mtd0", "--dry-run")
        self.assertEqual(r.returncode, 1)
        self.assertIn("restore a backup's boot chain with --backup-only", r.stderr)
        # With --backup-only the board's own chain may go back, U-Boot first.
        backup = self.backup({0: self.wla_mlo.ljust(BLOCK, b"\xff"),
                              4: self.uboot.ljust(0x1E0000, b"\xff")})
        info = self.plan("--model", "wla", "--backup-only", "--backup", str(backup),
                         "--partition", "mtd0", "--partition", "mtd4")
        erases = [s["start"] for s in info["sessions"][0]["steps"] if s["op"] == "erase"]
        self.assertEqual(erases, [0x80000, 0x0])
        (backup / "mtd0-SPL.oob").write_bytes(b"\0" * len(oob_dump(b"\0" * BLOCK)))
        r = self.tool("--model", "wla", "--backup-only", "--backup", str(backup),
                      "--partition", "mtd0", "--dry-run")
        self.assertIn("does not match", r.stderr)
        r = self.tool("--model", "wla", "--images", str(self.images), "--backup", str(backup),
                      "--dry-run")
        self.assertIn("--backup needs at least one --partition", r.stderr)
        r = self.tool("--model", "wla", "--images", str(self.images), "--partition", "mtd9",
                      "--dry-run")
        self.assertIn("--partition needs --backup", r.stderr)
        r = self.tool("--model", "wla", "--backup-only", "--backup", str(backup),
                      "--partition", "mtd13", "--dry-run")
        self.assertIn("unknown partition mtd13", r.stderr)
        # The ST20's ROM takes the release's SPL, so even a backup-only run needs the images.
        r = self.tool("--model", "st20", "--backup-only", "--backup", str(backup),
                      "--partition", "mtd0", "--dry-run")
        self.assertIn("--images DIR or --stu FILE", r.stderr)

    def test_erase_env(self) -> None:
        """--erase-env erases both environment copies last."""
        info = self.plan("--model", "st10", "--images", str(self.images), "--erase-env")
        erases = [(s["start"], s["end"]) for s in info["sessions"][0]["steps"] if s["op"] == "erase"]
        self.assertEqual(erases[-2:], [(0x240000, 0x260000), (0x260000, 0x280000)])


class BuildKnobTest(unittest.TestCase):
    """build-wla-uboot.sh, run up to its docker call, bakes the bootcmd the checker expects."""

    def build_env(self, knob: str) -> tuple[subprocess.CompletedProcess[str], str | None, str | None]:
        """Run the builder with knob up to its docker run; return it and the bootcmd and config it passed."""
        with tempfile.TemporaryDirectory() as tmp:
            # A copy, as test_wla_uboot_ram.py does: the builder makes its cache beside itself.
            builder = Path(tmp) / "archive/os/bootloader/build-wla-uboot.sh"
            builder.parent.mkdir(parents=True)
            builder.write_text((ROOT / "os/bootloader/build-wla-uboot.sh").read_text())
            fake = Path(tmp) / "docker"
            fake.write_text('#!/bin/sh\ncase "$1" in build) exit 0;; image) printf fake;; '
                            "run) printf '%s' \"$BOOTCOMMAND\" > \"$OUT_CMD\"; "
                            "printf '%s' \"$MINIMAL_INIT_CONFIG\" > \"$OUT_CFG\"; exit 47;; esac\n")
            fake.chmod(0o755)
            env = {**os.environ, "PATH": f"{tmp}:{os.environ['PATH']}",
                   "SOUNDTAP_REPO_REVISION": "a" * 40, "SOURCE_DATE_EPOCH": "1784143890",
                   "WLA_UBOOT_OUT": f"{tmp}/out",
                   "WLA_UBOOT_NAND_SCRIPT": knob, "OUT_CMD": f"{tmp}/cmd", "OUT_CFG": f"{tmp}/cfg"}
            r = subprocess.run(["sh", str(builder)], check=False, env=env,
                               capture_output=True, text=True, timeout=60)
            cmd = Path(tmp, "cmd").read_text() if Path(tmp, "cmd").exists() else None
            cfg = Path(tmp, "cfg").read_text() if Path(tmp, "cfg").exists() else None
            return r, cmd, cfg

    def test_bootcmd_matches_checker_and_tool(self) -> None:
        """The built bootcmd matches the checker's and fetches and checks the tool's blob."""
        sessions = R.plan(R.Request(mlo=make_mlo(1), uboot=make_uboot()))
        _script, blob = R.script_and_blob(sessions[0])
        knob = R.uboot_knob(blob)
        r, cmd, cfg = self.build_env(knob)
        self.assertEqual(r.returncode, 47, r.stderr)
        env = {**os.environ, "WLA_UBOOT_NAND_SCRIPT": knob}
        checker = subprocess.run([sys.executable, "-c", "import runpy,sys; "
                                  "print(runpy.run_path(sys.argv[1])['BOOTCOMMAND'], end='')",
                                  str(ROOT / "os/support/check_wla_uboot_ram.py")],
                                 env=env, capture_output=True, text=True, check=True).stdout
        self.assertEqual(cmd, checker)
        self.assertIn(f"tftpboot 0x81000000 {R.TFTP_NAME}; if crc32 -v 0x81000000 "
                      f"{len(blob):#x} {zlib.crc32(blob) & 0xffffffff:08x}; then source 0x81000000;", cmd)
        for opt in ("CMD_SOURCE", "LEGACY_IMAGE_FORMAT", "CMD_NAND_TRIMFFS", "CMD_ITEST",
                    "CRC32_VERIFY", "NETCONSOLE", "SYS_DEVICE_NULLDEV"):
            self.assertIn(f"--enable {opt}", cfg)

    def test_knob_refuses_a_malformed_value(self) -> None:
        """The builder refuses a malformed knob before docker runs."""
        for knob in ("1234:deadbeef", "0x1000:dead", "0x1000:DEADBEEF", "0x:deadbeef", "0x10"):
            with self.subTest(knob):
                r, cmd, _ = self.build_env(knob)
                self.assertEqual(r.returncode, 2, r.stderr)
                self.assertIn("WLA_UBOOT_NAND_SCRIPT must be", r.stderr)
                self.assertIsNone(cmd)


if __name__ == "__main__":
    unittest.main()
