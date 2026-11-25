#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

set -eu

loader=$1
source=$(dirname "${loader}")/rom-usb-loader.c
hash=ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
tmp=$(mktemp -d "${TMPDIR:-/tmp}/rom-usb-loader-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
printf abc > "${tmp}/MLO"
printf abc > "${tmp}/u-boot.img"
printf abc > "${tmp}/soundtap-ram-nand.itb"
printf abc > "${tmp}/soundtap-ram-capture.itb"

if "${loader}" --usb-spl --uboot-sha256 "${hash}" "${tmp}/MLO" "${tmp}/u-boot.img" --check >/dev/null 2>&1; then
	printf '%s\n' 'unpinned USB SPL MLO accepted' >&2
	exit 1
fi
if "${loader}" --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" "${tmp}/u-boot.img" --check >/dev/null 2>&1; then
	printf '%s\n' 'explicit MLO hash accepted outside USB SPL mode' >&2
	exit 1
fi
if "${loader}" --usb-spl --mlo-sha256 "${hash%?}0" --uboot-sha256 "${hash}" "${tmp}/MLO" "${tmp}/u-boot.img" --check >/dev/null 2>&1; then
	printf '%s\n' 'mismatched explicit MLO hash accepted' >&2
	exit 1
fi
"${loader}" --usb-spl --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" "${tmp}/u-boot.img" --check >/dev/null
"${loader}" --usb-spl --legacy-uboot --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" "${tmp}/u-boot.img" --check >/dev/null
"${loader}" --usb-spl --fit-payload --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" "${tmp}/u-boot.img" --payload "${hash}" "${tmp}/soundtap-ram-nand.itb" --check >/dev/null
"${loader}" --usb-spl --capture-payload --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" "${tmp}/u-boot.img" --payload "${hash}" "${tmp}/soundtap-ram-capture.itb" --check >/dev/null
if "${loader}" --usb-spl --fit-payload --capture-payload --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" "${tmp}/u-boot.img" --payload "${hash}" "${tmp}/soundtap-ram-capture.itb" --check >/dev/null 2>&1; then
	printf '%s\n' 'mutually exclusive payload aliases accepted' >&2
	exit 1
fi
# Uploads: refused unless a real directory is named, and off entirely by default.
if "${loader}" --usb-spl --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" \
	"${tmp}/u-boot.img" --upload-dir "${tmp}/not-a-directory" --check >/dev/null 2>&1; then
	printf '%s\n' 'upload directory that does not exist accepted' >&2
	exit 1
fi
if "${loader}" --usb-spl --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" \
	"${tmp}/u-boot.img" --upload-dir "${tmp}/MLO" --check >/dev/null 2>&1; then
	printf '%s\n' 'upload directory that is a plain file accepted' >&2
	exit 1
fi
"${loader}" --usb-spl --mlo-sha256 "${hash}" --uboot-sha256 "${hash}" "${tmp}/MLO" \
	"${tmp}/u-boot.img" --upload-dir "${tmp}" --check >/dev/null
grep -Fq 'static const char *upload_dir;' "${source}"
grep -Fq 'refusing upload of unsafe file name' "${source}"
grep -Fq 'fit_payload ? "soundtap-ram-nand.itb"' "${source}"
grep -Fq 'capture_payload ? "soundtap-ram-capture.itb"' "${source}"
grep -Fq '#define PAYLOAD_MAX_SIZE (32U * 1024U * 1024U)' "${source}"
grep -Fq 'rejected USB device %04x:%04x on expected path before %04x:%04x matcher' "${source}"
grep -Fq 'install tools/recovery/rom-usb-loader/70-soundtap-rom-usb.rules and reload udev rules' "${source}"
grep -Fq 'TFTP serving complete for u-boot.img; board copy and branch unproven' "${source}"
if grep -Fq 'RAM handoff complete' "${source}"; then
	printf '%s\n' 'live loader log claims RAM handoff completion' >&2
	exit 1
fi
printf '%s\n' 'loader approval tests passed'
