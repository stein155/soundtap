#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

set -eu

target_dir=$1
: "${BINARIES_DIR:?Buildroot must export BINARIES_DIR}"

# Buildroot installs /bin/busybox setuid root unconditionally. Every process on the speaker
# already runs as root, so the bit only matters to an attacker. This runs after the
# device table, which is why it is here and not in post-build.sh.
chmod 0755 "$target_dir/bin/busybox"
# dbus installs its activation helper setuid root. init starts every bus service itself, so
# nothing is ever bus-activated and the helper is only attack surface.
rm -f "$target_dir/usr/libexec/dbus-daemon-launch-helper"

# Only here, inside fakeroot, are the device-table modes visible; output/target on disk
# never carries them, so a check there would pass anything.
setid=$(find "$target_dir" -xdev \( -perm -4000 -o -perm -2000 \) -type f)
if [ -n "$setid" ]; then
	echo "ROOTFS REJECT: setuid/setgid files in the image:" >&2
	echo "$setid" >&2
	exit 1
fi

mkdir -p "$BINARIES_DIR/rootfs-tree"
rsync -a --delete "$target_dir/" "$BINARIES_DIR/rootfs-tree/"
