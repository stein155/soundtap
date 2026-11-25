#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# sign.sh <asset>...: writes <asset>.sig, the signed package statement soundtapd checks
# (ADR 0048), one line:
#
#   soundtap-package v1 kind=<core|app> profile=<production|alpha> version=<X.Y.Z|X.Y.Z-alpha.N|stock> sha256=<hex> sig=<base64>
#
# sig is the ed25519 signature over everything before " sig=". The key is the PEM file
# SOUNDTAP_RELEASE_KEY_FILE (default ~/.soundtap-release-key.pem), and is refused unless its
# public half is the one compiled into soundtapd. For a release, .github/workflows/sign.yml
# writes it from the release environment's secret and removes it afterwards (ADR 0035).
#
# An owner's own build needs no signature: it installs from an upload after the press on
# the speaker (ADR 0048, RELEASING.md "Installing your own builds").
#
# A production signature needs a key under a passphrase: a key that opens without one is
# refused (RELEASING.md). openssl asks for the passphrase, or reads it as
# SOUNDTAP_RELEASE_KEY_PASSIN says, in openssl's -passin form (env:VAR, file:path, fd:N).
# An alpha (bench) signature may use a key without one.
#
# kind: SOUNDTAP_PACKAGE_KIND, by default app for an ELF binary and core for anything else.
# A speaker installs a package only through the route of its kind.
# version: SOUNDTAP_PACKAGE_VERSION, by default os/VERSION for core and app/VERSION for an
# app; sign-release.sh takes it from the tag. Sign a Bose package for the way back to
# stock with SOUNDTAP_PACKAGE_VERSION=stock (core only).
# profile: SOUNDTAP_SIGN_PROFILE, alpha unless it says production (sign-release.sh sets
# it). An upload takes either as signed, on any image; a route that fetches a release from
# GitHub takes only production (ADR 0048).
set -eu
key=${SOUNDTAP_RELEASE_KEY_FILE:-${HOME}/.soundtap-release-key.pem}
[ -r "${key}" ] || { echo "sign.sh: no readable key at ${key}" >&2; exit 1; }
here=$(dirname "$0")
profile=${SOUNDTAP_SIGN_PROFILE:-alpha}
case ${profile} in
production | alpha) ;;
*) echo "sign.sh: SOUNDTAP_SIGN_PROFILE is alpha or production" >&2; exit 1 ;;
esac
case ${SOUNDTAP_PACKAGE_KIND:-} in
'' | core | app) ;;
*) echo "sign.sh: SOUNDTAP_PACKAGE_KIND is core or app" >&2; exit 1 ;;
esac
# describe <asset> prints "<kind> <version>" for it, or fails with the reason.
describe() {
	k=${SOUNDTAP_PACKAGE_KIND:-}
	if [ -z "${k}" ]; then
		k=core
		[ "$(head -c 4 "$1" | od -An -tx1 | tr -d ' \n' || true)" = 7f454c46 ] && k=app
	fi
	if [ "${k}" = app ]; then
		v=${SOUNDTAP_PACKAGE_VERSION:-$(tr -d ' \n' <"${here}/../../app/VERSION")}
	else
		v=${SOUNDTAP_PACKAGE_VERSION:-$(tr -d ' \n' <"${here}/../../os/VERSION")}
	fi
	if [ "${v}" = stock ]; then
		[ "${k}" = core ] || { echo "sign.sh: only a core package is signed as stock" >&2; return 1; }
	elif ! printf '%s\n' "${v}" | grep -Eqx '[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}(-alpha\.[1-9][0-9]{0,8})?'; then
		echo "sign.sh: bad version '${v}'" >&2
		return 1
	fi
	echo "${k} ${v}"
}
# Every asset is checked before the key is touched or anything is signed.
for asset in "$@"; do
	[ -r "${asset}" ] || { echo "sign.sh: cannot read ${asset}" >&2; exit 1; }
	describe "${asset}" >/dev/null
done
digest=$(mktemp)
raw=$(mktemp)
statement=$(mktemp)
trap 'rm -f "$digest" "$raw" "$statement"' EXIT
# An empty passphrase opens only a key that has none.
if [ "${profile}" = production ] && openssl pkey -in "${key}" -passin pass: -noout </dev/null >/dev/null 2>&1; then
	echo "sign.sh: ${key} has no passphrase; a production signature needs an encrypted key" \
		"(openssl pkey -in <key> -aes256 -out <new key>)" >&2
	exit 1
fi
want=$(sed -n 's/^const PublicKey = "\(.*\)"$/\1/p' "${here}/../../app/internal/release/release.go")
got=$(openssl pkey -in "${key}" ${SOUNDTAP_RELEASE_KEY_PASSIN:+-passin "${SOUNDTAP_RELEASE_KEY_PASSIN}"} -pubout -outform DER | tail -c 32 | base64)
[ -n "${want}" ] && [ "${got}" = "${want}" ] ||
	{ echo "sign.sh: ${key} does not match release.PublicKey" >&2; exit 1; }
for asset in "$@"; do
	described=$(describe "${asset}")
	kind=${described% *} version=${described#* }
	openssl dgst -sha256 -binary "${asset}" >"${digest}"
	hex=$(od -An -v -tx1 "${digest}" | tr -d ' \n')
	[ "${#hex}" -eq 64 ] || { echo "sign.sh: bad digest for ${asset}" >&2; exit 1; }
	printf 'soundtap-package v1 kind=%s profile=%s version=%s sha256=%s' \
		"${kind}" "${profile}" "${version}" "${hex}" >"${statement}"
	# Each step on its own: upstream of a pipe a failed signer would leave an empty .sig and
	# a "signed" line. An ed25519 signature is exactly 64 bytes.
	openssl pkeyutl -sign -inkey "${key}" ${SOUNDTAP_RELEASE_KEY_PASSIN:+-passin "${SOUNDTAP_RELEASE_KEY_PASSIN}"} \
		-rawin -in "${statement}" -out "${raw}"
	[ "$(wc -c <"${raw}" || true)" -eq 64 ] || { echo "sign.sh: bad signature for ${asset}" >&2; exit 1; }
	printf '%s sig=%s\n' "$(cat "${statement}" || true)" "$(base64 <"${raw}" | tr -d '\n' || true)" >"${asset}.sig"
	echo "signed ${asset}: ${kind} ${profile} ${version}"
done
