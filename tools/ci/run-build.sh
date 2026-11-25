#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Build SoundTap Core, package it and put it through the install gate: the steps Build
# (build.yml), the OS build of a pull request (build-stu.yml) and a build host or laptop
# run, from this one script (ADR 0011).
#
#   tools/ci/run-build.sh [--reuse] [step ...]      steps in order; default: os stu
#
#   os     make os: kernel, root filesystem and bootloaders in output/os/images
#   stu    make stu, then the candidate in dist/: SoundTap-Core-<os/VERSION>.stu, the preview
#          package SoundTap-Core-<os/VERSION>-preview.stu (ADR 0133), each with its .sha256,
#          the commit and profile it was built from, SHA256SUMS and kernel.config
#   run    install-gate layer 2: the shipped soundtapd from the candidate's own rootfs
#   gate   install-gate layer 1: the candidate into simulated NAND. MODELS picks the models
#          (default: all four, two cells each); SCENARIOS, when set, names the cells instead
#
# --reuse keeps what tools/ci/buildroot-reuse.sh allows of a previous Buildroot tree:
# a build host's warm tree, minutes instead of an hour. CI never passes it: a hosted runner
# starts empty, and a release is built from an empty tree (ADR 0035).
# SOUNDTAP_PROFILE picks the profile (alpha or production) as for make.
# One build per checkout at a time: a second run waits for the first (where flock exists).
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "${ROOT}"
MAKE=${MAKE:-make}
reuse=
if [ "${1:-}" = --reuse ]; then reuse=1; shift; fi
steps=${*:-os stu}

# The cells of docs/contributors/testing/install-gate.md: per model both slots, a clean and a worn chip,
# and on the ST10 and ST20 the power cut.
cells() {
	case $1 in
	st10) echo st10/8/none st10/9/some/cut ;;
	st20) echo st20/8/none st20/9/some/cut ;;
	wla) echo wla/8/some wla/9/none ;;
	st300) echo st300/8/none st300/9/some ;;
	*) echo "unknown model $1; models: st10 st20 wla st300" >&2; return 2 ;;
	esac
}

for step in ${steps}; do
	case ${step} in
	os | stu | run | gate) ;;
	*) echo "unknown step ${step}; steps: os stu run gate" >&2; exit 2 ;;
	esac
done

mkdir -p output dist
if command -v flock >/dev/null 2>&1; then
	exec 9>output/.build.lock
	flock 9
fi

version=$(tr -d ' \n' <os/VERSION)
stu=dist/SoundTap-Core-${version}.stu
preview=dist/SoundTap-Core-${version}-preview.stu
for step in ${steps}; do
	echo "== run-build: ${step}"
	case ${step} in
	os)
		[ -z "${reuse}" ] || sh tools/ci/buildroot-reuse.sh auto
		${MAKE} os
		# Only after a build that worked, so the next run never keeps half a tree.
		[ -z "${reuse}" ] || sh tools/ci/buildroot-reuse.sh record
		;;
	stu)
		${MAKE} stu
		# Named after the version it will be released as (ADR 0035); release.yml publishes
		# exactly this package, and only for the commit it names.
		mv dist/Update-soundtap-buildroot.stu "${stu}"
		mv dist/Update-soundtap-buildroot-preview.stu "${preview}"
		(cd dist && sha256sum "${stu#dist/}" >"${stu#dist/}.sha256")
		(cd dist && sha256sum "${preview#dist/}" >"${preview#dist/}.sha256")
		echo "${SOUNDTAP_COMMIT:-$(git rev-parse HEAD || true)}" >dist/commit
		echo "${SOUNDTAP_PROFILE:-alpha}" >dist/profile
		cp output/os/images/SHA256SUMS output/os/images/kernel.config dist/
		;;
	run)
		sh os/tests/installer/rootfs-run-test.sh "${stu}"
		;;
	gate)
		if [ -z "${SCENARIOS:-}" ]; then
			SCENARIOS=
			for model in ${MODELS:-st10 st20 wla st300}; do
				SCENARIOS="${SCENARIOS} $(cells "${model}")"
			done
		fi
		SCENARIOS=${SCENARIOS} sh os/tests/installer/nandsim-install-test.sh "${stu}"
		;;
	*) ;;
	esac
done
