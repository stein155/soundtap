#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Install a signed STU on a speaker on the bench and prove it came up (layer 3).
#
#   install-candidate.sh <speaker-ip> <file.stu>       (<file.stu>.sig next to it)
#   SOUNDTAP_CONFIRM=downgrade install-candidate.sh ...  (a bench build over a release)
#
# Through the real path: POST /api/firmware?install=1, then POST /api/reboot as the web UI
# does (an install never reboots on its own). Then it checks, with bounded waits only, that
#   - it booted again and answers (a new boot.id in /api/health),
#   - it runs from the other slot,
#   - soundtapd confirmed the boot (reset U-Boot's boot count, so no rollback),
#   - /api/version answers,
# and puts the volume back where it was.
#
# Works on a production image, through the HTTP API only (ADR 0119): the install and the
# reboot each wait for a key held on the speaker for two seconds, and the script says when.
# With root SSH (an alpha image) they carry the one-time token instead and nobody needs to
# be at the speaker. SOUNDTAP_HIL_SERIAL keeps the console; tools/hil/lib.sh lists the
# settings. Signing stays a separate, offline step (tools/release/sign.sh): this script
# never sees the key.
set -eu
ip=$1 stu=$2
[ -s "${stu}.sig" ] || { echo "no signature at ${stu}.sig" >&2; exit 2; }
here=$(dirname "$0")
. "${here}/lib.sh"
hil_init "${ip}"

# Wi-Fi speakers drop the odd connection; three tries before calling the speaker unreachable.
# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
for _ in 1 2 3; do hil_boot && break; boot_id=''; sleep "${hil_poll}"; done
[ -n "${boot_id}" ] || { echo "no running boot in http://${ip}/api/health (an image older than that report needs root ssh)" >&2; exit 2; }
boot0=${boot_id} slot0=${boot_slot}
[ -n "${slot0}" ] || hil_fail "the speaker runs from no NAND slot"
hil_volume_save
# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
echo "before: ${slot0}, volume ${hil_volume0}, $(hil_get /api/version || true)"

code=$(hil_gated "install $(basename "${stu}")" POST "/api/firmware?install=1" -m 900 \
	-H 'Content-Type: application/octet-stream' \
	-H "X-SoundTap-Confirm: ${SOUNDTAP_CONFIRM:-install}" \
	-H "X-SoundTap-Signature: $(cat "${stu}.sig" || true)" \
	--data-binary @"${stu}")
echo; echo "install: HTTP ${code}"
case ${code} in 2??) ;; *) exit 1 ;; esac
# 90 s: the 60 s the speaker waits for the hold, and the answer.
code=$(hil_gated "reboot into the new slot" POST /api/reboot -m 90 \
	-H 'Content-Type: application/json' -d '{"confirm":true}')
echo; echo "reboot: HTTP ${code}"
[ "${code}" = 200 ] || exit 1

# A warm reboot takes 40-75 s on all three models. Waiting longer would let a power cycle
# by hand rescue a stalled reboot and still end in PASS.
t0=$(date +%s)
hil_wait_boot "${boot0}"
# shellcheck disable=SC2312 # hil_since only prints the elapsed seconds
echo "rebooted after $(hil_since "${t0}") s"

# U-Boot counts boots in the RTC's SCRATCH2 and, until an install's slot is confirmed, in the
# environment too (ADR 0010); soundtapd resets both
# a minute into a healthy boot (bootconfirm.go); /api/health says when it has.
hil_wait_confirmed
slot1=${boot_slot}
ver=''
# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
for _ in 1 2 3; do ver=$(hil_get /api/version) && break; ver=''; sleep "${hil_poll}"; done
[ -n "${ver}" ] || hil_fail "/api/version does not answer"
hil_volume_restore
echo "after: ${slot1}, ${ver}"
[ "${slot1}" != "${slot0}" ] || hil_fail "still on ${slot0}"
echo PASS
