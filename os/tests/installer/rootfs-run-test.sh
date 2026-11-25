#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Run the soundtapd a package ships, from the package's own root filesystem, and prove it
# serves. This is layer 2 of the install gate (docs/contributors/testing/install-gate.md).
#
# Layer 1 proves the installer leaves the image's bytes in the slot; it runs a soundtapd
# built for the runner. What a speaker then runs is the armv7 binary inside the rootfs,
# which nothing else executes before a speaker does. This takes the rootfs volume out of
# the package (the same reader stu_expect.py uses), unpacks the UBIFS, and runs that
# binary chrooted into that tree under qemu-user, so it reads the image's own /etc.
#
# It proves: the shipped binary starts on ARMv7, /api/version answers with the OS version
# the rootfs carries, and the web UI it embeds loads with every asset index.html names.
# It does not boot the kernel or run init: QEMU has no AM335x machine, so the kernel, the
# device tree and the init scripts stay with layer 3 (the bench speakers).
#
# Usage: os/tests/installer/rootfs-run-test.sh <package.stu>
set -eu

if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then echo "usage: rootfs-run-test.sh <package.stu>" >&2; exit 1; fi
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
PKG=$(CDPATH= cd -- "$(dirname -- "$1")" && pwd || true)/$(basename -- "$1")
RUNNER_IMAGE=ubuntu:24.04@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac
# The same frozen archive as layer 1, so qemu-user is the same bytes on every run.
APT_SNAPSHOT=20261020T000000Z
UBI_READER=0.8.16

SETUP=$(cat <<'SETUP'
set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get -qq update >/dev/null
apt-get -qq install -y --no-install-recommends ca-certificates >/dev/null
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
cat >/etc/apt/sources.list.d/snapshot.sources <<EOF
Types: deb
URIs: https://snapshot.ubuntu.com/ubuntu/$APT_SNAPSHOT/
Suites: noble noble-updates
Components: main universe
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
EOF
rm -rf /var/lib/apt/lists/*
apt-get -qq update >/dev/null
apt-get -qq install -y --no-install-recommends qemu-user-static python3-pip wget >/dev/null
pip3 install -q --break-system-packages "ubi_reader==$UBI_READER"
apt-get clean
rm -rf /var/lib/apt/lists/*
SETUP
)
# Built once per setup and reused, as layer 1 does: the snapshot can be slow to fetch.
IMAGE=soundtap-rootfs-run:${APT_SNAPSHOT}-$(printf '%s%s' "${SETUP}" "${UBI_READER}" | sha256sum | cut -c1-12)
if ! docker image inspect "${IMAGE}" >/dev/null 2>&1; then
  c=soundtap-rootfs-run-setup-$$
  printf '%s\n' "${SETUP}" | docker run -i --name "${c}" -e APT_SNAPSHOT="${APT_SNAPSHOT}" \
    -e UBI_READER="${UBI_READER}" "${RUNNER_IMAGE}" sh -s || { docker rm -f "${c}" >/dev/null; exit 1; }
  docker commit "${c}" "${IMAGE}" >/dev/null
  docker rm "${c}" >/dev/null
fi

# Everything is unpacked inside the container, so no root-owned files land on the runner.
docker run --rm -i -v "${PKG}":/pkg.stu:ro -v "${REPO}/os/support":/repo/os/support:ro \
  -v "${REPO}/os/board":/repo/os/board:ro "${IMAGE}" sh -s <<'RUN'
set -eu
fail() { echo "FAIL: $*" >&2; exit 1; }
python3 -I - <<'PY'
import sys
sys.path.insert(0, "/repo/os/support")
import stu_check, stu_expect
from pathlib import Path
data = Path("/pkg.stu").read_bytes()
start, size = stu_expect.pick(stu_check.modules(data), "soundtap-rootfs", "ubi.img")
body = size if size % stu_check.UBI_PEB == 0 else size - stu_check.OVERLAP
Path("/vol.img").write_bytes(stu_expect.ubi_volume(data[start:start + body]))
PY
ubireader_extract_files -k -o /x /vol.img >/dev/null
test -x /x/usr/bin/soundtapd || fail "rootfs has no /usr/bin/soundtapd"
want_os=$(cat /x/etc/soundtap/os-version) || fail "rootfs has no /etc/soundtap/os-version"
want_commit=$(cat /x/etc/soundtap/os-commit) || fail "rootfs has no /etc/soundtap/os-commit"
cp /usr/bin/qemu-arm-static /x/qemu-arm
mkdir -p /x/tmp/home
chroot /x /qemu-arm /usr/bin/soundtapd -no-audio -listen 127.0.0.1:8080 -home /tmp/home \
  >/soundtapd.log 2>&1 &
pid=$!
version=
for _ in $(seq 1 60); do
  version=$(wget -qO- http://127.0.0.1:8080/api/version 2>/dev/null) && break
  kill -0 "$pid" 2>/dev/null || { cat /soundtapd.log; fail "soundtapd exited"; }
  sleep 1
done
test -n "$version" || { cat /soundtapd.log; fail "no /api/version within 60 s"; }
echo "version: $version"
case $version in *"\"os\":\"$want_os\""*) ;; *) fail "/api/version does not report os $want_os";; esac
case $version in *"\"commit\":\"$want_commit\""*) ;; *) fail "/api/version does not report commit $want_commit";; esac
wget -qO /index.html http://127.0.0.1:8080/ || fail "web UI does not load"
grep -q '<div id="root">' /index.html || fail "index.html has no app root"
assets=$(grep -o '/assets/[^"]*' /index.html | sort -u)
test -n "$assets" || fail "index.html names no assets"
for a in $assets; do
  wget -q -O /dev/null "http://127.0.0.1:8080$a" || fail "asset $a does not load"
done
echo "assets: $(echo "$assets" | wc -l) loaded"
kill "$pid"
echo "ROOTFS RUN TEST PASSED"
RUN
