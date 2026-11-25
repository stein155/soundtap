#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# sign-release.sh <tag> [reports-dir]: the signing half of a release (ADR 0035), which
# .github/workflows/sign.yml runs in the release environment with the release key in
# SOUNDTAP_RELEASE_KEY_FILE. release.yml publishes a draft with the asset and its .sha256; this
# downloads it, checks the digest, signs it with that key (sign.sh) and uploads <asset>.sig.
# The release stays a draft: the maintainer installs the signed asset on their own speakers
# first (RELEASING.md, "Steps").
#
# sign-release.sh --publish <tag>: makes that signed draft public, once the own speakers took
# it. An OS release becomes "latest"; an app release never does (ADR 0035). An alpha, a tag
# whose version is X.Y.Z-alpha.N, is published as a GitHub prerelease and never as latest.
#
# An OS release is one STU for every model, so it is signed only when reports-dir holds
# passing hardware-{st10,st20,wla,st300}.json reports made against these exact bytes
# (release_approval.py); sign.yml takes them from releases/<tag>/ on main. An alpha is signed without them, and says so; reports given for an
# alpha are still checked.
#
# An OS release also carries the preview package (ADR 0133), <asset minus .stu>-preview.stu
# with its own .sha256: the same payloads with the preview build of the installer as module 0,
# the only package a Preview board installs from. It is signed with the same statement fields
# (core, production, the tag's version) over its own digest, only when stu_check.py finds
# every byte after module 0 equal to the main asset's, whose reports cover those payloads.
# --publish requires both statements.
set -eu
publish=
if [ "${1:-}" = --publish ]; then
	publish=1
	shift
fi
tag=${1:?usage: sign-release.sh [--publish] <tag> [reports-dir]}
reports=${2:-}
here=$(cd "$(dirname "$0")" && pwd)
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT

# The statement in the .sig names the kind and the version from the tag, so a speaker can
# tell an older release from a newer one and an app from a Core package (ADR 0048).
case ${tag} in
os-v*) kind=core version=${tag#os-v} latest=--latest ;;
app-v*) kind=app version=${tag#app-v} latest=--latest=false ;;
*) echo "sign-release.sh: ${tag} is neither os-v* nor app-v*" >&2; exit 1 ;;
esac
# An alpha (ADR 0035): a GitHub prerelease, never latest. sign.sh checks the version itself.
alpha=
pre=--prerelease=false
case ${version} in
*-alpha.*) alpha=1 pre=--prerelease latest=--latest=false ;;
*) ;;
esac
[ "$(gh release view "${tag}" --json isDraft -q .isDraft || true)" = true ] ||
	{ echo "sign-release.sh: ${tag} is not a draft" >&2; exit 1; }
gh release download "${tag}" -D "${dir}"
# shellcheck disable=SC2012 # release asset names are plain file names
assets=$(cd "${dir}" && ls ./*.sha256 | sed 's|^\./||; s|\.sha256$||')
asset=$(printf '%s\n' "${assets}" | grep -v -e '-preview\.stu$' || true)
[ -n "${asset}" ] && [ "$(echo "${asset}" | wc -l || true)" -eq 1 ] ||
	{ echo "sign-release.sh: expected exactly one asset with a .sha256 in ${tag}" >&2; exit 1; }
# The preview package (ADR 0133): on an OS release exactly the main asset's -preview twin,
# nothing on an app release.
preview=
case ${kind} in
core)
	preview=${asset%.stu}-preview.stu
	{ [ "$(printf '%s\n' "${assets}" | grep -c -e '-preview\.stu$' || true)" -eq 1 ] &&
		printf '%s\n' "${assets}" | grep -qxF "${preview}"; } ||
		{ echo "sign-release.sh: ${tag} needs the preview package ${preview} with its .sha256, and no other" >&2; exit 1; }
	;;
*)
	[ "$(echo "${assets}" | wc -l || true)" -eq 1 ] ||
		{ echo "sign-release.sh: expected exactly one asset with a .sha256 in ${tag}" >&2; exit 1; }
	;;
esac
(cd "${dir}" && sha256sum -c "${asset}.sha256")
[ -z "${preview}" ] || (cd "${dir}" && sha256sum -c "${preview}.sha256")

if [ -n "${publish}" ]; then
	# Publish only the draft that carries this release's own statements: the bytes the
	# maintainer's speakers took, signed as this tag, the preview package's included.
	for a in "${asset}" ${preview}; do
		[ -s "${dir}/${a}.sig" ] ||
			{ echo "sign-release.sh: ${tag} has no ${a}.sig: sign it first" >&2; exit 1; }
		hex=$(sha256sum "${dir}/${a}" | cut -d ' ' -f 1)
		want="soundtap-package v1 kind=${kind} profile=production version=${version} sha256=${hex} sig="
		case $(head -n 1 "${dir}/${a}.sig") in
		"${want}"*) ;;
		*) echo "sign-release.sh: ${a}.sig does not state ${kind} ${version} production for these bytes" >&2; exit 1 ;;
		esac
	done
	gh release edit "${tag}" --draft=false "${pre}" "${latest}"
	if [ -n "${alpha}" ]; then
		echo "published ${tag} as a prerelease, not latest"
	else
		echo "published ${tag}"
	fi
	exit 0
fi

case ${tag} in
os-v*)
	if [ -n "${alpha}" ] && [ -z "${reports}" ]; then
		echo "sign-release.sh: ${tag} is an alpha: signing it WITHOUT the four hardware reports;" \
			"it is published as a prerelease, never latest (ADR 0035)" >&2
	else
		[ -n "${reports}" ] ||
			{ echo "sign-release.sh: an OS release needs the hardware reports directory" >&2; exit 1; }
		python3 "${here}/release_approval.py" --asset "${dir}/${asset}" --reports "${reports}"
	fi
	;;
*) ;;
esac
if [ -n "${preview}" ]; then
	python3 "${here}/../../os/support/stu_check.py" same-payloads "${dir}/${asset}" "${dir}/${preview}" ||
		{ echo "sign-release.sh: ${preview} is not ${asset} with only its installer swapped" >&2; exit 1; }
fi
rm -f "${dir}/${asset}.sig" ${preview:+"${dir}/${preview}.sig"}
SOUNDTAP_SIGN_PROFILE=production SOUNDTAP_PACKAGE_KIND=${kind} SOUNDTAP_PACKAGE_VERSION=${version} \
	"${here}/sign.sh" "${dir}/${asset}" ${preview:+"${dir}/${preview}"}
gh release upload --clobber "${tag}" "${dir}/${asset}.sig" ${preview:+"${dir}/${preview}.sig"}
echo "signed ${tag}${alpha:+ as an alpha}, still a draft: install it on your own speakers, then: sign-release.sh --publish ${tag}"
