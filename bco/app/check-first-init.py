#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Prove our own BCO first stage brings up the clock and SDRAM as the board needs.

Our first stage (bco/app/src/first.c) runs from SRAM 0x40da00 and, before copying
stage 2, programs the clock block (0xe0007000) and the SDRAM controller
(0xe001d000), modelled here with the board's delay-line measurement. This runs the built `first.bin` under Unicorn and asserts the MMIO
write sequence equals the pinned trace in first-init-trace.json (hardware facts;
the code is ours, ADR 0031).

    python3 bco/app/check-first-init.py bco/app/out/first.bin

Needs `unicorn` (pip).
"""
import contextlib
import json
import sys
from collections.abc import Callable
from pathlib import Path

from unicorn import (
    UC_ARCH_ARM,
    UC_HOOK_CODE,
    UC_HOOK_INTR,
    UC_HOOK_MEM_UNMAPPED,
    UC_MODE_ARM,
    Uc,
    UcError,
)
from unicorn.arm_const import UC_ARM_REG_LR, UC_ARM_REG_PC, UC_ARM_REG_R0, UC_ARM_REG_SP

SRAM = 0x00400000
LOAD = 0x0040DA00  # where BFL runs the first stage
SDRAM = 0x60000000
# Bands whose writes form the bring-up trace: clock block, SDRAM controller, 0x70000000.
BANDS = ((0xE0007000, 0xE0008000), (0xE001D000, 0xE001E000), (0x70000000, 0x70001000))
# The board's delay-line measurement (0xe0007844), read on the ST20: attempt 0 asks for a retry
# (bit 29), attempt 1 (0xe00070f0 bits 8..9) is valid.
MEASURE = {0: 0xA00D0F07}
MEASURE_DEFAULT = 0x800D0703
MEASURE_REG = 0xE0007844
# SDRAM command register: its command bits clear once the command is done.
SDRAM_CMD_REG = 0xE001D004
# Angel semihosting SYS_EXIT in r0 ends the run.
SEMIHOST_SYS_EXIT = 0x18


def in_band(addr: int) -> bool:
    """Return whether `addr` lies in one of the bring-up trace bands."""
    return any(lo <= addr < hi for lo, hi in BANDS)


def run(code: bytes, load: int, start: int, regs: dict[int, int] | None = None) -> list[tuple[int, int]]:
    """Execute first-stage `code` loaded at `load`; return the ordered bring-up writes."""
    uc = Uc(UC_ARCH_ARM, UC_MODE_ARM)
    for base, size in ((SRAM, 0x100000), (0x40000000, 0x800000), (SDRAM, 0x1000000),
                       (0x30000000, 0x1000000)):
        uc.mem_map(base, size)
    uc.mem_map(0xB0000000, 0x10000000)
    uc.mem_map(0xE0000000, 0x7000)
    uc.mem_map(0xE0008000, 0x1D000 - 0x8000)
    uc.mem_map(0xE001E000, 0x20000000 - 0x1E000)
    uc.mem_write(load, code)

    writes = []
    regs = dict(regs or {})

    def mmio(base: int) -> tuple[Callable[[Uc, int, int, object], int], Callable[[Uc, int, int, int, object], None]]:
        def rd(_uc: Uc, offset: int, _size: int, _ud: object) -> int:
            addr = base + offset
            if addr == MEASURE_REG:
                return MEASURE.get(regs.get(0xE00070F0, 0) >> 8 & 3, MEASURE_DEFAULT)
            if addr == SDRAM_CMD_REG:
                return regs.get(addr, 0) & ~0x180000
            return regs.get(addr, 0)

        def wr(_uc: Uc, offset: int, _size: int, value: int, _ud: object) -> None:
            addr = base + offset
            regs[addr] = value
            if in_band(addr):
                writes.append((addr, value))
        return rd, wr

    for base in (0xE0007000, 0xE001D000, 0x70000000):
        rd, wr = mmio(base)
        uc.mmio_map(base, 0x1000, rd, None, wr, None)
    uc.hook_add(UC_HOOK_MEM_UNMAPPED, _map_on_demand)
    uc.hook_add(UC_HOOK_INTR, _semihost)
    # Stop once we leave for SDRAM (stage-2 jump) or reach the stop() spin.
    uc.hook_add(UC_HOOK_CODE, _stop_in_sdram, begin=SDRAM, end=SDRAM + 0x1000000)

    uc.reg_write(UC_ARM_REG_SP, 0x30000000)
    with contextlib.suppress(UcError):
        uc.emu_start(start, load + 0x4000, timeout=60_000_000, count=50_000_000)
    return writes


def _map_on_demand(uc: Uc, _access: int, addr: int, *_rest: object) -> bool:
    """Map the 1 MiB around an unmapped access so the run goes on (size, value, user data unused)."""
    with contextlib.suppress(UcError):
        uc.mem_map(addr & ~0xFFFFF, 0x100000)
    return True


def _semihost(uc: Uc, _intno: int, _ud: object) -> None:
    """Handle an angel semihosting call: SYS_EXIT stops the run, anything else returns."""
    if uc.reg_read(UC_ARM_REG_R0) == SEMIHOST_SYS_EXIT:
        uc.emu_stop()
    else:
        uc.reg_write(UC_ARM_REG_PC, uc.reg_read(UC_ARM_REG_LR))


def _stop_in_sdram(uc: Uc, _addr: int, _size: int, _ud: object) -> None:
    """Stop the run once execution reaches SDRAM."""
    uc.emu_stop()


def expected() -> list[tuple[int, int]]:
    """Return the pinned bring-up writes from first-init-trace.json."""
    doc = json.loads((Path(__file__).with_name("first-init-trace.json")).read_text())
    return [(int(a, 16), int(v, 16)) for a, v in doc["writes"]]


def main() -> None:
    """Check the first stage at argv[1] against the pinned trace, with and without a running PLL."""
    want = expected()
    code = Path(sys.argv[1]).read_bytes()[0x18:]  # BFL loads image +0x18 at LOAD
    got = run(code, LOAD, LOAD)
    label = "own first.bin"

    # BFL left the CPU on a PLL set differently: the PLL must be left (source 0) before it is
    # reprogrammed, two extra writes after the dividers.
    left = {0xE0007004: 1, 0xE0007000: 0x00023200}
    got2 = run(code, LOAD, LOAD, regs=left)
    want2 = [*want[:2], (0xE0007004, 0), (0xE0007018, 1), *want[2:]]
    if got2 != want2:
        sys.exit(f"{label}: does not leave a running PLL before reprogramming it")

    if got != want:
        print(f"{label}: bring-up trace MISMATCH")
        print(f"  expected ({len(want)}):")
        for a, v in want:
            print(f"    [{a:08x}] <- {v:#010x}")
        print(f"  got ({len(got)}):")
        for a, v in got:
            print(f"    [{a:08x}] <- {v:#010x}")
        sys.exit(1)
    print(f"{label}: bring-up trace OK ({len(got)} writes; clock + SDRAM match the pin)")


if __name__ == "__main__":
    main()
