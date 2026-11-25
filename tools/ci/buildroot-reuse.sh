#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Decide how much of a previous Buildroot tree this run may keep, and put the tree
# in that state before `make <profile>` runs.
#
# A from-scratch WLA build is 97 minutes, and measured per package (the >>> lines
# in a run log, differenced) that time is mostly not our software:
#
#   linux               23.7 min     host-cmake          15.9 min
#   every other host-*  ~20   min     librespot+snapcast   9.3 min
#
# Over 40 minutes is host tooling that depends on the Buildroot version and the
# defconfig and on nothing we write. ccache is switched on in the defconfig and
# then thrown away with the workspace every run, so it has never once been warm.
#
# What is kept and what is not:
#
#   host/ and build/  kept -- compiled objects, the expensive part
#   target/           always rebuilt from nothing, so no file that a package
#                     stopped installing can survive into an image
#   our own packages  stamps dropped, so a source change is actually picked up.
#                     Buildroot makes the configure step only order-only dependent
#                     on the rsync of a local-site package, so a warm tree would
#                     otherwise build yesterday's code and say nothing about it.
#                     soundtapd guards itself, with a content digest in its .mk;
#                     soundtap-runtime and soundtap-aic3256-loader do not. Rebuilding
#                     all of them costs a few minutes and does not depend on which of
#                     them remembered.
#
# The whole tree is dropped when the toolchain, the Buildroot version or a host-side
# config option changes, because that is the case Buildroot itself says it does not
# support incrementally. A change that only selects or configures a target package is
# not that case, and keeps the host tree.
#
# Warmth is per runner: self-hosted runners share a machine but not a workspace, so the
# workflow asks for a "buildroot" label that only one runner carries, and the tree it
# warms is the tree the next run finds. That runner must carry no other label: CI's
# checkout cleans the workspace, output/ included.
set -eu

MODE=${1:-auto}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
OUT=${ROOT}/output/os
CONFIG=${ROOT}/os/configs/soundtap_defconfig
KEY_FILE=${OUT}/.soundtap-build-key

# Two keys, because the two halves of a build go stale for different reasons.
#
# The host tree -- over 40 of the 97 minutes, host-cmake alone 16 -- depends on the
# Buildroot version, the defconfig and the toolchain, and on nothing else. The target
# side additionally depends on the board directory and our package definitions, so a
# one-line device tree change must not throw away host-cmake too.
host_key() {
	{
		git -C "${ROOT}" rev-parse HEAD:buildroot 2>/dev/null || echo no-submodule
		# The builder image decides which host tools Buildroot builds at all: with
		# cmake in it, host-cmake is never built and BR2_CMAKE points at the image's.
		sha256sum "${ROOT}/os/container/Dockerfile.builder"
		# The external toolchain: a new one is a new compiler for every package.
		sha256sum "${ROOT}/os/configs/soundtap_toolchain_defconfig"
		# Not the whole defconfig: selecting or configuring a TARGET package says
		# nothing about the host tools, which take over 40 minutes to rebuild.
		# Host package selections are kept
		# in the key, and anything a newly selected target package needs on the
		# host gets built on demand -- a host tool left over from a package that
		# was since deselected costs disk, never an image, because target/ is
		# rebuilt from nothing every run.
		grep -vE '^BR2_PACKAGE_|^BR2_TARGET_ROOTFS_' "${CONFIG}"
		grep -E '^BR2_PACKAGE_HOST' "${CONFIG}" || true
	} | sha256sum | cut -d ' ' -f 1
}

# The defconfig gets its own key. A target package whose configuration changed keeps
# a valid .stamp_built, so Buildroot would not rebuild it, and a kept tree would ship
# the package as it was. Host tools are unaffected, so this costs build/, never host/.
config_key() {
	# The production profile merges a fragment over the defconfig (ADR 0011). Alpha
	# keeps the key it always had, so introducing profiles cost no warm tree.
	if [ "${SOUNDTAP_PROFILE:-alpha}" = alpha ]; then
		sha256sum "${CONFIG}" | cut -d ' ' -f 1
	else
		cat "${CONFIG}" "${ROOT}/os/configs/${SOUNDTAP_PROFILE}.fragment" | sha256sum | cut -d ' ' -f 1
	fi
}

target_key() {
	{
		host_key
		# All of os/board: the kernel patches and the BusyBox fragment live in
		# os/board/rootfs, and a patch-only change must rebuild the kernel.
		find "${ROOT}/os/board" "${ROOT}/os/drivers" \
			-type f ! -path '*/rootfs-overlay/*' -exec sha256sum {} + 2>/dev/null | sort
		find "${ROOT}/os/package" -name '*.mk' -o -name 'Config.in' -o -name '*.hash' |
			sort | xargs sha256sum 2>/dev/null
	} | sha256sum | cut -d ' ' -f 1
}

# shellcheck disable=SC2312 # the key is the output; || true would switch off set -e inside the function
build_key() { printf '%s %s %s\n' "$(host_key)" "$(config_key)" "$(target_key)"; }

# Each package under os/package gets its own digest, kept in its build directory.
# A changed .mk keeps a valid .stamp_built just like a changed defconfig does, and the
# target key above only decides whether the kernel goes back to configure.
# Upstream packages we patch (os/board/common/patches/<package>) get the same: a new
# patch for GLib would otherwise never reach a warm tree's libglib2.
pkg_digest() {
	find "$1" -type f -exec sha256sum {} + | sort | sha256sum | cut -d ' ' -f 1
}

# Calls $2 with each build directory of package $1, target and host, and $3, the
# directory that defines the package. $4 globs the version.
each_pkg_build() {
	for dir in "${OUT}/build/$1"-$4 "${OUT}/build/host-$1"-$4; do
		[ -d "${dir}" ] && "$2" "${dir}" "$3"
	done
	return 0
}

record_digest() { pkg_digest "$2" >"$1/.soundtap-pkg-digest"; }

# drops only the changed package, not what links against it. Our packages
# here are leaves (players, bootloaders, the BCO app), and the libraries we patch keep
# their ABI; a library that changed its ABI would need its users dropped too.
drop_if_changed() {
	# shellcheck disable=SC2310,SC2312 # the digest is the output; || true would switch off set -e inside the function
	[ "$(cat "$1/.soundtap-pkg-digest" 2>/dev/null || true)" = "$(pkg_digest "$2")" ] && return 0
	echo "buildroot-reuse: ${2#"${ROOT}"/} changed; rebuilding $(basename "$1")"
	discard "$1"
}

each_soundtap_pkg() {
	for pkg in "${ROOT}"/os/package/*/; do
		each_pkg_build "$(basename "${pkg}")" "$1" "${pkg%/}" '*'
	done
	# A digit after the name: linux's patches must not drop linux-headers.
	for pkg in "${ROOT}"/os/board/common/patches/*/; do
		each_pkg_build "$(basename "${pkg}")" "$1" "${pkg%/}" '[0-9]*'
	done
}

# Rename first, then delete: a cancelled run's make may still be writing into the tree
# when the next run starts on the same runner, and a plain `rm -rf` races the files it
# creates ("Directory not empty"). The rename is atomic
# and gives this run a clean directory whatever the old one is still doing; clearing the
# discarded tree afterwards is best-effort, and a leftover costs disk, not correctness.
discard() {
	[ -e "$1" ] || return 0
	trash=$1.discard.$$
	if mv "$1" "${trash}" 2>/dev/null; then
		rm -rf "${trash}" 2>/dev/null || echo "buildroot-reuse: could not clear ${trash}; left for the next run"
	else
		rm -rf "$1" 2>/dev/null || true
	fi
}

scratch() {
	echo "buildroot-reuse: $1; building from scratch"
	discard "${OUT}"
	# A previous run that could not clear its own discarded tree leaves one behind; this
	# is the moment to try again, before the build needs the disk.
	rm -rf "${OUT}".discard.* 2>/dev/null || true
	exit 0
}

if [ "${MODE}" = record ]; then
	# Only after a build that worked: the key says "a tree in this state came out
	# of this configuration", and recording it for a failed build would let the
	# next run keep half a tree.
	mkdir -p "${OUT}"
	build_key >"${KEY_FILE}"
	each_soundtap_pkg record_digest
	echo "buildroot-reuse: recorded the build key"
	exit 0
fi

[ "${MODE}" = none ] && scratch "reuse disabled"
{ [ -d "${OUT}/build" ] || [ -d "${OUT}/host" ]; } || scratch "no previous tree"

want_host=$(host_key)
want_config=$(config_key)
want_target=$(target_key)
have_host=$(cut -d ' ' -f 1 "${KEY_FILE}" 2>/dev/null || echo none)
have_config=$(cut -d ' ' -f 2 "${KEY_FILE}" 2>/dev/null || echo none)
have_target=$(cut -d ' ' -f 3 "${KEY_FILE}" 2>/dev/null || echo none)
[ "${want_host}" = "${have_host}" ] || scratch "the toolchain or a host-side config option changed"

if [ "${want_config}" != "${have_config}" ]; then
	# Over 40 minutes of host tooling survives this; only what it compiles for the
	# target does not.
	echo "buildroot-reuse: the defconfig changed; keeping the host tree only"
	for d in "${OUT}"/build/*/ "${OUT}/target" "${OUT}/images"; do
		# Host packages keep their stamps: their build directories are the host tree.
		case $(basename "${d}") in host-*) continue ;; *) ;; esac
		discard "${d%/}"
	done
	# These install into images/, which was just discarded.
	rm -f "${OUT}"/build/host-soundtap-bootloaders-*/.stamp_host_installed \
		"${OUT}"/build/host-soundtap-bco-app-*/.stamp_host_installed
	rm -f "${OUT}/.config"
	# What is left is still valid for this toolchain. Saying so keeps a cancelled or
	# failed build from costing the host tree too: with no key the next run would start
	# from scratch.
	printf '%s none none\n' "${want_host}" >"${KEY_FILE}"
	exit 0
fi

board_changed=0
if [ "${want_target}" != "${have_target}" ]; then
	# The board directory or one of our package definitions moved. build/ is kept: most
	# of it cannot be reached by such a change, and the kernel alone is a quarter of a
	# cold build.
	#
	# What board/ can actually reach is the kernel (its device tree, its config
	# fragment, prepare-linux.sh), busybox (its fragment) and the image steps. Nothing
	# in board/ or in os/package/*.mk changes how librespot, snapcast, boost or
	# openssl are built, so those stay compiled. The kernel and busybox go back to
	# their configure step, where kbuild does its own incremental work: an unchanged
	# fragment produces an identical .config and rebuilds nothing.
	echo "buildroot-reuse: the board configuration changed; rebuilding the kernel, busybox and our own packages"
	board_changed=1
	rm -f "${OUT}/.config"
	# Not the whole key: if this build never finishes, the next one redoes this same
	# board step instead of starting from nothing.
	printf '%s %s none\n' "${want_host}" "${want_config}" >"${KEY_FILE}"
fi

# The tree stays. Make the target directory be rebuilt from nothing: install steps
# are copying, not compiling, so this costs little and is what makes a warm tree
# safe to ship from.
rm -rf "${OUT}/target"
if [ "${board_changed}" = 1 ]; then discard "${OUT}/images"; fi
each_soundtap_pkg drop_if_changed
kept=0
resynced=0
for dir in "${OUT}"/build/*/; do
	[ -d "${dir}" ] || continue
	rm -f "${dir}/.stamp_target_installed" "${dir}/.stamp_images_installed"
	kept=$((kept + 1))
	case $(basename "${dir}") in
	linux-* | busybox-*) # linux-* includes linux-headers-*
		# Only when board/ moved: these are the two packages a board directory
		# can reconfigure. Back to the configure step, not to the rsync -- the
		# sources are upstream and unchanged, and kbuild decides what to redo.
		[ "${board_changed}" = 1 ] || continue
		rm -f "${dir}/.stamp_configured" "${dir}/.stamp_built" \
			"${dir}/.stamp_staging_installed" "${dir}/.stamp_host_installed"
		resynced=$((resynced + 1))
		;;
	host-soundtap-bootloaders-* | host-soundtap-bco-app-*)
		# Their install step is a copy into images/, which a board change just discarded;
		# the stamp would keep it from running again and post-image then misses the
		# bootloader images (buildroot-nand run 36774108811) or post-build the BCO app
		# (run 39944981315). The build stays.
		[ "${board_changed}" = 1 ] || continue
		rm -f "${dir}/.stamp_host_installed"
		;;
	soundtapd-* | soundtap-runtime-* | soundtap-aic3256-loader-* | soundtap-wl18xx-conf-*)
		# Built from this repository. Drop everything back to the rsync so the
		# source is copied in again and actually recompiled.
		rm -f "${dir}/.stamp_rsynced" "${dir}/.stamp_configured" "${dir}/.stamp_built" \
			"${dir}/.stamp_staging_installed" "${dir}/.stamp_host_installed"
		resynced=$((resynced + 1))
		;;
	*) ;;
	esac
done
rm -f "${OUT}"/.stamp_target_finalized "${OUT}"/.stamp_rootfs_* "${OUT}"/.stamp_images_installed
echo "buildroot-reuse: keeping ${kept} built packages, rebuilding ${resynced} of our own, target/ from nothing"
