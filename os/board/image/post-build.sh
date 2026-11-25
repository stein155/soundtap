#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Runs after rootfs/post-build.sh (module prune, gadget console, rhino and binky trees).
# Every model's kernel and the trees rootfs does not install are written here, then
# checked to be there.
set -eu

target=$1
: "${BINARIES_DIR:?}" "${HOST_DIR:?}"
# mkimage below stamps each uImage header with SOURCE_DATE_EPOCH, or with the clock when it
# is unset, and the rootfs would differ from build to build (ADR 0011).
: "${SOURCE_DATE_EPOCH:?the build sets SOURCE_DATE_EPOCH}"

# slot_boot tries /boot/uImage-${variant} out of the selected slot before the kernel
# partition. One kernel, so the same bytes under each model's name: hard links, which
# mkfs.ubifs keeps, so the slot holds them once.
# shellcheck disable=SC2250 # os/tests/boot/test_wla_uboot_ram.py matches this text as written
"${HOST_DIR}/bin/mkimage" -A arm -O linux -T kernel -C none \
	-a 0x80008000 -e 0x80008000 -n "Linux-6.12 SoundTap st10" \
	-d "$BINARIES_DIR/zImage" "$target/boot/uImage-rhino"
chmod 0644 "${target}/boot/uImage-rhino"
for variant in binky ginger; do
	# shellcheck disable=SC2250 # os/tests/boot/test_wla_uboot_ram.py matches this text as written
	ln -f "$target/boot/uImage-rhino" "$target/boot/uImage-$variant"
done
# The ST20's copy carries its own name: the packager checks each uImage's name against
# the model it is for. Its fallback, the kernel partition, runs Bose's command line, so
# the ST20 pair (kernel and spotty_scm.dtb) must both be here or the image is refused.
"${HOST_DIR}/bin/mkimage" -A arm -O linux -T kernel -C none \
	-a 0x80008000 -e 0x80008000 -n "Linux-6.12 SoundTap st20" \
	-d "${BINARIES_DIR}/zImage" "${target}/boot/uImage-spotty"
size=$(wc -c <"${target}/boot/uImage-spotty")
test "${size}" -le 4980736 || {
	printf 'uImage-spotty %s exceeds 4.75 MiB limit\n' "${size}" >&2
	exit 1
}
# The kernel is in the slot as uImage-*; the raw copy Buildroot installs is not read by
# anything and costs 4 MiB of a tight rootfs budget.
rm -f "${target}/boot/zImage"
install -m 0644 "${BINARIES_DIR}/am335x-soundtap-st20.dtb" "${target}/boot/spotty_scm.dtb"
install -m 0644 "${BINARIES_DIR}/am335x-soundtap-st20.dtb" "${target}/boot/am335x-soundtap-st20.dtb"
# SoundTouch Portable preview: the ST20's board with its battery gauge.
install -m 0644 "${BINARIES_DIR}/am335x-soundtap-portable.dtb" "${target}/boot/taigan_scm.dtb"
# SoundTouch adapter preview on the SCM: the ST20's board with the Bose link on UART0.
install -m 0644 "${BINARIES_DIR}/am335x-soundtap-lisa-scm.dtb" "${target}/boot/lisa_scm.dtb"
kver=$(sed -n 's/^BR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE="\(.*\)"$/\1/p' "${BR2_CONFIG:?}")
# 4 KiB of headroom in the ST10 tree for what U-Boot writes into it at boot.
"${BUILD_DIR:?}/linux-${kver}/scripts/dtc/dtc" -q -I dtb -O dtb -p 4096 \
	-o "${target}/boot/am335x-soundtap-st10.dtb" "${BINARIES_DIR}/am335x-soundtap-st10.dtb"
install -m 0644 "${target}/boot/am335x-soundtap-st10.dtb" "${target}/boot/rhino_sm2.dtb"
# SoundTouch 300: its U-Boot boots /boot/ginger_sm2.dtb (variant ginger, module sm2).
install -m 0644 "${BINARIES_DIR}/am335x-soundtap-st300.dtb" "${target}/boot/ginger_sm2.dtb"
# SoundTouch 20/30 Series III preview: the ST10's module under its own tree, whose lamp
# pads are other things on these boards. Same 4 KiB headroom as the ST10's.
"${BUILD_DIR:?}/linux-${kver}/scripts/dtc/dtc" -q -I dtb -O dtb -p 4096 \
	-o "${target}/boot/spotty3_sm2.dtb" "${BINARIES_DIR}/am335x-soundtap-st20-iii.dtb"
# SoundTouch SA-5 preview: the ST10's module on its amplifier board. Same headroom.
"${BUILD_DIR:?}/linux-${kver}/scripts/dtc/dtc" -q -I dtb -O dtb -p 4096 \
	-o "${target}/boot/burns_sm2.dtb" "${BINARIES_DIR}/am335x-soundtap-sa5.dtb"
# SoundTouch adapter preview on the SM2: the ST10's module without its codec. Same headroom.
"${BUILD_DIR:?}/linux-${kver}/scripts/dtc/dtc" -q -I dtb -O dtb -p 4096 \
	-o "${target}/boot/lisa_sm2.dtb" "${BINARIES_DIR}/am335x-soundtap-lisa.dtb"

conf=${target}/lib/firmware/ti-connectivity
# linux-firmware's TI_WL18XX also brings older firmware revisions and WiLink6/7 files; the
# wl18xx driver loads only wl18xx-fw-4.bin, and it must be the upstream one (ADR 0047).
# The nvs links point at the WiLink6/7 file just removed; wl18xx needs none.
rm -f "${conf}/wl18xx-fw.bin" "${conf}/wl18xx-fw-2.bin" "${conf}/wl18xx-fw-3.bin" \
	"${conf}/wl127x-nvs.bin" "${conf}/wl1271-nvs.bin" "${conf}/wl12xx-nvs.bin" "${conf}/TIInit_7.2.31.bts"
[ "$(sha256sum "${conf}/wl18xx-fw-4.bin" | cut -d ' ' -f 1 || true)" = \
	95f04731257febabc358b95ff06dbb2025d047c6ad3ae0239c8afb92dedc7c5c ] ||
	{ echo "post-build: wl18xx-fw-4.bin is not linux-firmware's" >&2; exit 1; }
# soundtap-wl18xx-conf links wl18xx-conf.bin to DF; soundtapd points it at the speaker's region.
test -f "${conf}/wl18xx-conf.bin" || { echo "post-build: wl18xx-conf.bin missing" >&2; exit 1; }

for f in uImage-rhino uImage-binky uImage-ginger uImage-spotty rhino_sm2.dtb binky_sm2.dtb \
	ginger_sm2.dtb spotty_scm.dtb spotty3_sm2.dtb taigan_scm.dtb burns_sm2.dtb lisa_scm.dtb lisa_sm2.dtb; do
	test -s "${target}/boot/${f}" || { echo "post-build: /boot/${f} missing" >&2; exit 1; }
done

# SoundTap Core names itself (ADR 0035): the version from os/VERSION, in
# /etc/soundtap/os-version for soundtapd and in os-release for everything else. Buildroot's
# own version stays readable as BUILDROOT_VERSION.
os_version=$(tr -d ' \n' <"$(dirname "$0")/../../VERSION")
case ${os_version} in [0-9]*.[0-9]*.[0-9]*) ;; *) echo "post-build: bad VERSION '${os_version}'" >&2; exit 1 ;; esac
mkdir -p "${target}/etc/soundtap"
printf '%s\n' "${os_version}" >"${target}/etc/soundtap/os-version"
# And the commit it was built from, so /api/version can tell two builds of one version apart.
commit=${SOUNDTAP_COMMIT:?the build sets SOUNDTAP_COMMIT}
printf '%s\n' "${commit}" >"${target}/etc/soundtap/os-commit"
br_version=$(sed -n 's/^VERSION_ID=//p' "${target}/usr/lib/os-release")
cat >"${target}/usr/lib/os-release" <<EOF
NAME="SoundTap Core"
VERSION="${os_version}"
ID=soundtap
VERSION_ID=${os_version}
PRETTY_NAME="SoundTap Core ${os_version}"
HOME_URL="https://github.com/stein155/soundtap"
BUILDROOT_VERSION=${br_version}
BUILD_ID=${commit}
EOF
