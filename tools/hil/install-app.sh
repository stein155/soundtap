#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Replace just soundtapd on a speaker on the bench, without a Core install or a reboot.
#
#   install-app.sh <speaker-ip> <soundtapd>       (<soundtapd>.sig next to it)
#
# <soundtapd> is `make app`'s output/app/soundtapd. It goes through the real path,
# POST /api/soundtapd, so it runs at once and from /mnt/nv at every boot after, until the
# next STU install. Then it checks, with bounded waits only, that /api/version reports the
# new build, and puts the volume back where it was.
#
# Works on a production image through the HTTP API (ADR 0119): the upload waits for a key
# held on the speaker for two seconds, and the script says when. With root SSH (an alpha
# image) it carries the one-time token instead. Signing stays a separate, offline step
# (tools/release/sign.sh): this script never sees the key.
set -eu
ip=$1 bin=$2
[ -s "${bin}.sig" ] || { echo "no signature at ${bin}.sig" >&2; exit 2; }
here=$(dirname "$0")
. "${here}/lib.sh"
hil_init "${ip}"

ver0=''
# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
for _ in 1 2 3; do ver0=$(hil_get /api/version) && break; ver0=''; sleep "${hil_poll}"; done
[ -n "${ver0}" ] || { echo "no answer from http://${ip}/api/version" >&2; exit 2; }
hil_volume_save
echo "before: ${ver0}"

code=$(hil_gated "install this soundtapd" POST /api/soundtapd -m 300 \
	-H 'Content-Type: application/octet-stream' \
	-H 'X-SoundTap-Confirm: soundtapd' -H "X-SoundTap-Signature: $(cat "${bin}.sig" || true)" \
	--data-binary @"${bin}")
echo; echo "upload: HTTP ${code}"
case ${code} in 2??) ;; *) exit 1 ;; esac

# The old process ends a second after answering; the new one answers within seconds.
t0=$(date +%s)
# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
until ver=$(hil_get /api/version) && [ -n "${ver}" ] && [ "${ver}" != "${ver0}" ]; do
	# shellcheck disable=SC2310,SC2312 # hil_since only prints the elapsed seconds
	[ "$(hil_since "${t0}")" -lt 120 ] || hil_fail "no new /api/version 120 s after the upload"
	sleep "${hil_poll}"
done
hil_volume_restore
echo "after: ${ver}"
echo PASS
