#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Build WLA RAM-proof bootloader images. Docker owns all build dependencies.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO=$(CDPATH= cd -- "$HERE/../.." && pwd)
OUT=${WLA_UBOOT_OUT:-$HERE/build/out-uboot-wla}
CACHE_ROOT=$HERE/build/cache-wla-uboot
[ -z "${WLA_UBOOT_CACHE:-}" ] || [ "$WLA_UBOOT_CACHE" = "$CACHE_ROOT" ] || {
  printf '%s\n' "WLA_UBOOT_CACHE must be $CACHE_ROOT" >&2; exit 2
}
CACHE=$CACHE_ROOT
# The build timestamp U-Boot bakes into its version string, and so into its bytes. It is a
# constant, and deliberately not derived from git.
#
# An install compares the packaged U-Boot with mtd4 byte for byte, so a bootloader that
# changes for any reason other than its own sources turns every install into a boot-chain
# write. Two earlier attempts at this both failed, and both failed by being clever:
#
#   HEAD's commit time      every commit anywhere rebuilt U-Boot
#   last commit touching    correct locally, and on 2026-09-18 a CI build still came out
#   os/bootloader       with a different time than the one before it -- the value
#                           either fell back to HEAD or the command returned nothing, and
#                           from the artifact alone the two are indistinguishable
#
# A constant has no such failure mode. The date in the banner is cosmetic; what matters is
# that identical sources give identical bytes. Bump os/bootloader/source-date-epoch by
# hand if a human wants the banner to say something newer -- that is the only thing it
# affects.
#
# It lives in a file rather than here because build-wla-stu.sh calls this script and used to
# export its own value from HEAD first, which won every time: the default below only applies
# when nothing is set, and something always was. One file, read by both.
: "${SOURCE_DATE_EPOCH:=$(cat "$REPO/os/bootloader/source-date-epoch")}"
export SOURCE_DATE_EPOCH
[ ! -L "$CACHE" ] || { printf '%s\n' "WLA U-Boot cache must not be a symlink" >&2; exit 2; }
[ "${WLA_UBOOT_CLEAN:-}" != 1 ] || { mkdir -p "$CACHE" && rm -rf "${CACHE:?}"/* "$CACHE"/.[!.]* "$CACHE"/..?*; }
BOOTCOMMAND=true
BOOTDELAY=0
# The only build meant to change NAND. It never autoboots -- it drops straight to the
# prompt -- and adds exactly what an install needs and nothing else: memory and crc32 to
# verify, TFTP in to fetch MLO/u-boot.img, TFTP out to carry a backup off the board
# before anything is erased. Writing the environment stays impossible: CMD_SAVEENV is off
# and ENV_IS_NOWHERE, so Bose's mtd5 cannot steer this U-Boot either.
# The product build, and the only one that lets NAND steer it. Every other build here
# keeps ENV_IS_NOWHERE on purpose: an install or restore U-Boot must do exactly what its
# compiled-in bootcmd says, and must not be steerable by whatever is in mtd5. A product
# U-Boot is the opposite -- the environment in mtd5 is this board's slot selector, which
# is what makes A/B switching work at all, so it has to read it.
# Diagnostic. This board boots NAND only with the cable out, so a NAND-booting U-Boot has
# no console; markers into mtd11 are the only way to see how far it got, and the only way
# to tell a build that never ran from a boot that happened not to come up.
UBOOT_MARKERS=
# The ST10 kernel install marks each step, and `mark` only exists with the marker
# patches applied, so asking for that install asks for them too.
[ -z "${ST10_UBOOT_KERNEL_INSTALL:-}" ] || UBOOT_MARKERS=1

ENV_IN_NAND=${WLA_UBOOT_ENV_IN_NAND:-}
ENV_CONFIG=
if [ "$ENV_IN_NAND" = 1 ]; then
  # The redundant environment (ADR 0010): two 32 KiB copies, each at the start of its own
  # 128 KiB erase block, the primary in mtd5 (0x260000) and the other in the last block of
  # mtd4 (0x240000), behind the spare U-Boot. ENV_RANGE stays one block for both: U-Boot
  # has one range, and a wider one would make the primary's erase run into Kernel-Ping.
  # ENV_REDUNDANT_UPGRADE reads the 8 KiB single copy Bose's U-Boot and our earlier builds
  # wrote while neither copy checks out (uboot-2026.07-env-redundant.patch gives it that
  # size), and the first save writes the redundant copy, leaving that one standing.
  # CMD_SAVEENV is what compiles env_nand_save in at all: without it env_save() reports
  # "not possible", and the USB recovery could never select the slot it wrote.
  # One line: this is interpolated unquoted as a flag list, so a newline in it becomes
  # the end of the command and the rest becomes a command of its own.
  # Deliberately narrow. The first attempt at this also turned off ENV_IS_IN_FAT and
  # ENV_IS_NOWHERE, which took FAT support with them and moved ENV_SIZE, and the board
  # then did not boot with 21 config differences to choose from. Change where the
  # environment can live and nothing else; let olddefconfig settle the rest.
  # CMD_RUN back on. The lean configuration below strips it, and nothing noticed while
  # the bootcmd was compiled in -- but this board's environment selects its slot with
  # `bootcmd=run nand_boot;` or `run nand_boot2;`, which is what an install rewrites.
  # Without `run` that is an unknown command: it fails instantly, autoboot returns, and
  # the board sits there having executed nothing. Measured -- a literal bootcmd in the
  # same environment booted in 44 seconds, the `run` form never got as far as its first
  # command.
  # ENV_IS_NOWHERE must be off, and this is not tidiness. env_nowhere_init() sets
  # gd->env_valid = ENV_INVALID, and env_relocate() reads
  #
  #     if (gd->env_valid == ENV_INVALID) {
  #     #if defined(CONFIG_ENV_IS_NOWHERE) || defined(CONFIG_XPL_BUILD)
  #             env_set_default(NULL, 0);          /* environment not changable */
  #
  # so with both symbols on, U-Boot takes that branch and never calls env_load() at all.
  # NAND is not consulted, however correct the bytes in mtd5 are. Measured on this board
  # on 2026-09-16: a flat bootcmd carrying a marker, written and read back byte for byte,
  # never reached the kernel command line. FAT goes too for the same reason it was never
  # wanted -- olddefconfig turns it back on whenever FAT support becomes reachable, and
  # mtd5 is this board's slot selector, so nothing else may answer for it.
  # CMD_MEMORY and CMD_ITEST are not conveniences. The slot recipe decides whether a
  # slot holds our kernel or the vendor's by poisoning the load address with mw and
  # then testing for a uImage magic with itest, because branching on whether
  # ubifsload failed does not work here -- measured 2026-09-17, the else arm was
  # never entered and a vendor slot could not fall back to its own kernel.
  ENV_CONFIG='--enable ENV_IS_IN_NAND --disable ENV_IS_IN_FAT --disable ENV_IS_NOWHERE --enable CMD_RUN --enable CMD_MEMORY --enable CMD_ITEST --enable CMD_SAVEENV --enable ENV_REDUNDANT --enable ENV_REDUNDANT_UPGRADE --set-val ENV_OFFSET 0x260000 --set-val ENV_OFFSET_REDUND 0x240000 --set-val ENV_SIZE 0x8000 --set-val ENV_RANGE 0x20000'
fi

NAND_INSTALL=${WLA_UBOOT_NAND_INSTALL:-}
if [ "${WLA_UBOOT_FIT_NC_PROBE:-}" = 1 ]; then
  # The boot delay stays at the default. -1 was copied in from the NAND install block,
  # where it stops a half-installed board sitting at a prompt nobody can see; here it
  # buys nothing and the checker is right to refuse it.
  INSTALL_CONFIG='--enable CMD_MEMORY --enable CMD_CRC32 --enable CMD_TFTPBOOT --enable CMD_SLEEP --disable ENV_IS_IN_FAT --enable ENV_IS_NOWHERE'
fi
if [ "$NAND_INSTALL" = 1 ]; then
  BOOTDELAY=-1
  # olddefconfig turns ENV_IS_IN_FAT back on once FAT support is reachable; keep every
  # env backing store off so the built-in bootcmd is the only one there is.
  INSTALL_CONFIG='--enable CMD_MEMORY --enable CMD_CRC32 --enable CMD_TFTPBOOT --enable CMD_TFTPPUT --enable CMD_DHCP --enable CMD_SLEEP --disable ENV_IS_IN_FAT --enable ENV_IS_NOWHERE'
fi
# The same FIT boot with a console on it. A first boot on a model nobody has run this
# chain on says nothing useful when it is silent: netconsole turns "it did not come up"
# into a line saying which step it reached. It also makes the build a diagnostic one by
# the checker's own definition, which is correct -- its config differs from the
# production SPL by construction and it is served with an explicit --mlo-sha256.
[ "${WLA_UBOOT_FIT_NC_PROBE:-}" = 1 ] && BOOTCOMMAND='setenv ethact usb_ether; setenv ipaddr 192.168.2.2; setenv netmask 255.255.255.0; setenv serverip 192.168.2.1; setenv ncip 192.168.2.1; setenv stdout nc; setenv stderr nc; sleep 5; echo FIT-start; setenv stdout nulldev; setenv stderr nulldev; tftpboot ${loadaddr} soundtap-ram-nand.itb; bootm ${loadaddr}'
# tftpboot, not dhcp, and netconsole silenced across it. Measured on an ST10 on 2026-09-19: the chain reached U-Boot and
# printed FIT-start over netconsole, then dhcp never returned -- no FIT-fetched, and the
# loader never saw a request for the payload. The addresses are already set two commands
# earlier, so there is nothing for DHCP to discover, and asking for it re-initialises the
# very USB link netconsole is riding on. Every WLA probe that works fetches by name.
#
# The console is turned off for the transfer and never turned back on. Restoring it
# tears the gadget down -- the comment above the NAND install bootcmd says so, and the
# bus shows it: with the restore in place U-Boot's own 0451:d022 gadget was still there
# 90 s later and bootm had never run. Nothing after the transfer needs a console; what
# happens next is visible on the bus, as Linux's gadget replacing U-Boot's.
#
# Previously the console was turned off for the transfer and back on after,
# which is what every WLA probe that moves real data does. Without it the payload and
# the console share one RNDIS link and fight: measured on the ST10, the request was
# accepted and the transfer died at block 2 with bulk-write timeouts.
# Product U-Boot for the ST10, the WLA and the ST20. Only a blank or corrupt mtd5 runs this; a
# good environment brings the slot recipes. The board code sets rt_v/rt_m from variant_raw
# (WLA 2518-2618, ST10 3630) and SYSBOOT (ST20); see uboot-2026.07-scm-nand-wp.patch. Both slots
# are tried with our kernel, and it ends in reset rather than a prompt.
[ "${WLA_UBOOT_NAND_BOOT:-}" = 1 ] && BOOTCOMMAND='if ubi part NAND.File-System-Pong 2048; then if ubifsmount ubi0:rootfs; then if ubifsload 0x83000000 /boot/${rt_v}_${rt_m}.dtb; then mw.l 0x82000000 0 1; ubifsload 0x82000000 /boot/uImage-${rt_v}; if itest.l *0x82000000 == 0x56190527; then setenv bootargs console=ttyGS0,115200n8 root=ubi0:rootfs ro rootfstype=ubifs ubi.mtd=9,2048 rootwait init=/init mem=128M cma=8M panic=10 omap_wdt.early_enable=1 variant=${rt_v} module_type=${rt_m} country=GB region=GB soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048; bootm 0x82000000 - 0x83000000; fi; fi; fi; fi; if ubi part NAND.File-System-Ping 2048; then if ubifsmount ubi0:rootfs; then if ubifsload 0x83000000 /boot/${rt_v}_${rt_m}.dtb; then mw.l 0x82000000 0 1; ubifsload 0x82000000 /boot/uImage-${rt_v}; if itest.l *0x82000000 == 0x56190527; then setenv bootargs console=ttyGS0,115200n8 root=ubi0:rootfs ro rootfstype=ubifs ubi.mtd=8,2048 rootwait init=/init mem=128M cma=8M panic=10 omap_wdt.early_enable=1 variant=${rt_v} module_type=${rt_m} country=GB region=GB soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048; bootm 0x82000000 - 0x83000000; fi; fi; fi; fi; reset'
# Read-only. The general form of the Ping backup: WLA_UBOOT_NAND_DUMP is a space
# separated list of name:offset:length, each piece read into RAM, checksummed and
# uploaded with tftpput to a loader started with --upload-dir. Nothing is written. A
# piece has to fit in RAM below the 128 MiB this board has, so keep each under 16 MiB
# and split anything larger the way the Ping probe splits mtd8.
if [ -n "${WLA_UBOOT_NAND_DUMP:-}" ]; then
  BOOTCOMMAND="setenv ethact usb_ether; setenv ipaddr 192.168.2.2; setenv netmask 255.255.255.0; setenv serverip 192.168.2.1; setenv ncip 192.168.2.1; setenv stdout nc; setenv stderr nc; sleep 10; echo DP-start; nand info; nand bad;"
  for piece in $WLA_UBOOT_NAND_DUMP; do
    name=${piece%%:*}
    rest=${piece#*:}
    off=${rest%%:*}
    len=${rest#*:}
    [ "$name" != "$piece" ] && [ "$off" != "$rest" ] && [ -n "$len" ] || {
      printf '%s\n' "WLA_UBOOT_NAND_DUMP piece must be name:offset:length, got $piece" >&2
      exit 2
    }
    BOOTCOMMAND="$BOOTCOMMAND nand read 0x82000000 $off $len; crc32 0x82000000 $len; echo DP-$name-crc; tftpput 0x82000000 $len $name; echo DP-$name-sent;"
  done
  BOOTCOMMAND="$BOOTCOMMAND echo DP-end"
fi
# The ROM-USB restore (tools/recovery/rom-usb-restore.py), and the only bootcmd in this repo
# that writes NAND. WLA_UBOOT_NAND_SCRIPT=length:crc32 binds the build to one blob: it fetches
# soundtap-rom-usb from the loader (started with --watch-file), refuses it unless it has
# exactly that length and crc32, and only then sources the script image at its front. The
# script checks every block it will program for a bad-block marker before the first erase,
# writes one block at a time and reads each back, reads everything back once more, and ends
# in RU-complete and a reset. A failure leaves the board sitting there, saying
# RU-failed-<step>.
#
# Pages that are all 0xff are skipped (write.trimffs) so they stay erased. Writing them
# would program ECC bytes into their OOB, and UBI/UBIFS could then never program those
# pages cleanly. The board has 128 MiB of RAM, so a blob is at most 16 MiB.
#
# The length is hex with 0x; the crc32 is eight lowercase hex digits, because crc32 -v takes
# a sum of exactly the digest's width as hex and anything else as the name of a variable.
#
# The console goes to nulldev across the transfer on purpose. netconsole shares the USB
# gadget with the transfer, and U-Boot prints a progress character per TFTP block, so
# reporting on the transfer is what stalls it: with the console live it dies at block 2,
# and silenced it completes 98 blocks in 14 ms. TFTP also stops USB ethernet
# (usb_eth_stop -> usb_gadget_disconnect), so whether the host hears RU-complete afterwards
# is unproven; a run it does not hear is unconfirmed, never done.
if [ -n "${WLA_UBOOT_NAND_SCRIPT:-}" ]; then
  # BOOTDELAY stays 0: this build *is* its bootcmd.
  sc_len=${WLA_UBOOT_NAND_SCRIPT%%:*}
  sc_crc=${WLA_UBOOT_NAND_SCRIPT#*:}
  sc_ok=1
  case $sc_len in 0x*) ;; *) sc_ok= ;; esac
  case ${sc_len#0x} in '' | *[!0-9a-f]*) sc_ok= ;; esac
  case $sc_crc in [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;; *) sc_ok= ;; esac
  [ -n "$sc_ok" ] || {
    printf '%s\n' "WLA_UBOOT_NAND_SCRIPT must be 0xLENGTH:crc32, the crc32 as eight hex digits" >&2; exit 2; }
  BOOTCOMMAND="setenv ethact usb_ether; setenv ipaddr 192.168.2.2; setenv netmask 255.255.255.0; setenv serverip 192.168.2.1; setenv ncip 192.168.2.1; setenv stdout nc; setenv stderr nc; sleep 10; echo RU-start; setenv stdout nulldev; setenv stderr nulldev; tftpboot 0x81000000 soundtap-rom-usb; if crc32 -v 0x81000000 $sc_len $sc_crc; then source 0x81000000; else setenv stdout nc; setenv stderr nc; echo RU-blob-refused; fi"
fi
# Read-only, and the reason the loader learned to receive files. mtd6 and mtd8 are the
# stock Ping slot -- the only factory copy of Bose's kernel and rootfs on this board, and
# the one thing an install through SoundTap would overwrite. At 5 MiB and 96 MiB they are
# far past what netconsole hex can carry, so this reads each piece into RAM, prints its
# crc32, and uploads it with tftpput to the loader started with --upload-dir. mtd8 goes in
# six 16 MiB pieces at fixed offsets, because the board only has 128 MiB of RAM; a restore
# has to write the pieces back at those same offsets.
[ "${WLA_UBOOT_PING_BACKUP_PROBE:-}" = 1 ] && BOOTCOMMAND='setenv ethact usb_ether; setenv ipaddr 192.168.2.2; setenv netmask 255.255.255.0; setenv serverip 192.168.2.1; setenv ncip 192.168.2.1; setenv stdout nc; setenv stderr nc; sleep 10; echo PB-start; nand info; nand bad; nand read 0x82000000 0x280000 0x500000; crc32 0x82000000 0x500000; echo PB-mtd6-crc; tftpput 0x82000000 0x500000 mtd6; echo PB-mtd6-sent; nand read 0x82000000 0xc80000 0x1000000; crc32 0x82000000 0x1000000; echo PB-mtd8-0-crc; tftpput 0x82000000 0x1000000 mtd8.0; echo PB-mtd8-0-sent; nand read 0x82000000 0x1c80000 0x1000000; crc32 0x82000000 0x1000000; echo PB-mtd8-1-crc; tftpput 0x82000000 0x1000000 mtd8.1; echo PB-mtd8-1-sent; nand read 0x82000000 0x2c80000 0x1000000; crc32 0x82000000 0x1000000; echo PB-mtd8-2-crc; tftpput 0x82000000 0x1000000 mtd8.2; echo PB-mtd8-2-sent; nand read 0x82000000 0x3c80000 0x1000000; crc32 0x82000000 0x1000000; echo PB-mtd8-3-crc; tftpput 0x82000000 0x1000000 mtd8.3; echo PB-mtd8-3-sent; nand read 0x82000000 0x4c80000 0x1000000; crc32 0x82000000 0x1000000; echo PB-mtd8-4-crc; tftpput 0x82000000 0x1000000 mtd8.4; echo PB-mtd8-4-sent; nand read 0x82000000 0x5c80000 0x1000000; crc32 0x82000000 0x1000000; echo PB-mtd8-5-crc; tftpput 0x82000000 0x1000000 mtd8.5; echo PB-mtd8-5-sent; echo PB-end'
# Write one kernel into the ST10's Pong slot and reset. This is the smallest thing that
# breaks the circle: the device tree deciding which partitions are writable is appended
# to the kernel, so a kernel from flash can never open the slots that hold it -- but a
# U-Boot loaded over the mask ROM has no such problem, and NAND.Kernel-Pong is 5 MiB,
# one transfer.
#
# The image must already carry the Linxt name in its uImage header or stock U-Boot
# will select the other slot afterwards. That is a safe outcome on this speaker, where
# Ping still holds stock Bose, but it is not an install.
#
# Padded to the full partition so the crc32 covers exactly what gets written. A mismatch
# means nothing is erased.
# Marked at every step. The console is on nulldev from the transfer onwards -- restoring
# netconsole tears the gadget down mid-command -- so the only thing a failed step can
# leave behind is a marker in the staging block, which the next boot reads back. The
# first attempt came back with Kernel-Pong untouched and no way to say whether the crc32
# rejected the image or the erase never ran.
[ -n "${ST10_UBOOT_KERNEL_INSTALL:-}" ] && BOOTCOMMAND="setenv ethact usb_ether; setenv ipaddr 192.168.2.2; setenv netmask 255.255.255.0; setenv serverip 192.168.2.1; setenv ncip 192.168.2.1; setenv stdout nc; setenv stderr nc; sleep 10; echo KI-start; setenv stdout nulldev; setenv stderr nulldev; tftpboot 0x82000000 st10-kernel-pong; mark 7; if crc32 -v 0x82000000 0x500000 ${ST10_UBOOT_KERNEL_INSTALL}; then mark 8; if nand erase 0x780000 0x500000; then mark 9; if nand write 0x82000000 0x780000 0x500000; then mark 10; reset; fi; fi; fi"
export BOOTCOMMAND
GADGET_CONFIG='--enable USB_GADGET_DOWNLOAD --enable USB_FUNCTION_ACM'
MINIMAL_INIT_CONFIG=
export MINIMAL_INIT_CONFIG
# The product U-Boot carries the USB emergency update (uboot-2026.07-usb-recovery.patch);
# ENV_CONFIG keeps ENV_IS_IN_FAT off once FAT is reachable; it only reads the stick, so no FAT_WRITE.
# No WATCHDOG_AUTOSTART: U-Boot would service WDT1 from every polling loop, so a hang inside
# U-Boot never resets (as on 2026-09-27). The SPL's 120 s timer runs through U-Boot and
# the kernel takes it over with omap_wdt.early_enable.
[ "${WLA_UBOOT_NAND_BOOT:-}" = 1 ] && MINIMAL_INIT_CONFIG="--enable USB_HOST --enable USB_STORAGE --enable FS_FAT --disable FAT_WRITE --disable WATCHDOG_AUTOSTART"
[ -n "${ST10_UBOOT_KERNEL_INSTALL:-}${WLA_UBOOT_FIT_NC_PROBE:-}${WLA_UBOOT_PING_BACKUP_PROBE:-}${WLA_UBOOT_NAND_DUMP:-}${WLA_UBOOT_NAND_SCRIPT:-}" ] && MINIMAL_INIT_CONFIG="$MINIMAL_INIT_CONFIG --enable NETCONSOLE --enable CMD_SLEEP --enable CMD_MEMORY"
# Netconsole comes up so the loader has a gadget to attach to and we hear RU-start, and
# goes to nulldev for the transfer itself. Three shapes were tried on the board:
#
# - console live throughout: the download dies at `block=2` every time. Uploads do not
#   suffer this -- the dump probe sends 16 MiB with the console printing -- so it is the
#   direction that matters, not the transfer.
# - no netconsole at all: nothing brings the gadget up until the transfer does, the
#   loader is not attached when it tries, and the board leaves the bus entirely.
# - console to nulldev across the transfer: works. This is also what the install probe
#   does, and the reason that shape looked broken here for an hour was unrelated -- the
#   command was spelled `tftp`, which this U-Boot does not have, and the error went to
#   nulldev where nobody could read it.
# The restore script is a legacy script image run with `source`.
[ -n "${WLA_UBOOT_NAND_SCRIPT:-}" ] && MINIMAL_INIT_CONFIG="$MINIMAL_INIT_CONFIG --enable CMD_NAND_TRIMFFS --enable CMD_SOURCE --enable LEGACY_IMAGE_FORMAT"
# The shell build needs netconsole for the same reason the probes do -- it *is* the console
# -- and the SPL-hash gate reads CONFIG_NETCONSOLE to tell a diagnostic build from the
# production one, so leaving it out made this pair claim to be the production SPL.
# The markers need the command, and the write needs to outlive the 60 s warm reset: a
# 5 MiB crc32 plus a 5 MiB erase and write does not fit inside it, and a reset landing in
# the middle of that leaves half a kernel. The install run is its own observable -- a
# fresh mask-ROM generation means every gated step succeeded.
[ -n "${ST10_UBOOT_KERNEL_INSTALL:-}" ] && MINIMAL_INIT_CONFIG="$MINIMAL_INIT_CONFIG --enable CMD_WLAMARK --disable WATCHDOG_AUTOSTART"
# The backup probe is the only netconsole build that also has to checksum what it read.
[ -n "${ST10_UBOOT_KERNEL_INSTALL:-}${WLA_UBOOT_FIT_NC_PROBE:-}${WLA_UBOOT_PING_BACKUP_PROBE:-}${WLA_UBOOT_NAND_DUMP:-}${WLA_UBOOT_NAND_SCRIPT:-}" ] && MINIMAL_INIT_CONFIG="$MINIMAL_INIT_CONFIG --enable CMD_CRC32 --enable CMD_TFTPBOOT --enable CMD_TFTPPUT --enable SYS_DEVICE_NULLDEV --enable CMD_ITEST --enable CRC32_VERIFY"
# Separate knob, because the watchdog is the only NEGATIVE indicator this board gives us.
# The 2026-08-27 14:42 rerun saw a fresh mask ROM 50.4 s after TFTP completion, which is
# 16% short of the configured 60 s. So
# "that reset was the watchdog" is not proven yet, and disabling the watchdog is the control
# that decides it: reset gone -> it was the watchdog and the hang is real; reset still near
# 50 s -> something else resets the SoC. Bundling this with MINIMAL_INIT would have made
# that control impossible to run on its own.
[ "${WLA_UBOOT_NO_WATCHDOG:-}" = 1 ] && MINIMAL_INIT_CONFIG="$MINIMAL_INIT_CONFIG --disable WATCHDOG_AUTOSTART"
POST_ENV_RESET=${WLA_UBOOT_POST_ENV_RESET:-}
[ "$POST_ENV_RESET" = "" ] || [ "$POST_ENV_RESET" = 1 ] || {
  printf '%s\n' "WLA_UBOOT_POST_ENV_RESET must be 1 or empty" >&2
  exit 2
}
NO_WATCHDOG=${WLA_UBOOT_NO_WATCHDOG:-}
[ "$NO_WATCHDOG" = "" ] || [ "$NO_WATCHDOG" = 1 ] || {
  printf '%s\n' "WLA_UBOOT_NO_WATCHDOG must be 1 or empty" >&2
  exit 2
}
# Without a marker the run has no positive signal, and
# without the watchdog it has no negative one either, so a silent bus would mean nothing.
# The ROM-USB restore counts as its own marker: it prints RU-complete over netconsole, and a
# warm reset landing inside a nand write would leave half a block, so it may drop the
# watchdog. Measured on an earlier install run: the watchdog fires 55-60 s in, and that run
# reached its writes at about 50 s.
[ "$NO_WATCHDOG" != 1 ] || [ "$(printf %s "${WLA_UBOOT_NAND_SCRIPT:+1}$POST_ENV_RESET" | tr -cd 1 | wc -c)" -eq 1 ] || {
  printf '%s\n' "WLA_UBOOT_NO_WATCHDOG needs exactly one reset checkpoint to be observable" >&2
  exit 2
}
# The profile the USB-stick recovery holds a signed stick to (ADR 0048): a production U-Boot
# takes only production packages, an alpha one both. The image's own, as Buildroot passes it
# to build-variant.sh; alpha, as `make` defaults to, when nothing says otherwise.
UBOOT_PROFILE=${SOUNDTAP_PROFILE:-alpha}
case $UBOOT_PROFILE in
alpha | production) ;;
*) printf '%s\n' "SOUNDTAP_PROFILE must be alpha or production" >&2; exit 2 ;;
esac
IMAGE=debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818
VERSION=2026.07
SHA256=78e8bfc382fe388f9b55aa1daf8c563522a037779b5d4c349d1415e381f1243e

mkdir -p "$OUT"
OUT=$(CDPATH= cd -- "$OUT" && pwd)
mkdir -p "$CACHE"
CACHE=$(CDPATH= cd -- "$CACHE" && pwd)
# Docker layer cache retains pinned build dependencies; CACHE retains verified source and tree.
BUILD_IMAGE=${WLA_UBOOT_BUILD_IMAGE:-soundtap-wla-uboot:bookworm-2026.07}
if [ "${SOUNDTAP_REPO_REVISION+x}" = x ]; then
  REPO_REV=$SOUNDTAP_REPO_REVISION
  case "$REPO_REV" in
    ????????????????????????????????????????) ;;
    *) printf '%s\n' "SOUNDTAP_REPO_REVISION must be exactly 40 hexadecimal characters" >&2; exit 2 ;;
  esac
  case "$REPO_REV" in
    *[!0123456789abcdefABCDEF]*) printf '%s\n' "SOUNDTAP_REPO_REVISION must be exactly 40 hexadecimal characters" >&2; exit 2 ;;
  esac
else
  REPO_REV=$(git -C "$REPO" rev-parse HEAD)
fi
[ "${WLA_UBOOT_CLEAN:-}" != 1 ] && BUILD_FLAGS= || BUILD_FLAGS=--no-cache
# BUILD_FLAGS intentionally expands to Docker argv; only fixed local value is accepted above.
docker build -q $BUILD_FLAGS -t "$BUILD_IMAGE" -f "$HERE/Dockerfile.wla-uboot" "$HERE" >/dev/null
BUILD_IMAGE_ID=$(docker image inspect --format "{{.Id}}" "$BUILD_IMAGE")
docker run --rm -e BOOTCOMMAND -e MINIMAL_INIT_CONFIG -e SOURCE_DATE_EPOCH -e REPO_REV="$REPO_REV" -e BUILD_IMAGE_ID="$BUILD_IMAGE_ID" -v "$REPO:/repo:ro" -v "$OUT:/out" -v "$CACHE:/cache" "$BUILD_IMAGE" sh -euc '
  export DEBIAN_FRONTEND=noninteractive
  archive=/cache/u-boot-'"$VERSION"'.tar.bz2
  source=/cache/u-boot-'"$VERSION"'
  [ -f "$archive" ] || curl -fsSL https://ftp.denx.de/pub/u-boot/u-boot-'"$VERSION"'.tar.bz2 -o "$archive"
  printf "%s  %s\n" '"$SHA256"' "$archive" | sha256sum -c -
  rm -rf "$source"
  tar -xjf "$archive" -C /cache
  rm -rf /work && cp -a "$source" /work
  cd /work
  test "$(sed -n "s/^VERSION = //p" Makefile).$(sed -n "s/^PATCHLEVEL = //p" Makefile)" = '"$VERSION"'
  git init -q
  git add . && GIT_AUTHOR_DATE="@$SOURCE_DATE_EPOCH +0000" GIT_COMMITTER_DATE="@$SOURCE_DATE_EPOCH +0000" \
    git -c user.name=build -c user.email=build@example.invalid commit -qm source
  git apply /repo/os/bootloader/uboot/wla-am335x-bringup.patch
  git apply /repo/os/bootloader/uboot/wla-uboot-nand-base.patch
  # Always. Without it our U-Boot cannot boot a stock kernel slot that the vendor
  # updater has marked, which is the slot their updater writes. It adds a command and
  # nothing else.
  git apply /repo/os/bootloader/uboot/wla-uboot-fixsig.patch
  # Always, and for the same reason: their kernel is not CONFIG_CMDLINE_FORCE and expects
  # the bootloader to hand it a live variant_raw. Ours never sampled it.
  git apply /repo/os/bootloader/uboot/wla-uboot-variant-raw.patch
  # ST20 SCM: NAND WP# on gpmc_wpn, muxed when SYSBOOT says ST20; also sets rt_v/rt_m for every model.
  git apply /repo/os/bootloader/uboot/uboot-2026.07-scm-nand-wp.patch
  # ST10/WLA: Wi-Fi lamp on from U-Boot, like stock; skipped at runtime on the ST20.
  git apply /repo/os/bootloader/uboot/uboot-2026.07-early-lamp.patch
  # VOLUME DOWN + PRESET 4 at power-on installs Update.stu from a USB stick, as the stock
  # bootloader does, when Update.stu.sig verifies (ADR 0048). Compiled only where USB storage
  # and FAT are on: the NAND product build.
  git apply /repo/os/bootloader/uboot/uboot-2026.07-usb-recovery.patch
  # ST20: the first boot frame on the OLED from U-Boot; skipped at runtime elsewhere.
  git apply /repo/os/bootloader/uboot/uboot-2026.07-st20-splash.patch
  # The redundant environment upgrades from the 8 KiB single copy, not one of its own size
  # (ADR 0010). Inert in every build without CONFIG_ENV_REDUNDANT.
  git apply /repo/os/bootloader/uboot/uboot-2026.07-env-redundant.patch
  # An unconfirmed slot counts its boots in that environment as well as in RTC SCRATCH2,
  # which a cold reset clears (ADR 0010). Without upgrade_available nothing changes.
  git apply /repo/os/bootloader/uboot/uboot-2026.07-bootcount-env.patch
  cp /repo/os/bootloader/uboot/soundtap-splash.h board/ti/am335x/
  # The statement verifier of the USB stick, TweetNaCl as upstream ships it (ADR 0124), the
  # release keys (generated from app/internal/release) and the profile, next to board.c.
  cp /repo/os/bootloader/uboot/soundtap-sig.c /repo/os/bootloader/uboot/soundtap-sig.h \
    /repo/os/bootloader/uboot/soundtap-keys.h /repo/os/bootloader/uboot/tweetnacl.c \
    /repo/os/bootloader/uboot/tweetnacl.h board/ti/am335x/
  printf "#define SOUNDTAP_UBOOT_PROFILE \"%s\"\n" '"$UBOOT_PROFILE"' > board/ti/am335x/soundtap-profile.h
  [ "'"$UBOOT_MARKERS"'" = 1 ] && git apply /repo/os/bootloader/uboot/wla-uboot-nand-markers.patch
  [ "'"$UBOOT_MARKERS"'" = 1 ] && git apply /repo/os/bootloader/uboot/wla-uboot-mark-cmd.patch
  [ "'"$POST_ENV_RESET"'" = 1 ] && git apply --unidiff-zero /repo/os/bootloader/uboot/wla-uboot-post-env-reset.patch
  export ARCH=arm CROSS_COMPILE=arm-linux-gnueabihf-
  make am335x_evm_defconfig < /dev/null
  ./scripts/config --file .config \
    --disable OF_UPSTREAM --set-str DEFAULT_DEVICE_TREE am335x-soundtap-wla \
    --enable SPL_USB_GADGET --enable SPL_USB_ETHER --enable SPL_NET \
    --disable SPL_ETH --disable SPL_MTD --disable SPL_NAND_SUPPORT --disable SPL_NAND_DRIVERS \
    --disable SPL_NAND_ECC --disable SPL_NAND_BASE --disable SPL_OS_BOOT \
    --disable SPL_NAND_AM33XX_BCH --disable SPL_I2C --disable SPL_DM_I2C \
    --disable SPL_POWER --disable SPL_POWER_TPS65910 --disable SPL_PMIC \
    --disable SPL_SERIAL --disable SPL_YMODEM_SUPPORT --disable SPL_WDT \
    --disable SPL_FALCON_BOOT_MMCSD --disable SPL_MMC --disable SPL_FAT_SUPPORT \
    --disable SPL_DFU --disable SPL_USB_HOST --disable SPL_USB_STORAGE \
    --disable SPL_LOAD_FIT --enable SPL_LEGACY_IMAGE_FORMAT \
     --disable SPL_LIBDISK_SUPPORT --disable SPL_FS_FAT --disable SPL_SYS_MALLOC --set-val SPL_SYS_MALLOC_F_LEN 0x2000 \
     --set-val SPL_TEXT_BASE 0x402f0400 --set-val SPL_SIZE_LIMIT 0xfc00 \
     --set-val SYS_TEXT_BASE 0x80100000 --set-val TEXT_BASE 0x80100000 \
    --enable MTD --enable MTD_RAW_NAND --enable NAND_OMAP_GPMC --enable NAND_OMAP_ELM \
    --enable NAND_OMAP_ECCSCHEME_BCH8_CODE_HW --disable NAND_OMAP_GPMC_PREFETCH \
     --enable CMD_NAND --enable CMD_MTDPARTS --enable MTD_PARTITIONS --enable CMD_UBI --enable CMD_UBIFS --enable MTD_UBI \
     --enable CMD_FIXSIG \
     --disable EFI_LOADER --disable BOOTSTD --disable DISTRO_DEFAULTS --disable PXE_UTILS \
     --disable FIT_SIGNATURE --disable FIT_VERBOSE --disable FIT_PRINT \
     --disable CMD_BOOTFLOW --disable CMD_BOOTZ --disable CMD_BOOTEFI --disable CMD_ELF \
     --disable CMD_FDT --disable CMD_EXTENSION --disable CMD_GO --disable CMD_RUN --disable CMD_IMI \
     --disable CMD_XIMG --disable CMD_SPL --disable CMD_ASKENV --disable CMD_EXPORTENV \
     --disable CMD_IMPORTENV --disable CMD_EDITENV --disable CMD_SAVEENV --disable CMD_ENV_EXISTS \
     --disable CMD_CRC32 --disable CMD_EEPROM --disable CMD_MEMINFO --disable CMD_MEMORY \
     --disable CMD_RANDOM --disable CMD_BIND --disable CMD_DFU --disable CMD_DM --disable CMD_FASTBOOT \
     --disable CMD_GPIO --disable CMD_GPT --disable CMD_I2C --disable CMD_LOADB --disable CMD_LOADS \
     --enable CMD_MMC --disable CMD_MMC_REG --disable CMD_PART --disable CMD_SF --disable CMD_SPI \
     --disable CMD_USB --disable CMD_MII --disable CMD_MDIO --disable CMD_NFS --disable CMD_PING \
     --disable CMD_PXE --disable CMD_BLOCK_CACHE --disable CMD_CACHE --disable CMD_EFICONFIG \
     --disable CMD_TIME --disable CMD_SLEEP --disable CMD_SYSBOOT --disable CMD_PMIC \
     --disable CMD_EXT2 --disable CMD_EXT4 --disable CMD_EXT4_WRITE --disable CMD_FAT --disable CMD_FS_GENERIC \
     --disable CMD_CYCLIC --disable SPI_FLASH --disable SPI --disable FS_EXT4 --disable FS_FAT \
     --disable USB_HOST --disable USB_STORAGE --disable USB_FUNCTION_FASTBOOT \
     '"$GADGET_CONFIG"' \
     --disable ENV_IS_IN_NAND --enable ENV_IS_NOWHERE \
    --set-str MTDIDS_DEFAULT nand0=nand0 \
    --set-str MTDPARTS_DEFAULT "mtdparts=nand0:0x20000@0x0(NAND.SPL),0x20000@0x20000(NAND.SPL.backup1),0x20000@0x40000(NAND.SPL.backup2),0x20000@0x60000(NAND.SPL.backup3),0x1E0000@0x80000(NAND.U-Boot),0x20000@0x260000(NAND.U-Boot-Env),0x500000@0x280000(NAND.Kernel-Ping),0x500000@0x780000(NAND.Kernel-Pong),0x6000000@0xC80000(NAND.File-System-Ping),0x6000000@0x6C80000(NAND.File-System-Pong),0x2600000@0xCC80000(NAND.Persistent-Storage),0xB80000@0xF280000(NAND.Update-Staging),0x40000@0xFE00000(NAND.Manufacturing-Data)" \
     --set-val BOOTDELAY '"$BOOTDELAY"' --set-str BOOTCOMMAND "$BOOTCOMMAND" \
    --set-str SYS_PROMPT "WLA> "
  # Unquoted on purpose: the fragment is a list of scripts/config flags. Its own call
  # rather than a line in the one above, because an empty variable interpolated there
  # becomes an empty argv entry and scripts/config rejects it.
  [ -z "${MINIMAL_INIT_CONFIG:-}" ] || ./scripts/config --file .config $MINIMAL_INIT_CONFIG
  [ -z "'"${INSTALL_CONFIG:-}"'" ] || ./scripts/config --file .config '"${INSTALL_CONFIG:-}"'
  [ -z "'"${ENV_CONFIG:-}"'" ] || ./scripts/config --file .config '"${ENV_CONFIG:-}"'
  # LOCALVERSION_AUTO appends a git hash to the version string, read from the throwaway repo
  # this script commits a few lines up. That hash moved between builds of identical sources
  # and took the U-Boot bytes with it, so every build looked like a new boot chain.
  # Turned off here rather than after the checks below, so olddefconfig runs once and what
  # those checks validate is what gets built.
  ./scripts/config --file .config --disable LOCALVERSION_AUTO
  make olddefconfig < /dev/null
  # olddefconfig has the last word on Kconfig dependencies, so check what it left rather
  # than what we asked for: a product U-Boot that silently lost its environment would boot
  # its compiled-in default and ignore every slot switch an install makes.
  if [ "'"$ENV_IN_NAND"'" = 1 ]; then
    grep -qx "CONFIG_ENV_IS_IN_NAND=y" .config
    grep -qx "CONFIG_ENV_OFFSET=0x260000" .config
    grep -qx "CONFIG_ENV_SIZE=0x8000" .config
    grep -qx "CONFIG_ENV_RANGE=0x20000" .config
    grep -qx "CONFIG_ENV_REDUNDANT=y" .config
    grep -qx "CONFIG_ENV_REDUNDANT_UPGRADE=y" .config
    grep -qx "CONFIG_ENV_OFFSET_REDUND=0x240000" .config
    grep -qx "CONFIG_CMD_SAVEENV=y" .config
  fi
  grep -qx "CONFIG_SPL_LEGACY_IMAGE_FORMAT=y" .config
  # Every guarded bootcmd -- the install, the restore -- is `if ...; then ...; fi`, and
  # that is the hush parser. It has no Kconfig default and DISTRO_DEFAULTS is off here;
  # it survives only as the value am335x_evm_defconfig wrote. If it ever stops surviving,
  # a guarded bootcmd becomes a list of commands that all run, which for the writing
  # builds means erasing on a failed checksum.
  grep -qx "CONFIG_HUSH_PARSER=y" .config
  cp /repo/os/bootloader/uboot/uboot-am335x-soundtap-wla.dts arch/arm/dts/am335x-soundtap-wla.dts
  printf "\ndtb-y += am335x-soundtap-wla.dtb\n" >> arch/arm/dts/Makefile
  python3 /repo/os/support/check_wla_uboot_dts.py .
  make dtbs
  # U-Boot builds its timestamp header from SOURCE_DATE_EPOCH itself when that is set --
  # filechk_timestamp.h in its Makefile -- so nothing more is needed here for the time. The
  # git hash beside it comes from LOCALVERSION_AUTO and is turned off in the config step.
  make -j${WLA_UBOOT_JOBS:-2}
  # MLO wraps SPL+DTB with 512-byte config and 8-byte GP headers. Its input is
  # removed as a make intermediate, so strip wrapper from retained MLO instead.
  dd if=MLO of=/out/MLO bs=1 skip=520 status=none
   ./tools/mkimage -A arm -O u-boot -T firmware -C none \
     -a 0x80100000 -e 0x80100000 -n "U-Boot WLA RAM net" \
     -d u-boot-dtb.bin /out/u-boot.img
   cp u-boot u-boot.map u-boot-dtb.bin .config spl/u-boot-spl spl/u-boot-spl.map /out/
  arm-linux-gnueabihf-size -A -d spl/u-boot-spl > /out/spl-size.txt
  arm-linux-gnueabihf-nm -n spl/u-boot-spl > /out/spl-symbols.txt
  arm-linux-gnueabihf-objdump -d spl/u-boot-spl > /out/spl-disasm.txt
  arm-linux-gnueabihf-objdump -d u-boot > /out/u-boot-disasm.txt
  {
    printf "source_url=https://ftp.denx.de/pub/u-boot/u-boot-'"$VERSION"'.tar.bz2\n"
    printf "source_sha256='"$SHA256"'\n"
    printf "source_revision=ece349ade2973e220f524ce59e59711cc919263f\n"
    printf "patches_sha256="; sha256sum /repo/os/bootloader/uboot/*.patch | sha256sum | cut -d" " -f1
    printf "verifier_sha256="; sha256sum /repo/os/bootloader/uboot/soundtap-sig.[ch] /repo/os/bootloader/uboot/soundtap-keys.h /repo/os/bootloader/uboot/tweetnacl.[ch] | sha256sum | cut -d" " -f1
    printf "profile='"$UBOOT_PROFILE"'\n"
    printf "builder_sha256="; sha256sum /repo/os/bootloader/build-wla-uboot.sh | cut -d" " -f1
    printf "checker_sha256="; sha256sum /repo/os/support/check_wla_uboot_ram.py | cut -d" " -f1
    printf "dts_sha256="; sha256sum /repo/os/bootloader/uboot/uboot-am335x-soundtap-wla.dts | cut -d" " -f1
    printf "repo_revision=%s\n" "$REPO_REV"
    printf "source_date_epoch=%s\n" "$SOURCE_DATE_EPOCH"
    printf "config_sha256="; sha256sum .config | cut -d" " -f1
    printf "container='"$IMAGE"'\n"
    printf "build_image='"$BUILD_IMAGE"'\n"
    printf "build_image_id=%s\n" "$BUILD_IMAGE_ID"
  } > /out/PROVENANCE
'
WLA_UBOOT_LEGACY_SPL=${WLA_UBOOT_LEGACY_SPL:-} WLA_UBOOT_NAND_INSTALL=$NAND_INSTALL WLA_UBOOT_NO_WATCHDOG=$NO_WATCHDOG WLA_UBOOT_POST_ENV_RESET=$POST_ENV_RESET WLA_UBOOT_ENV_IN_NAND=$ENV_IN_NAND python3 "$REPO/os/support/check_wla_uboot_ram.py" "$OUT"
[ "${WLA_UBOOT_NAND_BOOT:-}" != 1 ] || python3 "$REPO/os/support/check_wla_uboot_nand.py" \
  ${ENV_IN_NAND:+--env-in-nand} "$OUT"
sha256sum "$OUT/MLO" "$OUT/u-boot.img"
