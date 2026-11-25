#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Cross-build the WLA update installer for the stock board: static ARMv7, no libc surprises,
# no Go (the stock 3.14.43+ kernel does not run our Go builds).
#
#   build.sh [outdir]            release-shaped build, logs to /tmp only
#   WLA_PREVIEW=1 build.sh       the preview package's installer: Preview boards install with it
#   WLA_TRACE=1 build.sh [out]   trace build: every line also goes to UDP 40099 on the USB host,
#                                and the terminal-message candidates are walked
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
out=${1:-${here}/build}
mkdir -p "${out}"

# Two independent switches: WLA_NAND compiles the flash transaction in, WLA_TRACE adds the UDP
# trace the board rounds read. A binary without WLA_NAND has no code that opens a partition.
defines=""
name=wla-installer-verify
if [ "${WLA_NAND:-0}" = 1 ]; then
  defines="${defines} -DSOUNDTAP_INSTALL_NAND"
  name=wla-installer
  sources="installer.c swupdate.c mtd.c"
else
  sources="installer.c swupdate.c"
fi
# Replacing SPL and U-Boot is a separate decision from installing a slot, so it is a separate
# switch: a package built without it cannot touch mtd0-mtd4 whatever it carries.
if [ "${WLA_BOOTLOADERS:-0}" = 1 ]; then
  test "${WLA_NAND:-0}" = 1 || { echo "WLA_BOOTLOADERS needs WLA_NAND=1" >&2; exit 2; }
  defines="${defines} -DSOUNDTAP_INSTALL_BOOTLOADERS"
  name="${name}-full"
fi
# The preview package's installer (ADR 0133): the only one that installs on a Preview board.
if [ "${WLA_PREVIEW:-0}" = 1 ]; then
  defines="${defines} -DSOUNDTAP_PREVIEW_PACKAGE"
  name="${name}-preview"
fi
if [ "${WLA_TRACE:-0}" = 1 ]; then
  defines="${defines} -DSOUNDTAP_TRACE_UDP"
  name="${name}-trace"
fi

# The toolchain lives in a local image so a board round costs seconds, not an apt install.
# Tagged with its Dockerfile's digest: a fixed tag would keep whatever image a machine built
# first after the Dockerfile changed, and two machines would build other installers.
tag=$(sha256sum "${here}/toolchain/Dockerfile" | cut -c1-16)
image=${WLA_ARMHF_IMAGE:-wla-armhf-gcc:${tag}}
docker image inspect "${image}" >/dev/null 2>&1 ||
  docker build -q -t "${image}" "${here}/toolchain" >/dev/null

docker run --rm -v "${here}":/src:ro -v "${out}":/out -w /src "${image}" \
  sh -euc "
    arm-linux-gnueabihf-gcc -static -march=armv7-a -mfpu=vfpv3-d16 -O2 -Wall -Wextra \
      ${defines} -o /out/${name} ${sources}
    arm-linux-gnueabihf-strip /out/${name}
  "
ls -l "${out}/${name}"
sha256sum "${out}/${name}"
