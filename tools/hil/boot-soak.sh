#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Boot and install soak on a bench speaker, without sound (layer 3).
#
#   boot-soak.sh <speaker-ip> <file.stu> [installs] [reboots] [cold boots]
#                                         (<file.stu>.sig next to it; with 0 installs any
#                                          placeholder will do for <file.stu>)
#
# Installs the candidate <installs> times (default 5) through install-candidate.sh, which
# times the first warm reboot after each install: the case that once stalled before init
# on the ST10 and the Wireless Link. Then <reboots> warm reboots (default 3) through
# /api/reboot, and <cold boots> power cuts (default 0), each one a step the script asks a
# person for: cutting the power is not something the speaker can be asked to do. For every
# boot it prints one line: seconds until the API answers from the new boot, until the boot
# confirm, the slot, and how the boot before it ended (boot.previous in /api/health:
# reboot, power loss, a watchdog reset...).
#
# On a production image each install and reboot waits for a key held on the speaker, so a
# person stays with it; on an alpha image (root SSH) the one-time token stands in and the
# run is unattended. SOUNDTAP_HIL_SERIAL keeps the console (tools/hil/lib.sh). Bounded
# waits only: a boot not back in 300 s fails the run.
set -eu
ip=$1 stu=$2 installs=${3:-5} reboots=${4:-3} colds=${5:-0}
here=$(dirname "$0")
. "${here}/lib.sh"
hil_init "${ip}"

# measure <start> <boot it replaces>: one boot, from the moment it was asked for.
measure() {
	hil_wait_boot "$2"
	up=$(hil_since "$1")
	hil_wait_confirmed
	# shellcheck disable=SC2312 # hil_since only prints the elapsed seconds
	echo "up ${up}s confirmed $(hil_since "$1")s ${boot_slot} previous=${boot_previous:-unknown}"
}

n=1
out=$(mktemp)
trap 'rm -f "$out"; kill "${hil_console_pid:-}" 2>/dev/null || true' EXIT
while [ "${n}" -le "${installs}" ]; do
	t0=$(date +%s)
	# The prompts for the hold go to stderr and stay on screen; the report is read from
	# stdout. The console stays with this script: two readers would split its lines.
	SOUNDTAP_HIL_SERIAL='' sh "${here}/install-candidate.sh" "${ip}" "${stu}" >"${out}" || {
		tail -n 5 "${out}" >&2
		hil_fail "install ${n}"
	}
	# shellcheck disable=SC2312 # hil_since only prints the elapsed seconds
	echo "install ${n}: $(grep -o 'rebooted after [0-9]* s' "${out}" || true) total $(hil_since "${t0}") s, $(grep '^after:' "${out}" || true)"
	n=$((n + 1))
done

n=1
while [ "${n}" -le "${reboots}" ]; do
	# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
	hil_boot || hil_fail "no answer from http://${ip}/api/health"
	old=${boot_id}
	t0=$(date +%s)
	code=$(hil_gated "warm reboot ${n} of ${reboots}" POST /api/reboot -m 90 \
		-H 'Content-Type: application/json' -d '{"confirm":true}')
	[ "${code}" = 200 ] || hil_fail "reboot ${n}: HTTP ${code}"
	# measure runs in a subshell: its failure has to end this script too.
	# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
	m=$(measure "${t0}" "${old}") || exit 1
	echo "warm reboot ${n}: ${m}"
	n=$((n + 1))
done

n=1
while [ "${n}" -le "${colds}" ]; do
	# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
	hil_boot || hil_fail "no answer from http://${ip}/api/health"
	old=${boot_id}
	hil_manual "Cold boot ${n} of ${colds}: cut the speaker's power and wait 10 s. Press Enter as you switch it on again."
	t0=$(date +%s)
	# shellcheck disable=SC2310 # a probe of the speaker: its failure is handled right here
	m=$(measure "${t0}" "${old}") || exit 1
	echo "cold boot ${n}: ${m}"
	n=$((n + 1))
done
echo PASS
