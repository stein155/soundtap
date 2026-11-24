# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Install a stock-route package into files instead of NAND, as a pre-flight for the board.

It replays stock's half of the :17008 protocol from the package's own bytes and lets the real
installer perform its whole transaction against partition-sized files. What comes out is the
same check the board would make -- every module's CRC, both readbacks, the bootloader ordering
and the rewritten U-Boot environment -- for the exact artifact that is about to be uploaded.

    python3 os/support/wla_stock_route_dryrun.py dist/Update-wla-stock-route.stu [--work DIR]
"""
import argparse
import importlib.util
import os
import struct
import subprocess
import sys
import threading
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "os/installer/module"
GATE = ROOT / "os" / "tests" / "installer" / "test_wla_installer_offline.py"

KERNEL_PARTITION = 0x500000
ROOTFS_PARTITION = 0x6000000
ENV_PARTITION = 0x20000
ENV_SIZE = 0x2000


def load_uboot_env():
    spec = importlib.util.spec_from_file_location("uboot_env", ROOT / "os" / "support" / "uboot_env.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def written_env(flash: Path, bootloaders: bool):
    """The entries the install left and the problems with their format: with our boot chain the
    redundant environment, both copies (ADR 0010); without it Bose's single 8 KiB copy."""
    uboot_env = load_uboot_env()
    primary = (flash / "NAND.U-Boot-Env").read_bytes()
    try:
        entries, flag = uboot_env.parse(primary)
    except SystemExit as e:
        return [], [f"the rewritten environment does not check out: {e}"]
    problems = []
    if bootloaders:
        copy = (flash / "NAND.U-Boot").read_bytes()[uboot_env.REDUNDANT_OFFSET:]
        if flag != uboot_env.FRESH_PRIMARY_FLAG:
            problems.append("the environment is not the redundant one our U-Boot reads")
        else:
            try:
                if uboot_env.parse(copy) != (entries, uboot_env.FRESH_REDUNDANT_FLAG):
                    problems.append("the two environment copies differ")
            except SystemExit:
                problems.append("no redundant environment copy in NAND.U-Boot")
    elif flag is not None:
        problems.append("the environment is not the 8 KiB copy Bose's U-Boot reads")
    return entries, problems


def gate_bose_uboot():
    return load_gate().bose_uboot()


def load_gate():
    spec = importlib.util.spec_from_file_location("wla_gate", GATE)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def stock_stream(package: Path):
    """Everything stock hands the installer: the package after module 1's header and payload."""
    spec = importlib.util.spec_from_file_location("stu_check", ROOT / "os/support/stu_check.py")
    stu_check = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(stu_check)
    data = package.read_bytes()
    first = stu_check.modules(data)[0]
    return data[stu_check.HDR + first[2] - stu_check.OVERLAP:]


# How full a real vendor environment is. Measured on this board's own factory mtd5 on
# 2026-09-17: 78 entries using 6321 of the 8188 usable bytes. None of those entries are
# reproduced here -- no vendor material belongs in a fixture -- but the occupancy is, because
# the question this fixture has to be able to answer is whether the mainline recipes still fit
# once the installer adds them. With an almost empty environment they always fit and the
# interesting failure never happens.
FACTORY_BYTES_USED = 6321


def blank_environment(bootcmd=b"bootcmd=run nand_boot;"):
    entries = b"\x00".join([
        b"baudrate=115200", bootcmd,
        b"nand_args2=run bootargs_defaults;setenv bootargs ${bootargs} ubi.mtd=9,2048",
        b"bootdelay=1"])
    # One filler entry, sized so the whole thing occupies what a factory environment occupies.
    # The installer replaces no key it does not know, so this survives the rewrite untouched and
    # keeps the free space honest.
    filler_overhead = len(b"filler=") + 2
    filler = FACTORY_BYTES_USED - (len(entries) + 1) - filler_overhead
    if filler > 0:
        entries += b"\x00filler=" + b"x" * filler
    entries += b"\x00\x00"
    body = entries + b"\xff" * (ENV_SIZE - 4 - len(entries))
    return (struct.pack("<I", zlib.crc32(body) & 0xFFFFFFFF) + body
            + b"\xff" * (ENV_PARTITION - ENV_SIZE))


def prepare_flash(root: Path, bootloaders: bool):
    (root / "NAND.Kernel-Pong").write_bytes(b"\x00" * KERNEL_PARTITION)
    (root / "NAND.File-System-Pong").write_bytes(b"\x00" * ROOTFS_PARTITION)
    (root / "NAND.U-Boot-Env").write_bytes(blank_environment())
    if bootloaders:
        # Stock's U-Boot in the first copy, as a uImage: the installer checks it ends before
        # the spare it writes first.
        (root / "NAND.U-Boot").write_bytes(gate_bose_uboot().ljust(0x1E0000, b"\x00"))
        for name in ("NAND.SPL", "NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"):
            (root / name).write_bytes(b"\x00" * 0x20000)


def build(work: Path, bootloaders: bool, gate, preview=False):
    """Reuse the gate's compiler handling: the host one where there is one, a pinned container
    on a runner that only has Docker. preview builds the preview package's installer (ADR 0133)."""
    binary = work / ("wla-installer-dryrun" + ("-preview" if preview else ""))
    # Same port discipline as the gate: the installed binary listens on :40015 because
    # that is where stock calls it, but a dry run shares a runner with the offline gates
    # and cannot assume that port is free.
    command = ["cc", "-O2", "-Wall", "-Wextra", "-DSOUNDTAP_INSTALL_NAND",
               f"-DLISTEN_PORT={gate.PORT}"]
    if bootloaders:
        command.append("-DSOUNDTAP_INSTALL_BOOTLOADERS")
    if preview:
        command.append("-DSOUNDTAP_PREVIEW_PACKAGE")
    command += ["-o", str(binary), str(SOURCE / "installer.c"), str(SOURCE / "swupdate.c"),
                str(SOURCE / "mtd.c")]
    gate.compile_with(command, str(work))
    return binary


def run(binary: Path, stream: bytes, flash: Path, gate, variant="binky"):
    coordinator = gate.Coordinator(stream)
    # The installer reads its model from /proc/cmdline, and this host has the wrong one --
    # it refuses unknown hardware before it opens NAND, which is the right thing on a
    # speaker and an immediate "installer failed" here. Same escape hatch the offline gate
    # uses.
    env = dict(os.environ, SOUNDTAP_MTD_DIR=str(flash), SOUNDTAP_CANDIDATE_ROOTFS="9",
               SOUNDTAP_NO_REBOOT="1", SOUNDTAP_VARIANT=variant)
    process = subprocess.Popen([str(binary)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               env=env)
    conn = None
    try:
        # A refused connection on loopback returns at once, so a bare retry count is a spin
        # of a few milliseconds, not a wait; the gate's helper waits in seconds.
        conn = gate.await_listener(process, gate.PORT)
        if conn is None:
            raise SystemExit("installer never listened on :%d" % gate.PORT)
        thread = threading.Thread(target=coordinator.run, args=(conn,))
        thread.start()
        thread.join(1800)
        process.wait(600)
    finally:
        if conn:
            conn.close()
        if process.poll() is None:
            process.kill()
        log = process.stdout.read().decode("utf-8", "replace")
        process.stdout.close()
    return process.returncode, log


def check_spotty(flash: Path, log: str):
    """ST20: its own SPL in all four SPL copies and the shared U-Boot, written in the same order
    as on the other models, and the candidate selected through bootcmd with the ST20's recipes."""
    entries, problems = written_env(flash, True)
    if b"bootcmd=run nand_boot2;" not in entries:
        problems.append("bootcmd does not select the candidate slot")
    if not any(b"spotty_scm.dtb" in e for e in entries):
        problems.append("the environment does not carry the ST20 recipes")
    if (flash / "NAND.Kernel-Pong").read_bytes()[:4] != b"\x27\x05\x19\x56":
        problems.append("the installed kernel does not start with a uImage header")
    if (flash / "NAND.File-System-Pong").read_bytes()[:4] != b"UBI#":
        problems.append("the installed rootfs is not a UBI image")
    spl = (flash / "NAND.SPL").read_bytes()
    if spl[:4] != b"\x40\x00\x00\x00":
        problems.append("NAND.SPL does not hold an MLO")
    for name in ("NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"):
        if (flash / name).read_bytes() != spl:
            problems.append(f"{name} does not hold the same SPL as NAND.SPL")
    if (flash / "NAND.U-Boot").read_bytes()[:4] != b"\x27\x05\x19\x56":
        problems.append("no U-Boot image at NAND.U-Boot")
    if (flash / "NAND.U-Boot").read_bytes()[0x100000:0x100004] != b"\x27\x05\x19\x56":
        problems.append("no spare U-Boot at NAND.U-Boot + 1 MiB")
    written = load_gate().boot_chain_writes(log)
    if written != [("NAND.U-Boot", 0x100000), ("NAND.SPL.backup1", 0), ("NAND.SPL.backup2", 0),
                   ("NAND.SPL.backup3", 0), ("NAND.SPL", 0), ("NAND.U-Boot", 0)]:
        problems.append(f"boot chain order is wrong: {written}")
    return problems


def check_result(flash: Path, log: str, bootloaders: bool):
    kernel = (flash / "NAND.Kernel-Pong").read_bytes()
    rootfs = (flash / "NAND.File-System-Pong").read_bytes()
    entries, problems = written_env(flash, bootloaders)
    if kernel[:4] != b"\x27\x05\x19\x56":
        problems.append("the installed kernel does not start with a uImage header")
    name = kernel[32:56].split(b"\x00")[0].decode("ascii", "replace")
    if not name.startswith("Linub"):
        problems.append(f"stored uImage name is {name!r}; stock U-Boot's fixsig expects Linub")
    if rootfs[:4] != b"UBI#":
        problems.append("the installed rootfs is not a UBI image")
    bootcmd = next((e for e in entries if e.startswith(b"bootcmd=")), b"")
    if bootcmd != b"bootcmd=run nand_boot2;":
        problems.append(f"bootcmd is {bootcmd!r}, not the candidate slot")
    nand_args2 = next((e for e in entries if e.startswith(b"nand_args2=")), b"")
    if b"soundtap.nandroot=1" not in nand_args2.split():
        problems.append("candidate boot arguments do not mount persistent storage")
    if b"ubi.mtd=NAND.Persistent-Storage,2048" not in nand_args2.split():
        problems.append("candidate boot arguments do not attach the persistent UBI volume")
    if "readback verified" not in log:
        problems.append("no partition readback was reported")
    # The recipes and the boot chain are one decision, and it has to hold in both directions.
    import importlib.util
    spec = importlib.util.spec_from_file_location(
        "wla_env", Path(__file__).resolve().parents[2] / "os" / "support" / "uboot_recipes.py")
    wla_env = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(wla_env)
    ours_args, stock_args, slot = wla_env.ARGS
    mainline = (wla_env.PING, wla_env.PONG, slot, ours_args, stock_args,
                wla_env.MTDIDS, wla_env.MTDPARTS)
    written = set(entries)
    if not bootloaders:
        # Without our U-Boot the board keeps booting on the vendor's, which needs the vendor's
        # own recipes. Installing ours here would hand that bootloader commands it cannot run.
        for entry in mainline:
            if entry in written:
                key = entry.split(b"=", 1)[0].decode()
                problems.append(f"{key} was rewritten by a package that carries no bootloader")
    if bootloaders:
        # The boot chain and the recipes that drive it are one decision. A board running our
        # U-Boot against Bose's nand_boot would have a bootcmd that resolves to `nboot.e`, which
        # mainline does not have, so the slot this installer just chose would decide nothing.
        for expected in mainline:
            if expected not in written:
                key = expected.split(b"=", 1)[0].decode()
                problems.append(f"environment does not carry the mainline {key}")
        if b"upgrade_available=1" not in written:
            problems.append("the selection does not keep its boot count in the environment")
        written = load_gate().boot_chain_writes(log)
        if written[:1] != [("NAND.U-Boot", 0x100000)] or written[-1:] != [("NAND.U-Boot", 0)]:
            problems.append(f"boot chain order is wrong: {written}")
        if log.rfind("bootcmd ->") < log.find("boot chain replaced:"):
            problems.append("candidate slot selected before boot chain verification")
        if not 0 <= log.find("bootcmd ->") < log.find("written and verified"):
            problems.append("boot chain written before our boot recipes")
    return problems


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("package", type=Path)
    parser.add_argument("--work", type=Path, help="keep the flash files here")
    parser.add_argument("--bootloaders", action="store_true",
                        help="also exercise the SPL/U-Boot phase")
    parser.add_argument("--variant", default="binky",
                        help="the model the installer believes it is running on")
    parser.add_argument("--preview", action="store_true",
                        help="run the preview package's installer (ADR 0133)")
    args = parser.parse_args()

    gate = load_gate()
    temporary = None
    if args.work:
        args.work.mkdir(parents=True, exist_ok=True)
        work = args.work
    else:
        # The gate picks a directory the container build can also see.
        temporary = gate.scratch_dir()
        work = Path(temporary.name)
    stream = stock_stream(args.package)
    flash = work / "flash"
    flash.mkdir(exist_ok=True)
    prepare_flash(flash, args.bootloaders)
    binary = build(work, args.bootloaders, gate, args.preview)
    code, log = run(binary, stream, flash, gate, args.variant)
    print(log)
    check = (lambda: check_spotty(flash, log)) if args.variant == "spotty" else \
        (lambda: check_result(flash, log, args.bootloaders))
    problems = check() if code == 0 else ["installer failed"]
    if code != 0 or problems:
        for problem in problems:
            print(f"DRY RUN PROBLEM: {problem}", file=sys.stderr)
        raise SystemExit(1)
    print(f"WLA STOCK-ROUTE DRY RUN PASS: {args.package} installs cleanly "
          f"({len(stream):,} streamed bytes)")
    if temporary:
        temporary.cleanup()


if __name__ == "__main__":
    main()
