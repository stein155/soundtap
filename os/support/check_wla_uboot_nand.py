#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Check WLA U-Boot proper's fixed UBI boot chain."""
import sys
from pathlib import Path


# Only a blank or corrupt mtd5 runs this; a good environment's bootcmd comes from the
# slot recipes (app/internal/flash/recipes.go). The ST10 and the WLA share this U-Boot,
# so the model comes from the variant resistor board_late_init publishes: WLA reads
# 2518-2618, ST10 3630. itest compares as hex, which keeps four-digit decimals in order.
# Each slot is tried with our kernel only (magic 0x56190527), Pong first, then reset so
# the watchdog never sits at a prompt.
def _slot(ubi: str, mtd: int) -> str:
    return (f"if ubi part NAND.File-System-{ubi} 2048; then if ubifsmount ubi0:rootfs; then "
            "if ubifsload 0x83000000 /boot/${rt_v}_${rt_m}.dtb; then mw.l 0x82000000 0 1; "
            "ubifsload 0x82000000 /boot/uImage-${rt_v}; if itest.l *0x82000000 == 0x56190527; then "
            f"setenv bootargs console=ttyGS0,115200n8 root=ubi0:rootfs ro rootfstype=ubifs ubi.mtd={mtd},2048 "
            "rootwait init=/init mem=128M cma=8M panic=10 omap_wdt.early_enable=1 variant=${rt_v} "
            "module_type=${rt_m} country=GB region=GB soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048; "
            "bootm 0x82000000 - 0x83000000; fi; fi; fi; fi; ")


BOOTCOMMAND = (""
               + _slot("Pong", 9) + _slot("Ping", 8) + "reset")
REQUIRED = (
    "CONFIG_CMD_NAND=y", "CONFIG_CMD_MTDPARTS=y", "CONFIG_CMD_UBI=y",
    "CONFIG_CMD_UBIFS=y", "CONFIG_MTD_UBI=y", "CONFIG_CMD_BOOTM=y",
    # Our own fixsig, and the board hook that samples variant_raw. Without them this
    # U-Boot can boot our firmware but not the vendor's, and the speaker has no way back
    # to stock.
    "CONFIG_CMD_FIXSIG=y", "CONFIG_BOARD_LATE_INIT=y",
    # The slot boot commands in mtd5 choose between our layout and the vendor's with
    # `if ... then ... else ... fi`. Without a shell that understands that, the whole
    # command is a syntax error and the slot boots nothing.
    "CONFIG_HUSH_PARSER=y",
    # The SPL's timer guards U-Boot; an autostarted U-Boot watchdog is fed by every
    # polling loop, so a hang in U-Boot would never reset.
    "# CONFIG_WATCHDOG_AUTOSTART is not set",
    f'CONFIG_BOOTCOMMAND="{BOOTCOMMAND}"',
    "0x500000@0x780000(NAND.Kernel-Pong)",
    "0x6000000@0x6C80000(NAND.File-System-Pong)",
)

# A probe or install U-Boot must not be steerable: whatever is in mtd5 has to be unable
# to change what it does, so it carries no environment backing store at all.
PROBE_ONLY = ("CONFIG_ENV_IS_NOWHERE=y", "# CONFIG_ENV_IS_IN_NAND is not set")

# A product U-Boot is the opposite. On this board the environment *is* the slot selector,
# so reading it is the whole point, and the layout has to be the one every other tool here
# writes (ADR 0010): two 32 KiB copies, the primary at the start of mtd5 and the other in
# mtd4's last block, upgrading the single 8 KiB copy Bose's U-Boot reads.
PRODUCT_ONLY = (
    "CONFIG_ENV_IS_IN_NAND=y", "CONFIG_ENV_OFFSET=0x260000", "CONFIG_ENV_SIZE=0x8000",
    "CONFIG_ENV_REDUNDANT=y", "CONFIG_ENV_REDUNDANT_UPGRADE=y", "CONFIG_ENV_OFFSET_REDUND=0x240000",
    "CONFIG_ENV_RANGE=0x20000",
    # Without saveenv env_nand_save is not compiled in, and env_save() cannot write.
    "CONFIG_CMD_SAVEENV=y",
    # The slot is selected by `bootcmd=run nand_boot;` or `run nand_boot2;`. Without
    # CMD_RUN that is an unknown command and the board executes nothing at all -- which
    # is exactly what it did, silently, for most of 2026-09-16.
    "CONFIG_CMD_RUN=y",
    # ENV_IS_NOWHERE is fatal here, not merely untidy: its init sets env_valid to
    # ENV_INVALID, and env_relocate() then takes its "environment not changable" branch
    # and never calls env_load(). mtd5 is never read. FAT goes with it because
    # olddefconfig keeps turning it back on.
    "# CONFIG_ENV_IS_IN_FAT is not set",
    "# CONFIG_ENV_IS_NOWHERE is not set",
)


def main(out: Path, env_in_nand: bool = False) -> None:
    config = (out / ".config").read_text()
    image = (out / "u-boot.img").read_bytes()
    for value in REQUIRED + (PRODUCT_ONLY if env_in_nand else PROBE_ONLY):
        if value not in config.splitlines() and value not in config:
            raise SystemExit(f"missing {value}")
    chain = BOOTCOMMAND.split("; ")
    if config.find(f'CONFIG_BOOTCOMMAND="{BOOTCOMMAND}"') < 0:
        raise SystemExit("config lacks exact NAND boot command")
    if not all(image.find(step.encode()) >= 0 for step in chain):
        raise SystemExit("u-boot.img lacks complete NAND boot command chain")
    if BOOTCOMMAND.encode() not in image:
        raise SystemExit("u-boot.img lacks fixed UBI bootcmd")
    # Bose's own boot path must not be compiled in whatever the mode. A product U-Boot
    # will *read* nand_boot out of mtd5 at runtime -- that is how the slot is chosen --
    # but it must not carry its own copy, or a blank environment would boot Bose's way.
    # `fixsig` is no longer on this list: the name is theirs, the implementation is ours
    # (cmd/fixsig.c), and a U-Boot without it cannot boot a slot their updater wrote.
    for forbidden in (b"nboot.e", b"bootcmd=run "):
        if forbidden in image:
            raise SystemExit(f"u-boot.img retains Bose boot path: {forbidden.decode()}")
    for needed in (b"fixsig", b"variant_raw"):
        if needed not in image:
            raise SystemExit(f"u-boot.img cannot boot stock firmware: {needed.decode()} missing")
    mode = "product, environment from mtd5" if env_in_nand else "probe, no environment"
    print(f"WLA U-Boot NAND boot passed ({mode}): "
          "variant_raw picks binky/rhino, Pong then Ping, then reset")


if __name__ == "__main__":
    args = sys.argv[1:]
    env_in_nand = "--env-in-nand" in args
    args = [a for a in args if a != "--env-in-nand"]
    if len(args) != 1:
        raise SystemExit(f"usage: {sys.argv[0]} [--env-in-nand] OUT")
    main(Path(args[0]), env_in_nand)
