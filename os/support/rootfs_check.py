#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Validate the final ST10 rootfs boot, security and hardware contract."""

import os
import re
import struct
from pathlib import Path
from typing import Never

EXECUTABLES = [
    "usr/share/udhcpc/default.script", "usr/share/udhcpc/default.script.d/soundtap-log",
    "bin/busybox", "etc/init.d/soundtap", "etc/init.d/gadget-console", "opt/soundtap/soundtapd",
    "usr/bin/ffmpeg",
    "opt/soundtap/clock.sh", "opt/soundtap/lan-allow.sh",
    "opt/soundtap/bt.sh", "opt/soundtap/bt-ctl.sh",
    "opt/soundtap/bt-source.sh", "opt/soundtap/bt-nowplaying.sh", "usr/bin/btmgmt",
    "usr/sbin/hostapd",
    "usr/bin/bluealsa", "usr/bin/bluealsa-aplay", "usr/bin/bluealsa-cli",
    "opt/soundtap/airplay-source.sh", "usr/bin/shairport-sync", "usr/bin/nqptp",
    "usr/sbin/ubiattach", "usr/sbin/iw", "usr/sbin/wpa_supplicant", "usr/sbin/wpa_cli",
    "usr/bin/snapserver", "usr/bin/snapclient",
]
BUSYBOX_APPLETS = [
    "sbin/udhcpc", "bin/base64", "usr/bin/od", "usr/bin/tr", "bin/date",
    "usr/bin/tee", "usr/bin/wc", "usr/bin/mkfifo", "usr/bin/seq", "sbin/getty", "sbin/inotifyd",
    "usr/sbin/dnsd", "usr/sbin/udhcpd",
]
# An ELF header up to e_machine, and e_machine for ARM.
ELF_PREFIX_LEN = 20
EM_ARM = 40
# Name, password, uid and gid: the fields of /etc/passwd this reads.
PASSWD_FIELDS = 4


def fail(message: str) -> Never:
    """Reject the image with message."""
    msg = f"ROOTFS REJECT: {message}"
    raise SystemExit(msg)


def executable(path: Path) -> bool:
    """Return whether path is a regular file with an execute bit."""
    return path.is_file() and bool(path.stat().st_mode & 0o111)


def inside(root: str | Path, path: str | Path) -> Path:
    """Follow symlinks the way the speaker would, with `root` as /."""
    root, path = Path(root), Path(path)
    for _ in range(40):
        if not path.is_symlink():
            return path
        target = str(path.readlink())
        path = root / target.lstrip("/") if target.startswith("/") else Path(os.path.normpath(path.parent / target))
    return fail(f"{path} is a symlink loop")


def arm_elf(path: Path) -> bool:
    """Return whether path is a 32-bit little-endian ARM ELF."""
    data = path.read_bytes()[:ELF_PREFIX_LEN]
    return (len(data) == ELF_PREFIX_LEN and data[:4] == b"\x7fELF" and data[5] == 1
            and struct.unpack_from("<H", data, 18)[0] == EM_ARM)


def init_text(root: Path) -> str:
    """Return the image's init script."""
    return (root / "etc/init.d/soundtap").read_text()


def validate_gadget_console(root: str | Path) -> None:
    """Check the gadget console mounts proc before it reads /proc/cmdline."""
    script = (Path(root) / "etc/init.d/gadget-console").read_text()
    mount = script.find("mount -t proc")
    read = script.find("/proc/cmdline")
    if mount < 0 or read < 0 or mount > read:
        fail("gadget console reads /proc/cmdline before mounting proc")


def validate_writability_probes(root: Path) -> None:
    """Check init probes /mnt/nv with `true >`, not `: >`.

    `:` is a POSIX special built-in, so a failed redirection on it exits a
    non-interactive shell outright -- even inside an `if` condition. The two
    /mnt/nv probes exist precisely to survive a read-only mount, so writing them
    as `: >file` killed init at boot instead of taking the else branch. They must
    use `true`, an ordinary built-in, whose redirection failure only sets $?.
    """
    for line_no, line in enumerate(init_text(root).splitlines(), 1):
        if re.search(r"(^|[\s!])[:] *>", line) and "/mnt/nv" in line:
            fail(f"init line {line_no} probes /mnt/nv with `: >`; use `true >`")


def validate_ssh_policy(root: str | Path, profile: str = "alpha") -> None:
    """Check the image's SSH matches its profile: alpha's blank-password Dropbear, or none at all."""
    root = Path(root)
    shadow_path = root / "etc/shadow"
    init_path = root / "etc/init.d/soundtap"
    if not shadow_path.is_file() or not init_path.is_file():
        fail("SSH policy inputs /etc/shadow and /etc/init.d/soundtap are missing")
    shadow = shadow_path.read_text()
    root_entry = next((line.split(":", 2)[1] for line in shadow.splitlines()
                       if line.startswith("root:")), None)
    if root_entry is None:
        fail("no root account in /etc/shadow")
    if profile == "alpha":
        validate_alpha_ssh(root, root_entry)
    elif profile == "production":
        validate_production_ssh(root, root_entry)
    else:
        fail(f"unknown release profile: {profile}")


def validate_alpha_ssh(root: Path, root_entry: str) -> None:
    """Check the alpha image starts Dropbear and lets root in without a password."""
    if not executable(root / "usr/sbin/dropbear"):
        fail("alpha rootfs has no dropbear")
    if root_entry != "":
        fail("alpha rootfs must leave root's password field blank")
    ssh = root / "opt/soundtap/ssh.sh"
    if not executable(ssh) or "dropbear -B" not in ssh.read_text():
        fail("alpha ssh.sh does not start dropbear with blank-password logins enabled")
    if "/opt/soundtap/ssh.sh" not in init_text(root):
        fail("init does not run the alpha ssh.sh")


def validate_production_ssh(root: Path, root_entry: str) -> None:
    """Check the production image has no SSH, a root password and a default-deny filter loaded first."""
    if (root / "usr/sbin/dropbear").exists():
        fail("production rootfs contains dropbear")
    if root_entry == "":
        fail("production rootfs has a blank root password")
    if "dropbear" in init_text(root):
        fail("production init starts or configures dropbear")
    for leftover in ("opt/soundtap/ssh.sh", "etc/default/dropbear"):
        if (root / leftover).exists():
            fail(f"production rootfs carries /{leftover}")
    validate_no_ssh_at_all(root)
    if re.search(r"(?m)^ttyGS0:.*/bin/sh", (root / "etc/inittab").read_text()):
        fail("production inittab gives the USB gadget a root shell")
    marker = root / "etc/soundtap/profile"
    if not marker.is_file() or marker.read_text().strip() != "production":
        fail("production rootfs does not say so in /etc/soundtap/profile")
    rules = root / "etc/soundtap/firewall.nft"
    if not executable(root / "usr/sbin/nft"):
        fail("production rootfs has no nft for its input filter")
    if not rules.is_file() or "policy drop" not in rules.read_text():
        fail("production rootfs has no default-deny input filter")
    # Without these the setup AP gives no lease and no captive portal.
    if 'iifname "wlan1" udp dport { 53, 67 } accept' not in rules.read_text():
        fail("production input filter blocks the setup AP's DHCP and DNS")
    sysinit = re.findall(r"(?m)^::sysinit:(.*)$", (root / "etc/inittab").read_text())
    if not sysinit or sysinit[0] != "/usr/sbin/nft -f /etc/soundtap/firewall.nft":
        fail("production inittab does not load the input filter first")


SSH_NAMES = {"ssh", "scp", "sftp-server", "sshd", "dbclient", "authorized_keys", "known_hosts"}


def validate_no_ssh_at_all(root: Path) -> None:
    """Check production has no SSH (ADR 0119).

    Not the server alone, but no client, key tool, host key, authorized_keys or script that would make one, and no
    hole for port 22 in the input filter.
    """
    for top, dirs, files in os.walk(root):
        for name in dirs + files:
            rel = (Path(top) / name).relative_to(root)
            # ssh.service and sftp-ssh.service: avahi's default adverts of a server we lack.
            if name in SSH_NAMES or name == ".ssh" or "dropbear" in name or name.endswith("ssh.service"):
                fail(f"production rootfs carries /{rel}")
    scripts = [*(root / "etc/init.d").glob("*"), *(root / "opt/soundtap").glob("*.sh")]
    for script in scripts:
        if script.is_file() and "dropbear" in script.read_text(errors="replace"):
            fail(f"production /{script.relative_to(root)} starts or configures dropbear")
    rules = root / "etc/soundtap/firewall.nft"
    if rules.is_file() and re.search(r"(?m)^[^#\n]*dport[^#\n]*\b22\b", rules.read_text()):
        fail("production input filter opens port 22")


def validate_mount_points(root: str | Path) -> None:
    """Check the image carries the /mnt/nv mount point."""
    # / is read-only at runtime: init can no longer mkdir its own mount point, and
    # without it neither the persistent nor the volatile /mnt/nv mount happens (no wifi.conf).
    if not (Path(root) / "mnt/nv").is_dir():
        fail("/mnt/nv mount point missing from the image")


def table(path: Path) -> dict[str, list[str]]:
    """Return name -> fields of a colon-separated /etc/passwd or /etc/group."""
    if not path.is_file():
        return {}
    rows = (line.split(":") for line in path.read_text().splitlines() if line and not line.startswith("#"))
    return {row[0]: row for row in rows}


def validate_soundtapd_user(root: str | Path, accounts: str | Path | None = None) -> None:
    """Check soundtapd runs as its own user, started by soundtap-root (ADR 0066), and nothing else starts it.

    No root soundtapd, and the helper's socket only that user's group reaches.

    accounts is the tree whose /etc/passwd and /etc/group to read, root when None. Buildroot
    adds the users of BR2_ROOTFS_USERS_TABLES only to the filesystem's own copy of the
    target, never to output/target, so image-check passes images/rootfs-tree here.
    """
    root = Path(root)
    accounts = Path(accounts) if accounts else root
    users, groups = table(accounts / "etc/passwd"), table(accounts / "etc/group")
    user = users.get("soundtapd")
    if not user or len(user) < PASSWD_FIELDS or user[2] == "0" or user[3] == "0":
        fail("no soundtapd user apart from root in /etc/passwd")
    group = groups.get("soundtapd")
    if not group or group[2] != user[3]:
        fail("soundtapd's primary group is missing")
    if "soundtapd" not in groups.get("audio", ["", "", "", ""])[3].split(","):
        fail("soundtapd is not in group audio (the source bus)")
    if not arm_elf(inside(root, root / "usr/bin/soundtap-root")):
        fail("soundtap-root is not a 32-bit little-endian ARM ELF")
    policy = root / "etc/dbus-1/system.d/soundtapd.conf"
    if not policy.is_file() or '<policy user="soundtapd">' not in policy.read_text():
        fail("no D-Bus policy for the soundtapd user")
    init = init_text(root)
    starts = [line.strip() for line in init.splitlines()
              if '"$SOUNDTAPD_BIN" -home' in line and not line.lstrip().startswith("#")]
    if not starts or any(not s.startswith("${SOUNDTAPD_EXEC:-} ") for s in starts):
        fail("init starts soundtapd other than through soundtap-root's exec-soundtapd")
    if 'SOUNDTAPD_EXEC="$ROOT_HELPER exec-soundtapd"' not in init:
        fail("init never starts soundtapd as its own user")
    services = init[init.find("start_services() {"):]
    if not 0 <= services.find("start_root_helper") < services.find("soundtapd_loop "):
        fail("soundtap-root is not started before soundtapd")
    if 'chgrp "$SOUNDTAPD_USER" "$ROOT_RUN_DIR" && chmod 0750 "$ROOT_RUN_DIR"' not in init:
        fail("soundtap-root's socket directory is not root:soundtapd 0750")
    main = init[init.rfind("main() {"):]
    if "\tgrant_soundtapd\n" not in main or main.find("grant_soundtapd") > main.find("start_services"):
        fail("init does not hand soundtapd its devices and state before it starts")


def validate(root: str | Path, *, allow_baked_wifi: bool = False, profile: str = "alpha",
             accounts: str | Path | None = None) -> None:
    """Check the whole image contract and print ROOTFS OK, or reject the image."""
    root = Path(root)
    validate_soundtapd_user(root, accounts)
    validate_writability_probes(root)
    validate_mount_points(root)
    validate_no_env_fallback(root)
    validate_binaries(root)
    validate_ssh_policy(root, profile)
    validate_gadget_console(root)
    validate_image_state(root, allow_baked_wifi=allow_baked_wifi)
    init = init_text(root)
    validate_init(init)
    validate_airplay_and_bus(root, init)
    validate_snapcast(root, init)
    validate_snapserver_storage(init)
    validate_leftovers(root)
    print("ROOTFS OK: boot and security contract valid")


def validate_binaries(root: Path) -> None:
    """Check every program the speaker runs is there, executable, and ARM where it must be."""
    for name in EXECUTABLES:
        if not executable(inside(root, root / name)):
            fail(f"/{name} is missing or not executable")
    for name in BUSYBOX_APPLETS:
        if inside(root, root / name) != root / "bin/busybox":
            fail(f"/{name} is not a BusyBox applet")
    if not any(executable(inside(root, root / name)) for name in ("sbin/wpa_passphrase", "usr/bin/wpa_passphrase", "usr/sbin/wpa_passphrase")):
        fail("wpa_passphrase is missing or not executable")
    if not arm_elf(inside(root, root / "opt/soundtap/soundtapd")):
        fail("soundtapd is not a 32-bit little-endian ARM ELF")
    if not arm_elf(inside(root, root / "usr/bin/ffmpeg")):
        fail("FFmpeg is not a 32-bit little-endian ARM ELF")


def validate_image_state(root: Path, *, allow_baked_wifi: bool) -> None:
    """Check the image carries no Wi-Fi profile (unless allowed), has /mnt/nv and a build epoch."""
    provisioned_wifi = root / "etc/soundtap-wifi.conf"
    if provisioned_wifi.exists() and not allow_baked_wifi:
        fail("production rootfs contains a build-time Wi-Fi profile")
    if not (root / "mnt/nv").is_dir():
        fail("persistent storage mountpoint /mnt/nv is missing")
    build_epoch = root / "etc/soundtap-build-epoch"
    if not build_epoch.is_file() or not build_epoch.read_text().strip().isdigit():
        fail("credible build epoch is missing")


def validate_init(init: str) -> None:
    """Check init mounts persistent NAND safely and keeps / read-only and the USB dump clean."""
    if ("NAND\\.Persistent-Storage" not in init
            or "NAND\\.Persistant-Storage" not in init
            or 'ubiattach -m "$PERSIST_MTD" -O 2048 -d 1 /dev/ubi_ctrl' not in init
            or any("ubiformat" in line and not line.lstrip().startswith("#")
                   for line in init.splitlines())):
        fail("persistent NAND mount is missing or destructive")
    if "busybox --install" in init:
        fail("init writes BusyBox applets to the root filesystem")
    # The boot watchdog may only reboot a speaker whose daemon never came up: soundtapd
    # touches the disarm file once it binds.
    if "soundtap.watchdog" in init and "/run/soundtap/booted" not in init:
        fail("mainline boot watchdog has no disarm path")
    # The USB debug dump lands on a removable FAT volume that also backs the live
    # root: it must be scrubbed of network identity and must not leave the stick rw.
    if "/media/usb/soundtap-mainline-root.txt" in init:
        if "redact" not in init:
            fail("USB debug dump is not redacted")
        if "wpa_cli" in init or "iw dev wlan0 link" in init:
            fail("USB debug dump leaks Wi-Fi network identity")
        if "remount,ro /media/usb" not in init:
            fail("USB debug dump leaves /media/usb writable")
    # / stays read-only (ADR 0066): boot-time state goes to /run, not the image.
    if "remount,rw /\n" in init or "remount,rw / " in init:
        fail("init remounts / read-write")


def validate_airplay_and_bus(root: Path, init: str) -> None:
    """Check the AirPlay configuration, that soundtapd owns nqptp, and the ALSA source bus."""
    shairport = (root / "etc/shairport-sync.conf").read_text()
    for contract in ('service_type = "airplay2"', "output_rate = 48000",
                     'output_format = "S16_LE"', "output_channels = 2",
                     "period_size = 1024", "buffer_size = 8192",
                     'wait_for_completion = "yes"', 'disable_synchronization = "yes"'):
        if contract not in shairport:
            fail(f"AirPlay configuration contract missing: {contract}")
    if "supervise nqptp" in init:
        fail("init supervises nqptp; soundtapd owns it (ADR 0121)")
    asound = (root / "etc/asound.conf.bus").read_text()
    for contract in ("type file", 'file "/run/soundtap/source.pcm"', 'format "raw"',
                      "format S16_LE", "rate 48000", "channels 2"):
        if contract not in asound:
            fail(f"ALSA source bus contract missing: {contract}")


def validate_snapcast(root: Path, init: str) -> None:
    """Check the Snapcast server and client configuration and how init hands it to soundtapd."""
    daemon_start = "soundtapd_loop ${SOUNDTAPD_ARGS:-} &"
    snapserver = (root / "etc/snapserver.conf").read_text()
    for contract in ("datadir = /run/soundtap/snapserver", "mdns_enabled = false", "[http]\nenabled = false",
                     "[tcp-control]\nenabled = true\nbind_to_address = 127.0.0.1",
                     "bind_to_address = 0.0.0.0",
                     "port = 1705", "port = 1704",
                     "pipe:///run/soundtap/snapcast.pcm?name=SoundTap&sampleformat=46875:16:2&codec=flac:1&chunk_ms=20",
                     "buffer = 1000"):
        if contract not in snapserver:
            fail(f"Snapserver configuration contract missing: {contract}")
    # The group stream is FLAC (ADR 0013): a snapserver without libFLAC refuses the stream,
    # and a snapclient without it exits at the codec header, so a follower stays silent.
    if not any((root / "usr/lib").glob("libFLAC.so.*")):
        fail("libFLAC missing: the FLAC group stream cannot be encoded or decoded")
    for name in ("usr/bin/snapserver", "usr/bin/snapclient"):
        if b"libFLAC.so" not in inside(root, root / name).read_bytes():
            fail(f"{name} is not linked with libFLAC (BUILD_WITH_FLAC)")
    if (root / "usr/share/snapserver/snapweb").exists() or "port = 1780" in snapserver:
        fail("Snapweb must not be shipped")
    for contract in ('AUDIO_DEV="alsa:$SNAPCLIENT_SOUNDCARD"', "AUDIO_TAP=/run/soundtap/snapcast.pcm",
                     "unset AUDIO_PHYSICAL_RATE",
                     'SOUNDTAP_MULTIROOM="${SOUNDTAP_MULTIROOM:-0}"',
                     "[ -f /mnt/nv/soundtap/multiroom-enabled ] && SOUNDTAP_MULTIROOM=1",
                     "SNAPCLIENT_SOUNDCARD=shelby", "SNAPCLIENT_SOUNDCARD=plughw:0,0",
                      "SNAPCLIENT_SAMPLEFORMAT='48000:16:*'", "SNAPCLIENT_SAMPLEFORMAT='46875:16:*'",
                     ):
        if contract not in init:
            fail(f"Snapcast init contract missing: {contract}")
    # soundtapd supervises snapserver from this configuration, so it must be set first.
    if init.index("SOUNDTAP_SNAPSERVER_CONF=/run/soundtap/snapserver.conf") > init.index(daemon_start):
        fail("Snapserver configuration is not handed to soundtapd before it starts")
    if re.search(r"(?m)^supervise\(\)", init):
        fail("init has its own supervise(); soundtapd supervises the media daemons (ADR 0121)")


def validate_snapserver_storage(init: str) -> None:
    """Check init falls back to a volatile /mnt/nv and disables snapserver when storage is read-only."""
    for contract in ('/.writable.$$" 2>/dev/null',
                      "read -r build_time </etc/soundtap-build-epoch",
                      "persistent storage unavailable or read-only; using volatile /mnt/nv",
                     "SOUNDTAP_SNAPSERVER_CONF=/run/soundtap/snapserver.conf",
                     "snapserver disabled: settings directory is not writable"):
        if contract not in init:
            fail(f"Snapserver read-only-storage contract missing: {contract}")


def validate_leftovers(root: Path) -> None:
    """Check the recovery console is ttyS3, the only modules are the ST300's and no wifi.sh is back."""
    inittab = (root / "etc/inittab").read_text()
    if "ttyS3::respawn:" not in inittab or "ttyS0::respawn:" in inittab:
        fail("recovery console is not ttyS3")
    st300_cpsw = {"ti_cpsw_new", "cpsw-common", "davinci_mdio", "phy-gmii-sel"}
    for module in (root / "lib/modules").rglob("*.ko*"):
        if module.name.split(".ko")[0] not in st300_cpsw:
            fail(f"the image carries a kernel module other than the ST300 CPSW ones: {module.name}")
    # Wi-Fi is soundtapd's (app/internal/wifi, tested there); a wifi.sh left in the image
    # would be a second owner of wlan0.
    for gone in ("opt/soundtap/wifi.sh", "opt/soundtap/wifi-event.sh"):
        if os.path.lexists(root / gone):
            fail(f"{gone} is back: soundtapd owns Wi-Fi")


REPO = Path(__file__).resolve().parents[2]


def validate_redistributable(config: str, root: Path = REPO) -> None:
    """Check a production image builds no package that may not be redistributed."""
    # REDISTRIBUTE = NO only keeps a package's source out of legal-info; Buildroot still
    # builds the package and installs its binaries. A release image may carry none.
    enabled = set(re.findall(r"(?m)^(BR2_\w+)=y$", config))
    for mk in sorted([*root.glob("os/package/*/*.mk"), *root.glob("buildroot/package/*/*.mk")]):
        for name in re.findall(r"(?m)^(\w+)_REDISTRIBUTE\s*=\s*NO\b", mk.read_text()):
            if f"BR2_PACKAGE_{name}" in enabled:
                fail(f"production image builds {mk.parent.name}, which may not be redistributed")


def validate_no_env_fallback(root: str | Path) -> None:
    """Check the image carries no default environment fw_setenv could fall back to."""
    # With this file fw_setenv replaces a damaged U-Boot environment with its contents (ADR 0010).
    if os.path.lexists(Path(root) / "etc/u-boot-initial-env"):
        fail("/etc/u-boot-initial-env would let fw_setenv replace a damaged environment")


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("--allow-baked-wifi", action="store_true")
    parser.add_argument("--profile", choices=("alpha", "production"), default="alpha")
    parser.add_argument("--accounts", help="tree with the image's /etc/passwd and /etc/group (default: rootfs)")
    parser.add_argument("rootfs")
    opts = parser.parse_args()
    if opts.profile == "production":
        # Buildroot's .config sits next to target/ in its output directory.
        config = Path(opts.rootfs).resolve().parent / ".config"
        if not config.is_file():
            fail(f"no Buildroot .config next to the rootfs: {config}")
        validate_redistributable(config.read_text())
    validate(opts.rootfs, allow_baked_wifi=opts.allow_baked_wifi, profile=opts.profile, accounts=opts.accounts)
