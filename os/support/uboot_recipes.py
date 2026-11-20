#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Replace the Wireless Link's two slot boot commands with mainline ones.

The input is a dump of NAND.U-Boot-Env (no OOB): the single 8 KiB copy Bose's U-Boot reads,
or one 32 KiB copy of our redundant environment (uboot_env.py). Only `nand_boot` and
`nand_boot2` change, and the entries the recipes need are added; every other entry keeps
its bytes. The output is the redundant environment: OUT for NAND.U-Boot-Env and
OUT.redundant for NAND.U-Boot + 0x1c0000, both written, the redundant copy first.
--legacy writes the single 8 KiB copy instead, for a board on Bose's U-Boot or on ours
from before the redundant environment.

Why this exists. On this board the environment is the slot selector: `bootcmd` is either
`run nand_boot;` or `run nand_boot2;`, and that one entry is what an install rewrites to
activate the slot it just wrote. Bose's definitions of those two commands cannot run under
our U-Boot -- they use `nboot.e` and `fixsig`, neither of which exists outside Bose's
2014.07 fork. Replacing what the names mean, rather than the names themselves, keeps the
whole A/B contract intact: internal/flash/bootselect.go and os/installer/module both go
on writing `bootcmd` exactly as before and neither needs to know about this.

Each command sets bootargs for its own slot. An earlier version did not, because the
kernel was built CONFIG_CMDLINE_FORCE=y and discarded whatever U-Boot passed -- which
meant the device tree's hard-coded `ubi.mtd=9` won every time and A/B moved the kernel
while the root filesystem stayed on PONG. FORCE is off now; these lines decide the root.
The built-in command line remains as a fallback for a bootloader that passes nothing.

Each slot command decides for itself what it is booting. Ours keeps its kernel in the
slot's root filesystem at /boot/uImage-binky; stock keeps its kernel in the slot's kernel
partition and has no such file. So the presence of that file is the discriminator, and it
has to be the kernel rather than the device tree -- both layouts ship
/boot/binky_sm2.dtb at the same path.

Booting stock needs two things mainline does not do by itself, both added by
os/bootloader/uboot/ (wla-uboot-fixsig.patch, wla-uboot-variant-raw.patch): `fixsig`, which clears the vendor's boot marker from the uImage name
in RAM when doing so reproduces the header CRC already stored, and a live `variant_raw`
sampled from the ADC in board_late_init, which their kernel expects on its command line.
An earlier version of this file claimed their fixsig recomputes the header CRC. It does
not -- the stored CRC is always the one over "Linux", so restoring the name restores the
CRC by construction.

The arguments live in their own entries rather than inline, and unquoted, because the
stock line has to expand ${variant_raw} at boot: U-Boot's hush does not expand inside
single quotes, and `setenv` joins its arguments with single spaces.

    uboot_recipes.py [--legacy] in.bin out.bin
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import uboot_env

# U-Boot builds `mtdids` and `mtdparts` into its *default* environment, from
# CONFIG_MTDIDS_DEFAULT and CONFIG_MTDPARTS_DEFAULT. A stored environment replaces that
# default whole, and Bose's has neither, so `ubi part NAND.File-System-Pong` then has no
# partition table to resolve the name against and fails. Measured on the board: with the
# environment in NAND, U-Boot loaded it, ran the boot command and came back -- the marker
# written after autoboot_command, which a successful bootm never reaches, was present.
# These two entries have to travel with the boot commands that depend on them.
MTDIDS = b"mtdids=nand0=nand0"
MTDPARTS = (
    b"mtdparts=mtdparts=nand0:"
    b"0x20000@0x0(NAND.SPL),0x20000@0x20000(NAND.SPL.backup1),"
    b"0x20000@0x40000(NAND.SPL.backup2),0x20000@0x60000(NAND.SPL.backup3),"
    b"0x1E0000@0x80000(NAND.U-Boot),0x20000@0x260000(NAND.U-Boot-Env),"
    b"0x500000@0x280000(NAND.Kernel-Ping),0x500000@0x780000(NAND.Kernel-Pong),"
    b"0x6000000@0xC80000(NAND.File-System-Ping),0x6000000@0x6C80000(NAND.File-System-Pong),"
    b"0x2600000@0xCC80000(NAND.Persistent-Storage),0xB80000@0xF280000(NAND.Update-Staging),"
    b"0x40000@0xFE00000(NAND.Manufacturing-Data)"
)

# Each slot sets its own root: the device tree names ubi.mtd=9, and the kernel (built
# without CONFIG_CMDLINE_FORCE) takes the root these lines give it.
# What differs between the three speakers is four strings, and nothing else: the NAND
# wiring, the GPMC timings and all thirteen partitions are byte-identical across binky,
# rhino and spotty (checked against os/board/common/dts/ti/omap/am335x-soundtap-st10.dts and
# am335x-soundtap-nand.dtsi). So one set of recipes serves every model once the kernel
# name, the device-tree name and the two variant fields come from a table.
MODELS = {
    b"binky":  {b"dtb": b"binky_sm2.dtb",  b"kernel": b"uImage-binky",  b"module_type": b"sm2"},
    b"rhino":  {b"dtb": b"rhino_sm2.dtb",  b"kernel": b"uImage-rhino",  b"module_type": b"sm2"},
    b"spotty": {b"dtb": b"spotty_scm.dtb", b"kernel": b"uImage-spotty", b"module_type": b"scm"},
    b"ginger": {b"dtb": b"ginger_sm2.dtb", b"kernel": b"uImage-ginger", b"module_type": b"sm2"},
}


_OURS = (b"soundtap_args=setenv bootargs console=ttyGS0,115200n8 root=ubi0:rootfs ro "
         b"rootfstype=ubifs ubi.mtd=${rmtd},2048 rootwait init=/init mem=128M cma=8M "
         b"panic=10 omap_wdt.early_enable=1 variant=%s module_type=%s country=GB "
         b"region=GB soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048")

# Bose's own nand_args with bootargs_defaults folded in, and the values this board boots
# stock with. variant_raw stays
# a reference on purpose: board_late_init samples it fresh every boot.
_STOCK = (b"stock_args=setenv bootargs console=ttyO3,115200n8 quiet root=ubi0:rootfs ro "
          b"ubi.mtd=${rmtd},2048 noinitrd rootfstype=ubifs rootwait=1 ip=off variant=%s "
          b"variant_raw=${variant_raw} variant_mode=normal oled_type= country=GB region=GB")

# One copy of the logic, driven by three variables the slot commands set. Two copies would
# not fit: the environment is 8 KiB and Bose's own entries keep most of it, which they must
# -- restoring their bootloader has to find its environment intact.
#
# Every prerequisite gates the next. That is not tidiness. Semicolons in hush ignore failure,
# so an unchecked chain would let a failed `ubi part` fall through to the layout test, and a
# failed device-tree load would hand bootm whatever happened to be left at 0x83000000 by an
# earlier attempt.
#
# Note what the discriminator does and does not say. Reaching it means the slot's root
# filesystem mounted; only then is a missing /boot/uImage-binky evidence that this slot holds
# the vendor's layout rather than ours. A slot that will not mount is not "stock", it is
# broken, and this fails rather than booting the raw kernel partition against it. The device
# tree cannot be the discriminator: both layouts ship /boot/binky_sm2.dtb at the same path.
# No `else` anywhere, and that is the point: on this U-Boot an `if ubifsload ...` never
# takes its else (measured on the board), and a slot holding vendor firmware, which has no
# /boot/uImage-binky, must still reach the fallback that boots it.
#
# So the recipe does not ask whether a load failed. It poisons the load address, tries
# the load, and tests for a uImage magic that is either there or not. If it is, bootm does
# not return and nothing below runs; if it is not -- or if that bootm fails -- execution
# simply falls through to the vendor path. The fallback is the absence of a branch.
#
# If the vendor path fails as well, the recipe resets. Ending at the U-Boot prompt would leave
# the watchdog serviced and the bootcount untouched, so ADR 0010's bootlimit/altbootcmd could
# never take over; a reset counts the failed boot and lets the fallback slot run.
_SLOT = (b"slot_boot="
         b"if ubi part NAND.File-System-${slot} 2048; then "
         b"if ubifsmount ubi0:rootfs; then "
         b"if ubifsload 0x83000000 /boot/%s; then "
         b"mw.l 0x82000000 0 1; "
         b"ubifsload 0x82000000 /boot/%s; "
         b"if itest.l *0x82000000 == 0x56190527; then "
         b"run soundtap_args; bootm 0x82000000 - 0x83000000; fi; "
         b"run stock_args; "
         b"if nand read 0x80007fc0 ${kaddr} 0x500000; then "
         b"if fixsig 0x80007fc0; then bootm 0x80007fc0 - 0x83000000; fi; "
         b"fi; "
         b"fi; fi; fi; reset")


def recipes(variant=b"binky"):
    """The seven entries this board needs, in the order the installer carries them.

    os/installer/module/installer.c carries TEMPLATES verbatim and fills them the same
    way, per index; os/tests/installer/test_wla_env_recipes.py holds the two to each other.
    """
    dtb, kernel, module_type = (MODELS[variant][k] for k in (b"dtb", b"kernel", b"module_type"))
    fill = {2: (dtb, kernel), 3: (variant, module_type), 4: (variant,)}
    return tuple(t % fill[i] if i in fill else t for i, t in enumerate(TEMPLATES))


_ENTER = b"setenv slot %s; setenv rmtd %s; setenv kaddr %s; run slot_boot"

PING = b"nand_boot=" + _ENTER % (b"Ping", b"8", b"0x280000")
PONG = b"nand_boot2=" + _ENTER % (b"Pong", b"9", b"0x780000")

# These travel with the two boot commands that reference them, the same way mtdids and
# mtdparts do: a stored environment replaces U-Boot's default whole.
TEMPLATES = (PING, PONG, _SLOT, _OURS, _STOCK, MTDIDS, MTDPARTS)

ARGS = recipes()[3], recipes()[4], recipes()[2]


def load(path):
    """The entries of the environment in the dump at path."""
    entries, _flag = uboot_env.parse(Path(path).read_bytes())
    return entries


def rewrite(entries):
    seen = {"nand_boot": False, "nand_boot2": False}
    out = []
    for entry in entries:
        key = entry.split(b"=", 1)[0].decode("ascii", "replace")
        if key == "nand_boot":
            out.append(PING)
            seen["nand_boot"] = True
        elif key == "nand_boot2":
            out.append(PONG)
            seen["nand_boot2"] = True
        else:
            out.append(entry)
    for key, found in seen.items():
        if not found:
            raise SystemExit(f"environment has no {key}; this is not a WLA environment")
    # Replace or append; Bose's environment has neither. Every occurrence goes, not just
    # the first: a hand-edited environment can carry a key twice, and which one U-Boot
    # ends up using is not something a tool that claims to have installed a value should
    # be guessing about.
    for entry in (*ARGS, MTDIDS, MTDPARTS):
        key = entry.split(b"=", 1)[0]
        at = next((i for i, e in enumerate(out) if e.split(b"=", 1)[0] == key), None)
        out = [e for e in out if e.split(b"=", 1)[0] != key]
        if at is None:
            out.append(entry)
        else:
            out.insert(at, entry)
    return out


def main(argv):
    args = argv[1:]
    legacy = bool(args) and args[0] == "--legacy"
    if legacy:
        args = args[1:]
    if len(args) != 2:
        raise SystemExit(__doc__.strip().splitlines()[-1].strip())
    out = rewrite(load(args[0]))
    written = uboot_env.write(args[1], out, legacy)
    bootcmd = next(e for e in out if e.startswith(b"bootcmd="))
    print(f"wrote {', '.join(written)}: {bootcmd.decode()}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
