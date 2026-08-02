#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

set -eu

repo=$1
output=$2
action=$3
defconfig=os/configs/soundtap_defconfig
linux_state=$output/.soundtap-linux-inputs.sha256
linux_dts_state=$output/.soundtap-linux-dts-inputs.sha256
loader_state=$output/.soundtap-aic3256-loader-inputs.sha256

manifest=$(mktemp)
trap 'rm -f "$manifest"' EXIT

# digest FUNCTION: the sha256 of what FUNCTION prints. Each step runs on its own, never
# upstream of a pipe, so a missing input or a failed hash stops the script under set -e
# instead of yielding a partial digest that would then be recorded.
digest() {
	"$1" >"$manifest"
	sum=$(sha256sum "$manifest")
	printf '%s\n' "${sum%% *}"
}

linux_inputs() {
	cd "$repo"
	grep -E '^BR2_(arm|cortex_a8|ARM_EABIHF|ARM_FPU|TOOLCHAIN|KERNEL_HEADERS|GLOBAL_PATCH_DIR|LINUX_KERNEL)' \
		"$defconfig"
	files=$(find \
		os/board/common/patches/linux \
		os/board/image/linux.fragment \
		os/board/rootfs/linux-patches \
		os/drivers/soundtap-st10-audio.c \
		os/external.mk \
		-type f -print)
	printf '%s\n' "$files" | LC_ALL=C sort -u | xargs sha256sum
}

linux_dts_inputs() {
	cd "$repo"
	files=$(find os/board/common/dts -type f -print)
	printf '%s\n' "$files" | LC_ALL=C sort | xargs sha256sum
}

loader_inputs() {
	grep -E '^BR2_(arm|cortex_a8|ARM_FPU|TOOLCHAIN)' \
		"$repo/$defconfig"
	sha256sum \
		"$repo/os/drivers/aic3256-loader.c" \
		"$repo/os/drivers/aic3256-bitbang.c" \
		"$repo/os/package/soundtap-aic3256-loader/Config.in" \
		"$repo/os/package/soundtap-aic3256-loader/soundtap-aic3256-loader.hash" \
		"$repo/os/package/soundtap-aic3256-loader/soundtap-aic3256-loader.mk"
}

linux_digest=$(digest linux_inputs)
linux_dts_digest=$(digest linux_dts_inputs)
loader_digest=$(digest loader_inputs)

kver=$(sed -n 's/^BR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE="\(.*\)"$/\1/p' "$repo/os/configs/soundtap_defconfig")

case "$action" in
prepare)
	if [ -d "$output/build/linux-$kver" ] \
		&& { [ ! -f "$linux_state" ] || [ "$(cat "$linux_state")" != "$linux_digest" ]; }; then
		make -C "$repo/buildroot" O="$output" BR2_EXTERNAL="$repo/os" linux-dirclean
	elif [ -d "$output/build/linux-$kver" ] \
		&& { [ ! -f "$linux_dts_state" ] || [ "$(cat "$linux_dts_state")" != "$linux_dts_digest" ]; }; then
		make -C "$repo/buildroot" O="$output" BR2_EXTERNAL="$repo/os" linux-rebuild
	fi
	if [ -d "$output/build/soundtap-aic3256-loader-1" ] \
		&& { [ ! -f "$loader_state" ] || [ "$(cat "$loader_state")" != "$loader_digest" ]; }; then
		make -C "$repo/buildroot" O="$output" BR2_EXTERNAL="$repo/os" \
			soundtap-aic3256-loader-dirclean
	fi
	;;
record)
	printf '%s\n' "$linux_digest" >"$linux_state"
	printf '%s\n' "$linux_dts_digest" >"$linux_dts_state"
	printf '%s\n' "$loader_digest" >"$loader_state"
	;;
*)
	printf 'usage: %s REPO OUTPUT prepare|record\n' "$0" >&2
	exit 2
	;;
esac
