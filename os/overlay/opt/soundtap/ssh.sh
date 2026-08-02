#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# ALPHA only (ADR 0011): root SSH with no password. A production image does not carry
# this file, nor dropbear. -B accepts the blank password field the alpha image ships;
# the host key lives on /mnt/nv because the rootfs is read-only after flash, and ptys
# need devpts mounted or every session dies at login.
LOG=/run/soundtap/log.fifo
[ -p "$LOG" ] || LOG=/tmp/soundtap.log
mkdir -p /dev/pts /mnt/nv/dropbear
mount -t devpts devpts /dev/pts 2>/dev/null || true # already mounted on a restart
# Written aside and moved into place: dropbearkey creates its output before it fills it, so a
# full flash or a power cut left an empty key that `[ -f ]` kept for good and dropbear refused.
if [ ! -s /mnt/nv/dropbear/host_ed25519 ]; then
	rm -f /mnt/nv/dropbear/host_ed25519.new
	dropbearkey -t ed25519 -f /mnt/nv/dropbear/host_ed25519.new >>"$LOG" 2>&1 \
		&& mv /mnt/nv/dropbear/host_ed25519.new /mnt/nv/dropbear/host_ed25519 \
		|| rm -f /mnt/nv/dropbear/host_ed25519.new
fi
if dropbear -B -r /mnt/nv/dropbear/host_ed25519 -p 22 2>>"$LOG"; then
	echo "[soundtap] ssh :22 (root, no password)" >>"$LOG"
else
	echo "[soundtap] ssh failed" >>"$LOG"
fi
