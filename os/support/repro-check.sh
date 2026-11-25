#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Two builds of one tree, each from an empty output/os, with the same SOURCE_DATE_EPOCH and
# SOUNDTAP_COMMIT, must give the same images and STU (ADR 0011). `make repro-check` runs
# this; reproducible.yml runs that. Two full builds: hours on a hosted runner.
#
#   repro-check.sh REPO
#
# SOURCE_DATE_EPOCH, SOUNDTAP_COMMIT  required; both builds get exactly these
# REPRO_DIR             where the builds are kept, as a/ and b/ (default REPO/output/repro)
# REPRO_STU=0           compare the images only, not the STU `make stu` packs from them
# REPRO_FRESH_CCACHE=1  the second build starts with an empty compiler cache, so a cached
#                       object cannot hide a compile that differs; the cache is put back
set -eu

repo=$(CDPATH= cd -- "${1:?usage: repro-check.sh REPO}" && pwd)
: "${SOURCE_DATE_EPOCH:?set SOURCE_DATE_EPOCH}" "${SOUNDTAP_COMMIT:?set SOUNDTAP_COMMIT}"
out=${REPRO_DIR:-${repo}/output/repro}
compare=${repo}/os/support/repro_compare.py
stu=${repo}/dist/Update-soundtap-buildroot.stu
ccache=${repo}/.cache/buildroot-ccache

build() {
	echo "repro-check: build $1 of ${SOUNDTAP_COMMIT} at SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}"
	# Never a warm tree: an incremental build could keep or hide what a clean one changes.
	rm -rf "${repo}/output/os" "${stu}"
	make -C "${repo}" os SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" SOUNDTAP_COMMIT="${SOUNDTAP_COMMIT}"
	if [ "${REPRO_STU:-1}" = 1 ]; then
		# The dry runs judge the package, not its bytes; Build runs them.
		SOUNDTAP_SKIP_DRY_RUN=1 make -C "${repo}" stu
		python3 "${compare}" save "${repo}/output/os/images" "${out}/$1" --extra "${stu}"
	else
		python3 "${compare}" save "${repo}/output/os/images" "${out}/$1"
	fi
}

build a
if [ "${REPRO_FRESH_CCACHE:-0}" = 1 ] && [ -d "${ccache}" ]; then
	kept=${ccache}.repro-kept
	test ! -e "${kept}" || { echo "repro-check: ${kept} is left from an earlier run; put it back first" >&2; exit 2; }
	mv "${ccache}" "${kept}"
	trap 'rm -rf "$ccache"; mv "$kept" "$ccache"' EXIT
fi
build b
python3 "${compare}" compare "${out}/a" "${out}/b"
