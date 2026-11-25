#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Build one bootloader image set the way os/bootloader does, from a saved final .config.
#
#   build-variant.sh VARIANT UBOOT_TARBALL WORKDIR OUTDIR
#
# VARIANT is st20-spl, wla-uboot or wla-spl. The caller sets CROSS_COMPILE and
# SOURCE_DATE_EPOCH. Buildroot calls this from soundtap-bootloaders.mk; CI also runs it in the
# mainline builder image and demands the same bytes as build-wla-uboot.sh and
# build-wla-product-spl.sh (.github/workflows/buildroot-bootloaders.yml).
#
# The scripts/config calls are not repeated here. configs/VARIANT.config is the .config the
# mainline script leaves behind (update-configs.sh copies it out), so what gets built is that
# script's final config by construction, and olddefconfig must leave it byte-identical.
set -eu

variant=$1
tarball=$2
work=$3
out=$4
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "${here}/../../.." && pwd)
: "${CROSS_COMPILE:?}" "${SOURCE_DATE_EPOCH:?}"
export ARCH=arm CROSS_COMPILE SOURCE_DATE_EPOCH
# The image's profile, which the USB-stick recovery holds a signed package to (ADR 0048);
# soundtap-bootloaders.mk passes it, and check-mainline.sh builds alpha as mainline does.
profile=${SOUNDTAP_PROFILE:-alpha}
case ${profile} in
alpha | production) ;;
*) echo "SOUNDTAP_PROFILE must be alpha or production" >&2; exit 2 ;;
esac
# Make, kbuild and git read these from the environment. Buildroot passes some of them down
# (HOST_MAKE_ENV sets GIT_DIR=.); the mainline container has none of them, and LOCALVERSION
# set to anything, even empty, drops the "+" setlocalversion gives the mainline tree.
unset MAKEFLAGS MFLAGS MAKELEVEL MAKEOVERRIDES O KBUILD_OUTPUT LOCALVERSION KCFLAGS GIT_DIR GIT_WORK_TREE \
	CFLAGS CPPFLAGS LDFLAGS

# The patch lists are the unconditional `git apply` lines of each mainline script; the two
# SPLs differ only in their config. os/tests/build/test_buildroot_bootloaders.py holds them to that.
base="os/bootloader/uboot/wla-am335x-bringup.patch os/bootloader/uboot/wla-uboot-nand-base.patch"
# shellcheck disable=SC2250 # os/tests/build/test_buildroot_bootloaders.py matches this text as written
spl="$base os/bootloader/uboot/uboot-2026.07-spl-board.patch os/bootloader/uboot/uboot-2026.07-spl-second-uboot.patch"
# shellcheck disable=SC2250 # os/tests/build/test_buildroot_bootloaders.py matches this text as written
uboot="$base os/bootloader/uboot/wla-uboot-fixsig.patch os/bootloader/uboot/wla-uboot-variant-raw.patch os/bootloader/uboot/uboot-2026.07-scm-nand-wp.patch os/bootloader/uboot/uboot-2026.07-early-lamp.patch os/bootloader/uboot/uboot-2026.07-usb-recovery.patch os/bootloader/uboot/uboot-2026.07-st20-splash.patch os/bootloader/uboot/uboot-2026.07-env-redundant.patch os/bootloader/uboot/uboot-2026.07-bootcount-env.patch"
# shellcheck disable=SC2250 # os/tests/build/test_buildroot_bootloaders.py matches this text as written
case ${variant} in
st20-spl | wla-spl) patches=$spl ;;
wla-uboot) patches=$uboot ;;
*) echo "unknown bootloader variant ${variant}" >&2; exit 2 ;;
esac
config=${here}/configs/${variant}.config
dts=${repo}/os/bootloader/uboot/uboot-am335x-soundtap-wla.dts

rm -rf "${work}"
mkdir -p "${work}" "${out}"
tar -xjf "${tarball}" -C "${work}"
cd "${work}/u-boot-2026.07"
test "$(sed -n 's/^VERSION = //p' Makefile || true).$(sed -n 's/^PATCHLEVEL = //p' Makefile || true)" = 2026.07
# The same throwaway repository as mainline, same author and dates, so setlocalversion
# sees the same commit and the same dirty tree. The SPL keeps LOCALVERSION_AUTO on.
git init -q
git add . && GIT_AUTHOR_DATE="@${SOURCE_DATE_EPOCH} +0000" GIT_COMMITTER_DATE="@${SOURCE_DATE_EPOCH} +0000" \
	git -c user.name=build -c user.email=build@example.invalid commit -qm source
for p in ${patches}; do git apply "${repo}/${p}"; done
cp "${repo}/os/bootloader/uboot/soundtap-splash.h" board/ti/am335x/
# The USB stick's statement verifier, the release keys and the profile the stick is held to,
# as build-wla-uboot.sh puts them next to board.c (ADR 0048).
for f in soundtap-sig.c soundtap-sig.h soundtap-keys.h tweetnacl.c tweetnacl.h; do
	cp "${repo}/os/bootloader/uboot/${f}" board/ti/am335x/
done
printf '#define SOUNDTAP_UBOOT_PROFILE "%s"\n' "${profile}" >board/ti/am335x/soundtap-profile.h

case ${variant} in
*-spl)
	cp "${dts}" arch/arm/dts/am335x-soundtap-wla.dts
	cp "${config}" .config
	make olddefconfig </dev/null
	cmp .config "${config}"
	KCFLAGS="-DCONFIG_SYS_MMC_MAX_BLK_COUNT=0 -DCONFIG_USB_MAX_CONTROLLER_COUNT=0" \
		make -j"${JOBS:-2}" spl/u-boot-spl.bin
	KCFLAGS="-DCONFIG_SYS_MMC_MAX_BLK_COUNT=0 -DCONFIG_USB_MAX_CONTROLLER_COUNT=0" \
		make -j"${JOBS:-2}" MLO
	cp MLO spl/u-boot-spl.bin .config "${out}/"
	[ "${variant}" != st20-spl ] || ./tools/mkimage -A arm -O linux -T kernel -C none \
		-a 0x402f0400 -e 0x402f0400 -n "ST20 SPL SRAM test" \
		-d spl/u-boot-spl.bin "${out}/uImage-spl-sram"
	;;
*)
	cp "${config}" .config
	make olddefconfig </dev/null
	cmp .config "${config}"
	cp "${dts}" arch/arm/dts/am335x-soundtap-wla.dts
	printf "\ndtb-y += am335x-soundtap-wla.dtb\n" >>arch/arm/dts/Makefile
	python3 "${repo}/os/support/check_wla_uboot_dts.py" .
	make dtbs
	make -j"${JOBS:-2}"
	./tools/mkimage -A arm -O u-boot -T firmware -C none \
		-a 0x80100000 -e 0x80100000 -n "U-Boot WLA RAM net" \
		-d u-boot-dtb.bin "${out}/u-boot.img"
	cp .config "${out}/"
	;;
esac
# $out survives rebuilds: drop the old manifest first, or the glob hands it to sha256sum
# and the new manifest lists a hash of itself that can never match.
(cd "${out}" && rm -f SHA256SUMS && sha256sum -- * >SHA256SUMS)
