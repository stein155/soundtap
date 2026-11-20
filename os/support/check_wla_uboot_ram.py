#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Check offline properties needed for WLA ROM USB RAM proof."""

import sys
import os
import struct
import hashlib
import re
import zlib
from pathlib import Path

SPL_TEXT_BASE = 0x402F0400
SPL_SIZE_LIMIT = 0xFC00
UBOOT_LOAD_ADDRESS = 0x80100000
WLA_DRAM_TOP = 0x90000000
DRAM_IMAGE_CAP = WLA_DRAM_TOP - UBOOT_LOAD_ADDRESS
PRE_DDR_SYMBOLS = ("s_init", "board_init_f", "sdram_init")

NAND_INSTALL = os.environ.get("WLA_UBOOT_NAND_INSTALL") == "1"
POST_ENV_RESET = os.environ.get("WLA_UBOOT_POST_ENV_RESET") == "1"
PING_BACKUP_PROBE = os.environ.get("WLA_UBOOT_PING_BACKUP_PROBE") == "1"
NAND_DUMP = os.environ.get("WLA_UBOOT_NAND_DUMP", "")
# The ROM-USB restore of tools/recovery/rom-usb-restore.py: 0xLENGTH:crc32 of the one blob
# this build fetches and sources.
NAND_SCRIPT = os.environ.get("WLA_UBOOT_NAND_SCRIPT", "")
# The netconsole preamble every probe starts with. Spelled once here and once in the
# build script, so a probe that drifts in either place fails this check.
NET = ('setenv ethact usb_ether; setenv ipaddr 192.168.2.2; setenv netmask 255.255.255.0; '
       'setenv serverip 192.168.2.1; setenv ncip 192.168.2.1; setenv stdout nc; '
       'setenv stderr nc; sleep 10; ')


def dump_piece(name, offset, length):
    """One read-checksum-upload step. Reads only; nothing here writes NAND."""
    return (f'nand read 0x82000000 {offset} {length}; crc32 0x82000000 {length}; '
            f'echo DP-{name}-crc; tftpput 0x82000000 {length} {name}; echo DP-{name}-sent;')


if NAND_SCRIPT:
    # Writes only what the script in the one blob says, and only if the blob has exactly the
    # length and crc32 this build was made for. Console up for RU-start, then nulldev across
    # the transfer: see build-wla-uboot.sh for the three shapes tried on the board.
    sc_len, _, sc_crc = NAND_SCRIPT.partition(":")
    BOOTCOMMAND = (NET + 'echo RU-start; setenv stdout nulldev; setenv stderr nulldev; '
                   'tftpboot 0x81000000 soundtap-rom-usb; '
                   f'if crc32 -v 0x81000000 {sc_len} {sc_crc}; then source 0x81000000; '
                   'else setenv stdout nc; setenv stderr nc; echo RU-blob-refused; fi')
elif PING_BACKUP_PROBE:
    # mtd6, then mtd8 in six 16 MiB pieces: the board has 128 MiB of RAM and the
    # partition is 96 MiB, so it cannot be read in one go.
    steps = ('nand read 0x82000000 0x280000 0x500000; crc32 0x82000000 0x500000; '
             'echo PB-mtd6-crc; tftpput 0x82000000 0x500000 mtd6; echo PB-mtd6-sent; ')
    for n in range(6):
        offset = 0xc80000 + n * 0x1000000
        steps += (f'nand read 0x82000000 {offset:#x} 0x1000000; crc32 0x82000000 0x1000000; '
                  f'echo PB-mtd8-{n}-crc; tftpput 0x82000000 0x1000000 mtd8.{n}; '
                  f'echo PB-mtd8-{n}-sent; ')
    BOOTCOMMAND = NET + 'echo PB-start; nand info; nand bad; ' + steps + 'echo PB-end'
elif NAND_DUMP:
    BOOTCOMMAND = NET + 'echo DP-start; nand info; nand bad;'
    for piece in NAND_DUMP.split():
        name, offset, length = piece.split(":")
        BOOTCOMMAND += " " + dump_piece(name, offset, length)
    BOOTCOMMAND += " echo DP-end"
elif os.environ.get("ST10_UBOOT_KERNEL_INSTALL"):
    # One kernel into the ST10's Pong slot, then reset. Padded to the whole partition so
    # the crc32 covers exactly what is written; a mismatch erases nothing.
    KERNEL_INSTALL = os.environ["ST10_UBOOT_KERNEL_INSTALL"]
    BOOTCOMMAND = ('setenv ethact usb_ether; setenv ipaddr 192.168.2.2; setenv netmask 255.255.255.0; setenv serverip 192.168.2.1; setenv ncip 192.168.2.1; setenv stdout nc; setenv stderr nc; sleep 10; echo KI-start; setenv stdout nulldev; setenv stderr nulldev; tftpboot 0x82000000 st10-kernel-pong; mark 7; '
                   f'if crc32 -v 0x82000000 0x500000 {KERNEL_INSTALL}; then mark 8; '
                   'if nand erase 0x780000 0x500000; then mark 9; '
                   'if nand write 0x82000000 0x780000 0x500000; then mark 10; reset; fi; fi; fi')
elif os.environ.get("WLA_UBOOT_FIT_NC_PROBE") == "1":
    # The first boot of this chain on a model nobody has run it on. Every echo is a
    # step, so silence localises: no FIT-start means U-Boot never reached its command,
    # no FIT-fetched means the network did not come up, and FIT-bootm-returned means the
    # payload was rejected rather than never delivered.
    BOOTCOMMAND = ('setenv ethact usb_ether; setenv ipaddr 192.168.2.2; setenv netmask 255.255.255.0; setenv serverip 192.168.2.1; setenv ncip 192.168.2.1; setenv stdout nc; setenv stderr nc; sleep 5; echo FIT-start; setenv stdout nulldev; setenv stderr nulldev; tftpboot ${loadaddr} soundtap-ram-nand.itb; bootm ${loadaddr}')
# One chain, not two. This used to start a second, independent `if`, so its final
# `else` overwrote whatever the chain above had decided and fourteen probes -- every
# backup, the erase and write probes, the install, DHCP, memtest -- were checked
# against CONFIG_BOOTCOMMAND="true" and could only ever fail. No knob means anything
# different as one chain: the two chains had no condition in common.
elif os.environ.get("WLA_UBOOT_NAND_BOOT") == "1":
    import importlib.util
    _spec = importlib.util.spec_from_file_location("nand_check", Path(__file__).with_name("check_wla_uboot_nand.py"))
    _nand = importlib.util.module_from_spec(_spec)
    _spec.loader.exec_module(_nand)
    BOOTCOMMAND = _nand.BOOTCOMMAND
else:
    BOOTCOMMAND = "true"
ENV_IN_NAND = os.environ.get("WLA_UBOOT_ENV_IN_NAND") == "1"
KERNEL_INSTALL_ENV = os.environ.get("ST10_UBOOT_KERNEL_INSTALL", "")
# The ST10 kernel install turns the watchdog off in the same breath as it asks for the
# markers: a 5 MiB crc32, erase and write do not fit inside the 60 s warm reset, and a
# reset landing in the middle of that write leaves half a kernel in the slot that boots.
NO_WATCHDOG = os.environ.get("WLA_UBOOT_NO_WATCHDOG") == "1" or bool(KERNEL_INSTALL_ENV)


def require(config: str, value: str) -> None:
    if value not in config.splitlines():
        raise SystemExit(f"missing {value}")


def reject(config: str, value: str) -> None:
    if value in config.splitlines():
        raise SystemExit(f"unexpected {value}")


def require_usb0_peripheral(payload: bytes) -> None:
    """Require USB0 peripheral mode in final DTB appended to U-Boot proper."""
    marker = b"\xd0\x0d\xfe\xed"
    start = payload.rfind(marker)
    if start < 0 or start + 40 > len(payload):
        raise SystemExit("u-boot-dtb.bin lacks appended DTB")
    _, total, off_struct, off_strings, _, _, _, _, size_strings, size_struct = struct.unpack(
        ">10I", payload[start:start + 40]
    )
    if start + total > len(payload):
        raise SystemExit("appended DTB is truncated")
    dtb = payload[start:start + total]
    strings = dtb[off_strings:off_strings + size_strings]
    structure = dtb[off_struct:off_struct + size_struct]
    stack = []
    offset = 0
    while offset < len(structure):
        token, = struct.unpack_from(">I", structure, offset)
        offset += 4
        if token == 1:
            end = structure.index(b"\0", offset)
            stack.append(structure[offset:end].decode())
            offset = (end + 4) & ~3
        elif token == 2:
            stack.pop()
        elif token == 3:
            length, name_offset = struct.unpack_from(">II", structure, offset)
            offset += 8
            end = strings.index(b"\0", name_offset)
            name = strings[name_offset:end]
            value = structure[offset:offset + length]
            offset = (offset + length + 3) & ~3
            if stack[-1] == "usb@47401000" and name == b"dr_mode" and value == b"peripheral\0":
                return
        elif token == 9:
            break
    raise SystemExit("appended DTB USB0 dr_mode is not peripheral")


def check_legacy_uimage(image: bytes, payload: bytes) -> None:
    if len(image) < 64:
        raise SystemExit("u-boot.img is smaller than legacy uImage header")
    magic, hcrc, _, size, load, entry, dcrc, _, _, _, _, _ = struct.unpack(
        ">7I4B32s", image[:64]
    )
    if magic != 0x27051956:
        raise SystemExit("u-boot.img is not legacy uImage")
    header = bytearray(image[:64])
    struct.pack_into(">I", header, 4, 0)
    if zlib.crc32(header) & 0xFFFFFFFF != hcrc:
        raise SystemExit("legacy uImage header CRC mismatch")
    if size != len(payload) or image[64:] != payload:
        raise SystemExit("legacy uImage does not append exact u-boot-dtb.bin")
    if zlib.crc32(payload) & 0xFFFFFFFF != dcrc:
        raise SystemExit("legacy uImage data CRC mismatch")
    if load != UBOOT_LOAD_ADDRESS or entry != UBOOT_LOAD_ADDRESS:
        raise SystemExit("legacy uImage load/entry does not match legacy SPL address")
    if b"soundtap-nand-pins" not in payload:
        raise SystemExit("u-boot-dtb.bin lacks required WLA NAND device tree")


def check_uboot_link(elf: bytes, link_map: str, image: bytes) -> None:
    if elf[:4] != b"\x7fELF" or elf[4:6] != b"\x01\x01":
        raise SystemExit("u-boot is not little-endian ELF32")
    entry, = struct.unpack_from("<I", elf, 24)
    match = re.search(r"^\s*(0x)?([0-9a-fA-F]+)\s+__image_copy_start\b", link_map, re.MULTILINE)
    if not match:
        raise SystemExit("u-boot.map lacks __image_copy_start")
    link = int(match.group(2), 16)
    if entry != link or link != UBOOT_LOAD_ADDRESS:
        raise SystemExit(
            f"u-boot ELF/map link {link:#x}, entry {entry:#x}, expected {UBOOT_LOAD_ADDRESS:#x}"
        )
    _, _, _, _, load, entry_header, _, _, _, _, _, _ = struct.unpack(
        ">7I4B32s", image[:64]
    )
    if load != link or entry_header != entry:
        raise SystemExit("legacy uImage load/entry does not match U-Boot ELF/map link")


def main() -> None:
    args = sys.argv[1:]
    if len(args) > 1:
        raise SystemExit("usage: check_wla_uboot_ram.py [OUT]")
    out = Path(args[0]) if args else Path("os/bootloader/build/out-uboot-wla")
    config = (out / ".config").read_text()
    for value in (
        'CONFIG_DEFAULT_DEVICE_TREE="am335x-soundtap-wla"', 'CONFIG_SPL=y',
        'CONFIG_SPL_NET=y', 'CONFIG_SPL_USB_GADGET=y', 'CONFIG_SPL_USB_ETHER=y',
        'CONFIG_SPL_LEGACY_IMAGE_FORMAT=y',
        'CONFIG_TEXT_BASE=0x80100000',
        'CONFIG_SPL_TEXT_BASE=0x402f0400', 'CONFIG_SPL_SIZE_LIMIT=0xfc00',
        'CONFIG_SPL_SYS_MALLOC_F_LEN=0x2000', '# CONFIG_SPL_SYS_MALLOC is not set',
        'CONFIG_USB_ETHER=y', 'CONFIG_USB_ETH_RNDIS=y', 'CONFIG_CMD_DHCP=y',
        'CONFIG_SYS_PROMPT="WLA> "',
        # The install build must never autoboot: it exists to be typed at.
        'CONFIG_BOOTDELAY=-1' if NAND_INSTALL else 'CONFIG_BOOTDELAY=0',
        # Every build but the product one must be unsteerable: nothing in mtd5 may change
        # what it does. The product build is the opposite -- mtd5 is this board's slot
        # selector -- and it must have exactly one backing store, or U-Boot has more than
        # one place an environment could come from.
        'CONFIG_ENV_IS_IN_NAND=y' if ENV_IN_NAND else 'CONFIG_ENV_IS_NOWHERE=y',
        # Kconfig stores a double quote inside a string value escaped, so compare against
        # the escaped form. Only the install bootcmd contains any.
        'CONFIG_BOOTCOMMAND="' + BOOTCOMMAND.replace("\\", "\\\\").replace('"', '\\"') + '"',
    ):
        require(config, value)
    if NAND_INSTALL:
        # Exactly the tools an install needs, and still no way to write the
        # environment: a saved env would outrank the built-in bootcmd for good.
        for value in ('CONFIG_CMD_MEMORY=y', 'CONFIG_CMD_CRC32=y',
                      'CONFIG_CMD_TFTPBOOT=y', 'CONFIG_CMD_TFTPPUT=y',
                      'CONFIG_CMD_NAND=y'):
            require(config, value)
        for value in ('CONFIG_CMD_SAVEENV=y', 'CONFIG_ENV_IS_IN_NAND=y'):
            reject(config, value)
        if BOOTCOMMAND != "true":
            raise SystemExit("install build must not carry a bootcmd")
    # No lab build may take its orders from flash: a saved environment would outrank the
    # compiled-in bootcmd, and an install or restore U-Boot has to do exactly what it was
    # built to do. The product build is the one exception -- on this board the environment
    # in mtd5 *is* the slot selector, so reading it is the entire point -- and it says so
    # explicitly rather than by omission.
    #
    # MTD_RAW_NAND=n removes ENV_IS_IN_NAND from Kconfig output entirely.
    if ENV_IN_NAND:
        require(config, 'CONFIG_ENV_IS_IN_NAND=y')
        require(config, 'CONFIG_ENV_OFFSET=0x260000')
        require(config, 'CONFIG_ENV_SIZE=0x8000')
        require(config, 'CONFIG_ENV_REDUNDANT=y')
        require(config, 'CONFIG_ENV_OFFSET_REDUND=0x240000')
        require(config, 'CONFIG_CMD_SAVEENV=y')
    else:
        require(config, '# CONFIG_ENV_IS_IN_NAND is not set')
    # NAND/UBI belong to every variant that can reach a bootcmd.
    for value in (
        'CONFIG_CMD_NAND=y', 'CONFIG_CMD_MTDPARTS=y', 'CONFIG_MTD_PARTITIONS=y',
        'CONFIG_CMD_UBI=y', 'CONFIG_CMD_UBIFS=y', 'CONFIG_MTD_UBI=y',
    ):
        require(config, value)
    if KERNEL_INSTALL_ENV:
        # The second build in this tree that writes flash, held the same way as the first:
        # one partition, only behind a crc32 on the image it fetched, every step gated on
        # the one before it, and no other partition offset allowed to appear at all.
        require(config, "CONFIG_HUSH_PARSER=y")
        require(config, "CONFIG_CRC32_VERIFY=y")
        require(config, "CONFIG_CMD_CRC32=y")
        require(config, "CONFIG_CMD_TFTPBOOT=y")
        require(config, "CONFIG_CMD_WLAMARK=y")
        require(config, "CONFIG_ENV_IS_NOWHERE=y")
        reject(config, "CONFIG_CMD_SAVEENV=y")
        # Bare hex, the form U-Boot prints and the WLA install already requires. The first
        # two ST10 attempts passed 0x315128be and stopped at marker 7 -- fetched, checksum
        # refused -- with the slot untouched.
        if not re.fullmatch(r"[0-9a-f]{1,8}", KERNEL_INSTALL_ENV):
            raise SystemExit("ST10_UBOOT_KERNEL_INSTALL must be U-Boot's own crc32 form")
        writes = re.findall(r"nand (?:erase|write) (?:0x[0-9a-f]+ )?(0x[0-9a-f]+) (0x[0-9a-f]+)",
                            BOOTCOMMAND)
        if sorted(set(writes)) != [("0x780000", "0x500000")]:
            raise SystemExit(f"install bootcmd writes ranges outside Kernel-Pong: {writes}")
        if BOOTCOMMAND.count("nand erase") != 1 or BOOTCOMMAND.count("nand write") != 1:
            raise SystemExit("install bootcmd must erase and write exactly one partition")
        if BOOTCOMMAND.index("crc32 -v") > BOOTCOMMAND.index("nand erase"):
            raise SystemExit("install bootcmd erases before checking the image it fetched")
        if BOOTCOMMAND.count("; then ") != 3 or "; then mark 10; reset; fi" not in BOOTCOMMAND:
            raise SystemExit("install bootcmd does not gate each step on the previous one")
        for word in ("saveenv", "scrub", "markbad", "ubi ", "ubifs", "0x0 ", "0x80000 ",
                     "0x280000", "0xc80000", "0x6c80000", "0x260000"):
            if word in BOOTCOMMAND:
                raise SystemExit(f"install bootcmd reaches beyond Kernel-Pong: {word}")
    if NAND_SCRIPT:
        # The one build that writes NAND. Everything it writes comes from the script in the
        # blob, so the gate is the blob: exact length and crc32 before `source`, the hush
        # parser so that gate is a gate, and no saved environment that could replace it.
        for value in ("CONFIG_HUSH_PARSER=y", "CONFIG_CRC32_VERIFY=y", "CONFIG_CMD_CRC32=y",
                      "CONFIG_CMD_TFTPBOOT=y", "CONFIG_CMD_SOURCE=y", "CONFIG_LEGACY_IMAGE_FORMAT=y",
                      "CONFIG_CMD_NAND_TRIMFFS=y", "CONFIG_CMD_ITEST=y", "CONFIG_ENV_IS_NOWHERE=y"):
            require(config, value)
        reject(config, "CONFIG_CMD_SAVEENV=y")
        if not re.fullmatch(r"0x[0-9a-f]+", sc_len) or not re.fullmatch(r"[0-9a-f]{8}", sc_crc):
            raise SystemExit("WLA_UBOOT_NAND_SCRIPT must be 0xLENGTH:crc32 with eight hex digits")
        if int(sc_len, 16) > 0x1000000:
            raise SystemExit("WLA_UBOOT_NAND_SCRIPT blob exceeds 16 MiB")
        if BOOTCOMMAND.index("crc32 -v") > BOOTCOMMAND.index("source"):
            raise SystemExit("restore bootcmd sources the blob before checking it")
    # The watchdog is a separate knob: it is the board's only negative indicator, so a run
    # may drop NAND/MMC while keeping it, or drop only the watchdog as the control for
    # whether the 50.4 s reset really came from it. The product NAND boot never autostarts
    # it: the SPL's timer guards U-Boot until the kernel takes over.
    no_watchdog = NO_WATCHDOG or os.environ.get("WLA_UBOOT_NAND_BOOT") == "1"
    require(config, '# CONFIG_WATCHDOG_AUTOSTART is not set' if no_watchdog
            else 'CONFIG_WATCHDOG_AUTOSTART=y')
    for value in (
        'CONFIG_SPL_ETH=y', 'CONFIG_SPL_MTD=y', 'CONFIG_SPL_NAND_SUPPORT=y',
        'CONFIG_SPL_NAND_DRIVERS=y', 'CONFIG_SPL_NAND_ECC=y', 'CONFIG_SPL_NAND_BASE=y',
        'CONFIG_SPL_OS_BOOT=y', 'CONFIG_SPL_NAND_AM33XX_BCH=y', 'CONFIG_SPL_I2C=y',
        'CONFIG_SPL_DM_I2C=y', 'CONFIG_SPL_SERIAL=y', 'CONFIG_SPL_FALCON_BOOT_MMCSD=y',
        'CONFIG_SPL_MMC=y', 'CONFIG_SPL_FAT_SUPPORT=y', 'CONFIG_SPL_DFU=y',
        'CONFIG_SPL_USB_HOST=y', 'CONFIG_SPL_USB_STORAGE=y',
    ):
        reject(config, value)
    # Only the product writes its environment: the USB recovery selects the slot it wrote,
    # and the boot count of an unconfirmed slot lives there (ADR 0010).
    if not ENV_IN_NAND:
        reject(config, 'CONFIG_CMD_SAVEENV=y')
    for value in ('CONFIG_USB_GADGET_DOWNLOAD=y', 'CONFIG_USB_FUNCTION_ACM=y'):
        require(config, value)
    mlo = (out / "MLO").read_bytes()
    if len(mlo) < 4 or mlo[:4] != bytes.fromhex("0f0000ea"):
        raise SystemExit("MLO must start with raw ARM SPL vectors, not TI GP header")
    loader = (Path(__file__).parents[2] / "tools" / "recovery" / "rom-usb-loader" / "rom-usb-loader.c").read_text()
    approved = re.search(r'approved_usb_spl_sha256\[\] =\s*"([0-9a-f]{64})"', loader)
    # The pin covers the default path, where the loader supplies the hash itself and
    # nothing else vouches for the MLO it serves. A netconsole build is a diagnostic
    # one: its config differs from the production SPL by construction, so its MLO
    # differs too, and it is only ever served with an explicit --mlo-sha256 measured
    # from that same build. Pinning it here would mean the constant tracking whichever
    # probe ran last, which is not a pin at all.
    # The restore build belongs here too: its bootcmd is a one-off, its config differs by
    # construction, and it is served with an explicit --mlo-sha256 measured from the same build.
    diagnostic = "CONFIG_NETCONSOLE=y" in config or bool(NAND_SCRIPT)
    if not diagnostic and not os.environ.get("WLA_UBOOT_LEGACY_SPL") and (
        not approved or approved.group(1) != hashlib.sha256(mlo).hexdigest()
    ):
        raise SystemExit("loader USB SPL hash does not match MLO")
    image = (out / "u-boot.img").read_bytes()
    payload = (out / "u-boot-dtb.bin").read_bytes()
    check_legacy_uimage(image, payload)
    check_uboot_link((out / "u-boot").read_bytes(), (out / "u-boot.map").read_text(), image)
    require_usb0_peripheral(payload)
    if len(image) > DRAM_IMAGE_CAP:
        raise SystemExit(
            f"u-boot.img {len(image)} exceeds WLA DRAM window {DRAM_IMAGE_CAP}"
        )
    for string in (b"CDC Ethernet", b"RNDIS"):
        if string not in image:
            raise SystemExit(f"u-boot.img missing {string.decode()} USB Ethernet string")
    if POST_ENV_RESET:
        patch = (Path(__file__).parents[2] / "os" / "bootloader" / "uboot" /
                 "wla-uboot-post-env-reset.patch").read_text()
        if "+\tINITCALL(initr_env);\n+\treset_cpu();" not in patch:
            raise SystemExit("post-env reset is not immediately after initr_env")
        disasm = (out / "u-boot-disasm.txt").read_text()
        board_init_r = re.search(r"<board_init_r>:(.*?)(?:\n\n|\Z)", disasm, re.S)
        if not board_init_r or "<reset_cpu>" not in board_init_r.group(1):
            raise SystemExit("post-env reset call absent from board_init_r")
    if len(mlo) > SPL_SIZE_LIMIT:
        raise SystemExit(f"SPL {len(mlo)} exceeds SRAM limit {SPL_SIZE_LIMIT}")
    symbols = (out / "spl-symbols.txt").read_text()
    if not re.search(r"^[0-9a-f]+ T spl_parse_legacy_header$", symbols, re.MULTILINE):
        raise SystemExit("SPL legacy uImage parser is absent")
    for name in PRE_DDR_SYMBOLS:
        match = re.search(rf"^([0-9a-f]+) [A-Za-z] {name}$", symbols, re.MULTILINE)
        if not match or int(match.group(1), 16) >= SPL_TEXT_BASE + SPL_SIZE_LIMIT:
            raise SystemExit(f"SPL pre-DDR symbol outside ROM window: {name}")
    spl_disasm = (out / "spl-disasm.txt").read_text()
    if "<spl_relocate_stack_gd>" not in spl_disasm:
        raise SystemExit("SPL lost spl_relocate_stack_gd")
    reloc = re.search(r"<spl_relocate_stack_gd>:(.*?)(?:\n\n|\Z)", spl_disasm, re.S).group(1)
    if "0x100000" not in reloc:
        raise SystemExit("SPL does not set the 1 MiB simple-malloc pool; dm_init() will fail with -ENOMEM")
    if "memcpy" in reloc:
        raise SystemExit("SPL moves gd; the SRAM stack must be kept")
    uboot_disasm = (out / "u-boot-disasm.txt").read_text()
    if "<board_nand_pinmux>" not in uboot_disasm:
        raise SystemExit("U-Boot lacks the board_nand_pinmux hook; NAND will probe as -ENODEV")
    if " spl_net_load_image_usb" not in symbols:
        raise SystemExit("SPL lacks USB-net stage2 loader")
    if any(name in symbols for name in ("spl_mmc_load_image", "spl_nand_load_image")):
        raise SystemExit("SPL retains MMC or NAND stage2 loader")
    size = (out / "spl-size.txt").read_text()
    if ".text" not in size or ".bss" not in size:
        raise SystemExit("missing SPL size report")
    print(f"WLA U-Boot RAM properties passed: raw-vector USB-Net SPL {len(mlo)}/{SPL_SIZE_LIMIT} bytes at {SPL_TEXT_BASE:#x}; legacy uImage {len(image)}/{DRAM_IMAGE_CAP} bytes load/entry {UBOOT_LOAD_ADDRESS:#x}; {BOOTCOMMAND!r} env-free bootcmd and RNDIS USB Ethernet")


if __name__ == "__main__":
    main()
