#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# shellcheck disable=SC2154 # hil_* and boot_* are set by lib.sh, sourced below.
# Prove the boot-count rollback on a bench speaker (layer 3; ADR 0010).
#
#   rollback-check.sh <speaker-ip>
#
# Makes the running slot fail to confirm its boot and checks that U-Boot falls back to the
# other slot, that soundtapd there selects it, and that it stays selected on the next
# reboot. Past bootlimit (4) U-Boot runs altbootcmd. Every model on our boot chain counts
# boots. The speaker ends on the other slot, so install the candidate twice first
# (tools/hil/install-candidate.sh) if both slots should hold it. Bounded waits only; a
# reboot not back in 300 s fails.
#
# How a boot of the slot under test is kept from confirming depends on the image:
#   - alpha (root SSH): soundtapd is frozen (SIGSTOP) before its one-minute confirm
#     (bootconfirm.go), so the kernel watchdog resets the board warm, as a hang would.
#   - production (no SSH, ADR 0119): the script asks for a reboot through /api/reboot as
#     soon as the boot answers, before soundtapd confirms it a minute after it started, and
#     prompts for the key held on the speaker that the reboot needs. Hold it at once: a
#     boot that confirms first resets the count. Init keeps the reset warm while U-Boot
#     counts, so the count survives the reboot. A power cut does not: a cold reset clears
#     the count, so a power cut never leads to a rollback.
# SOUNDTAP_HIL_SERIAL keeps the console (tools/hil/lib.sh).
set -eu
ip=$1
here=$(dirname "$0")
. "$here/lib.sh"
hil_init "$ip"

# Alpha: the boot is read over SSH, as soundtapd is frozen and its API silent.
sp() { hil_ssh_run "$1"; }
ssh_slot() { sp 'grep -o "ubi.mtd=[89]" /proc/cmdline | head -n 1'; }
ssh_bootid() { sp 'cat /proc/sys/kernel/random/boot_id' || true; }
ssh_confirmed() { sp 'cat /tmp/soundtap.log* | grep -q "msg=\"boot confirmed\""'; }
# The next boot_id after $1, within the timeout.
ssh_next_boot() {
	t0=$(date +%s) b=$1
	while [ -z "$b" ] || [ "$b" = "$1" ]; do
		[ "$(hil_since "$t0")" -lt "$hil_timeout" ] || hil_fail "not back $hil_timeout s after a reset"
		sleep "$hil_poll"; b=$(ssh_bootid)
	done
	echo "$b"
}

reboot_speaker() {
	code=$(hil_gated "$1" POST /api/reboot -m 90 -H 'Content-Type: application/json' -d '{"confirm":true}')
	echo
	[ "$code" = 200 ] || hil_fail "reboot: HTTP $code"
}

if [ "$hil_ssh" = 1 ]; then
	boot=$(ssh_bootid) bad=$(ssh_slot)
	[ -n "$boot" ] && [ -n "$bad" ] || { echo "no root ssh to $ip" >&2; exit 2; }
	echo "slot under test: $bad"
	reboot_speaker "reboot into the slot under test"
	# bootlimit is 4, so at most five boots land on the slot under test before altbootcmd.
	n=0
	while :; do
		boot=$(ssh_next_boot "$boot")
		[ "$(ssh_slot)" = "$bad" ] || break
		n=$((n + 1))
		[ "$n" -le 6 ] || hil_fail "still on $bad after $n unconfirmed boots"
		# soundtapd confirms a minute after it starts; ssh answers well before that.
		t0=$(date +%s)
		until sp 'kill -STOP $(pidof soundtapd)'; do
			[ "$(hil_since "$t0")" -lt 40 ] || hil_fail "could not freeze soundtapd in time"
			sleep 2
		done
		! ssh_confirmed || hil_fail "boot $n on $bad confirmed before the freeze"
		echo "boot $n on $bad: soundtapd frozen, waiting for the watchdog"
	done
	good=$(ssh_slot)
	echo "fell back to $good after $n unconfirmed boots"
	t0=$(date +%s)
	until ssh_confirmed; do
		[ "$(hil_since "$t0")" -lt "$hil_timeout" ] || hil_fail "fallback boot not confirmed"
		sleep "$hil_poll"
	done
	sp 'cat /tmp/soundtap.log* | grep -q "booted by fallback"' || hil_fail "soundtapd did not see the fallback"
	# The selection must survive a normal reboot.
	reboot_speaker "a normal reboot, which must stay on $good"
	boot=$(ssh_next_boot "$boot")
	[ "$(ssh_slot)" = "$good" ] || hil_fail "the next reboot went back to $bad"
	echo "PASS: $bad -> $good, kept after a reboot"
	exit 0
fi

hil_boot || { echo "no running boot in http://$ip/api/health (an image older than that report needs root ssh)" >&2; exit 2; }
bad=$boot_slot
[ -n "$bad" ] || hil_fail "the speaker runs from no NAND slot"
echo "slot under test: $bad"
reboot_speaker "reboot into the slot under test"
n=0
while :; do
	hil_wait_boot "$boot_id"
	[ "$boot_slot" = "$bad" ] || break
	n=$((n + 1))
	[ "$n" -le 6 ] || hil_fail "still on $bad after $n unconfirmed boots"
	[ "$boot_confirmed" = false ] || hil_fail "boot $n on $bad confirmed before the script saw it"
	echo "boot $n on $bad: rebooting it before soundtapd confirms it"
	this=$boot_id
	reboot_speaker "reboot $n before the boot confirm: hold at once"
	# The reboot follows its answer by a second; a boot that confirmed meanwhile reset the
	# count, and the fallback would come late or not at all.
	if hil_boot && [ "$boot_id" = "$this" ] && [ "$boot_confirmed" = true ]; then
		hil_fail "boot $n on $bad confirmed before it rebooted: hold the key sooner"
	fi
	boot_id=$this
done
good=$boot_slot
echo "fell back to $good after $n unconfirmed boots"
hil_wait_confirmed
# soundtapd selects the slot it fell back to when it confirms; a normal reboot shows it did.
reboot_speaker "a normal reboot, which must stay on $good"
hil_wait_boot "$boot_id"
[ "$boot_slot" = "$good" ] || hil_fail "the next reboot went back to $bad"
echo "PASS: $bad -> $good, kept after a reboot"
