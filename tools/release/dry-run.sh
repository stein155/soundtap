#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# dry-run.sh <package.stu> <commit> [sbom.cdx.json [package.stu.sig]]: every pre-publish
# check of a Core release on one machine, stopping before anything is published.
# Never reads the private key (signing stays tools/release/sign.sh, run by the reviewer) and
# never talks to GitHub. Versions and the public key are read from <commit>; the checks run from this checkout. Exit 0 only when all steps ran
# and passed; a missing SBOM or .sig is reported and exits 3, never counted as a pass.
set -eu
stu=${1:?usage: dry-run.sh <package.stu> <commit> [sbom.cdx.json [package.stu.sig]]}
commit=${2:?usage: dry-run.sh <package.stu> <commit> [sbom.cdx.json [package.stu.sig]]}
sbom=${3:-}
sig=${4:-}
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
incomplete=0
step() { printf '\n== %s\n' "$1"; }

step "version (ADR 0035)"
git -C "$root" rev-parse --verify -q "$commit^{commit}" >/dev/null ||
	{ echo "dry-run.sh: unknown commit $commit" >&2; exit 1; }
version=$(git -C "$root" show "$commit:os/VERSION" | tr -d ' \n')
app=$(git -C "$root" show "$commit:app/VERSION" | tr -d ' \n')
for v in "$version" "$app"; do
	printf '%s\n' "$v" | grep -Eqx '[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}(-alpha\.[1-9][0-9]{0,8})?' ||
		{ echo "dry-run.sh: bad version '$v'" >&2; exit 1; }
done
[ -z "$(git -C "$root" tag -l "os-v$version")" ] ||
	{ echo "dry-run.sh: tag os-v$version exists: raise os/VERSION first" >&2; exit 1; }
echo "SoundTap Core $version, app $app, tag os-v$version is free"
case $version in
*-alpha.*) echo "an alpha (ADR 0035): a GitHub prerelease, signed without the four hardware reports" ;;
esac
echo "package name for the release: SoundTap-Core-$version.stu"

step "CHANGELOG section (RELEASING.md, Changelog)"
python3 -I "$here/changelog.py" check "os-v$version" --commit "$commit"

step "README screenshots (RELEASING.md, Steps)"
# Made by make screenshots from this commit's web UI, or the release would show an older one.
python3 -I "$here/screenshots.py" check "$commit"

step "package structure"
python3 "$root/os/support/stu_check.py" accept "$stu"

step "SBOM against known CVEs"
if [ -n "$sbom" ]; then
	python3 -I "$here/os_cves.py" "$sbom"
else
	echo "SKIPPED: no SBOM given (make legal-info writes SoundTap-Core-$version.cdx.json)"
	incomplete=1
fi

step "release keys (RELEASING.md, Release keys)"
reserve=$(git -C "$root" show "$commit:app/internal/release/release.go" |
	sed -n 's/^const ReservePublicKey = "\(.*\)"$/\1/p')
if [ -n "$reserve" ]; then
	echo "the reserve key is filled in"
else
	echo "INCOMPLETE: release.ReservePublicKey is the empty placeholder; fill it in before a public release"
	incomplete=1
fi

step "signature against release.PublicKey"
if [ -n "$sig" ]; then
	key=$(git -C "$root" show "$commit:app/internal/release/release.go" |
		sed -n 's/^const PublicKey = "\(.*\)"$/\1/p')
	# An ed25519 public key in DER is this fixed prefix plus the 32 raw bytes.
	python3 -I -c 'import base64, sys; sys.stdout.buffer.write(bytes.fromhex("302a300506032b6570032100") + base64.b64decode(sys.argv[1]))' "$key" |
		openssl pkey -pubin -inform DER -out "$tmp/pub.pem"
	# The one statement a Core release may carry (ADR 0048): these bytes, this version,
	# production.
	openssl dgst -sha256 -binary "$stu" >"$tmp/digest"
	hex=$(od -An -v -tx1 "$tmp/digest" | tr -d ' \n')
	expected="soundtap-package v1 kind=core profile=production version=$version sha256=$hex"
	line=$(cat "$sig")
	statement=${line% sig=*}
	[ "$statement" = "$expected" ] ||
		{ echo "dry-run.sh: the .sig states '$statement'; this release needs '$expected'" >&2; exit 1; }
	printf '%s' "${line##* sig=}" | base64 -d >"$tmp/s"
	printf '%s' "$statement" >"$tmp/statement"
	openssl pkeyutl -verify -pubin -inkey "$tmp/pub.pem" -rawin -in "$tmp/statement" -sigfile "$tmp/s"
else
	echo "SKIPPED: no .sig given; the reviewer signs offline with tools/release/sign.sh"
	incomplete=1
fi

step "static update feed (into $tmp/updates)"
mkdir "$tmp/updates"
python3 "$root/tools/release/mock_update_server.py" --serve "$stu" \
	--write-index "$tmp/updates/soundtouch" --host https://github.com \
	--urlpath "stein155/soundtap/releases/download/os-v$version"
python3 -I - "$tmp/updates/soundtouch" "$stu" <<'PY'
import os, sys, xml.etree.ElementTree as ET, zlib
tree = ET.parse(sys.argv[1])
devices = tree.findall("DEVICE")
image = tree.find(".//IMAGE")
assert devices, "feed has no DEVICE"
assert int(image.get("LENGTH")) == os.path.getsize(sys.argv[2]), "feed length differs from the package"
assert int(image.get("CRC"), 16) == zlib.crc32(open(sys.argv[2], "rb").read()), "feed CRC differs"
print(f"feed parses: {len(devices)} devices, {image.get('FILENAME')}, {image.get('LENGTH')} bytes")
PY
# The check the feed PR gets (feed.yml), on the local files: release URL, length, CRC and
# a .sig that verifies with the keys compiled into soundtapd.
if [ -n "$sig" ]; then
	mkdir "$tmp/assets"
	name=$(basename "$stu")
	ln -s "$(cd "$(dirname "$stu")" && pwd)/$name" "$tmp/assets/$name"
	cp "$sig" "$tmp/assets/$name.sig"
	python3 "$here/feed_check.py" "$tmp/updates/soundtouch" --assets "$tmp/assets"
fi

if [ $incomplete -ne 0 ]; then
	printf '\nDRY RUN INCOMPLETE: the skipped steps above are not passes\n'
	exit 3
fi
printf '\nDRY RUN OK: nothing was published\n'
