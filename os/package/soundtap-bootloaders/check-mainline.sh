#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Hold the Buildroot bootloaders to os/bootloader, the reference.
#
#   check-mainline.sh [--update] [VARIANT...]
#
# For each variant (default: all three) this builds the images with the mainline script,
# requires configs/VARIANT.config to equal the .config that script left behind (--update
# copies it in instead), then builds the same variant with build-variant.sh in the mainline
# builder image and requires every shipped image to be byte-identical. Docker only.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$here/../../.." && pwd)
update=
[ "${1:-}" != --update ] || { update=1; shift; }
variants=${*:-st20-spl wla-uboot wla-spl}
out=${BOOTLOADER_CHECK_OUT:-$repo/os/bootloader/build/buildroot-bootloaders}
mainline=$repo/os/bootloader
mkdir -p "$out"
out=$(CDPATH= cd -- "$out" && pwd)
tarball=$out/u-boot-2026.07.tar.bz2
# ftp.denx.de first, then Buildroot's mirror of it, as the build itself falls back; the hash
# below decides what is accepted either way.
for site in https://ftp.denx.de/pub/u-boot https://sources.buildroot.net/uboot; do
	[ -f "$tarball" ] || curl -fsSL --connect-timeout 20 "$site/u-boot-2026.07.tar.bz2" -o "$tarball" ||
		rm -f "$tarball"
done
grep -q "$(sha256sum <"$tarball" | cut -d ' ' -f 1)  u-boot-2026.07.tar.bz2" "$here/soundtap-bootloaders.hash" || {
	echo "$tarball does not match soundtap-bootloaders.hash" >&2; rm -f "$tarball"; exit 1
}
# The image build-wla-uboot.sh builds; the product SPL script installs the same packages.
docker build -q -t soundtap-wla-uboot:bookworm-2026.07 -f "$mainline/Dockerfile.wla-uboot" "$mainline" >/dev/null

for v in $variants; do
	ref=$out/mainline-$v
	# The same flags the STU builders use; see build.yml.
	case $v in
	st20-spl) ST20_SPL=1 WLA_PRODUCT_SPL_OUT=$ref sh "$mainline/build-wla-product-spl.sh"
		images="MLO u-boot-spl.bin uImage-spl-sram" ;;
	wla-uboot) WLA_UBOOT_LEGACY_SPL=1 WLA_UBOOT_NAND_BOOT=1 WLA_UBOOT_ENV_IN_NAND=1 \
		WLA_UBOOT_OUT=$ref sh "$mainline/build-wla-uboot.sh"
		images="u-boot.img" ;;
	wla-spl) WLA_PRODUCT_SPL_OUT=$ref sh "$mainline/build-wla-product-spl.sh"
		images="MLO" ;;
	*) echo "unknown bootloader variant $v" >&2; exit 2 ;;
	esac
	if [ -n "$update" ]; then
		cp "$ref/.config" "$here/configs/$v.config"
	elif ! cmp -s "$ref/.config" "$here/configs/$v.config"; then
		diff -u "$here/configs/$v.config" "$ref/.config" >&2 || :
		echo "$v: configs/$v.config is not the .config the mainline script produces;" \
			"run $0 --update $v" >&2
		exit 1
	fi

	docker run --rm -e SOURCE_DATE_EPOCH="$(cat "$mainline/source-date-epoch")" \
		-e CROSS_COMPILE=arm-linux-gnueabihf- -e JOBS="${JOBS:-2}" \
		-v "$repo:/repo:ro" -v "$out:/check" soundtap-wla-uboot:bookworm-2026.07 \
		sh /repo/os/package/soundtap-bootloaders/build-variant.sh "$v" \
		/check/u-boot-2026.07.tar.bz2 \
		"/tmp/build-$v" "/check/buildroot-$v"
	for f in $images; do
		a=$(sha256sum <"$ref/$f" | cut -d ' ' -f 1)
		b=$(sha256sum <"$out/buildroot-$v/$f" | cut -d ' ' -f 1)
		[ "$a" = "$b" ] || { echo "$v/$f: mainline $a, buildroot $b" >&2; exit 1; }
		echo "$v/$f $a identical"
	done
done
