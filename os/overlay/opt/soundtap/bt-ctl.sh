#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# AVRCP transport control for the phone currently connected over A2DP.
#
# bluealsa only carries audio; skipping a track is a control command to the phone,
# which bluez exposes as org.bluez.MediaPlayer1. bluetoothctl already speaks that,
# so this is a shell around its player menu rather than a second D-Bus client in
# soundtapd. Commands are fed on stdin instead of the dotted `player.next` form:
# the submenu is stable across bluez versions, the shorthand is not.
#
# Silent by design when nothing is connected — bluez answers "No default player
# available" and there is nothing for the user to do about it.
# The FIFO init.d/soundtap drains into syslogd; the file when there is no syslogd.
LOG=/run/soundtap/log.fifo
[ -p "${LOG}" ] || LOG=/tmp/soundtap.log

BT_STATE=${BT_STATE:-/var/lib/bluetooth}

case "${1:-}" in
	next) cmd=next ;;
	prev) cmd=previous ;;
	play) cmd=play ;;
	pause) cmd=pause ;;
	reconnect) cmd=reconnect ;;
	disconnect) cmd=disconnect ;;
	trim) cmd=trim ;;
	*) echo "usage: bt-ctl.sh next|prev|play|pause|reconnect|disconnect|trim" >&2; exit 2 ;;
esac

command -v bluetoothctl >/dev/null 2>&1 || exit 1

# reconnect — Bluetooth was selected on the speaker: call the trusted phones instead of
# waiting for one to find us. Stock does the same, most recent first. BlueZ keeps no
# last-connected time; its per-device info file is rewritten as the device connects, so the
# newest file is the best guess at the newest phone. Each try is bounded, the list is cut
# at five, and it stops as soon as anything is connected (a phone may beat us to it).
# Exits 3 when it called phones and none connected.
if [ "${cmd}" = reconnect ]; then
	connected() { [ -n "$(bluetoothctl devices Connected </dev/null 2>/dev/null || true)" ]; } # no bluetoothd = none
	tried=0
	# shellcheck disable=SC2045 # ls -t is the ordering; the paths are MAC addresses
	for info in $(ls -t "${BT_STATE}"/*/*/info 2>/dev/null); do # nothing paired yet
		connected && exit 0
		[ "${tried}" -lt 5 ] || break
		grep -q '^Trusted=true' "${info}" || continue
		addr=${info%/info}
		addr=${addr##*/}
		tried=$((tried + 1))
		echo "[bt] reconnect: trying ${addr}" >>"${LOG}"
		timeout 10 bluetoothctl connect "${addr}" </dev/null >>"${LOG}" 2>&1
	done
	connected && exit 0
	echo "[bt] reconnect: nothing answered after ${tried} tries" >>"${LOG}"
	# 3: phones were called and none came, so the panel can say it could not connect.
	[ "${tried}" = 0 ] || exit 3
	exit 0
fi

# disconnect: Bluetooth was left a minute ago. Stock drops the idle link then.
if [ "${cmd}" = disconnect ]; then
	bluetoothctl devices Connected </dev/null 2>/dev/null | while read -r _ addr _; do # no bluetoothd
		timeout 5 bluetoothctl disconnect "${addr}" </dev/null >>"${LOG}" 2>&1
	done
	exit 0
fi

# trim: a device connected. Stock keeps eight pairings and forgets the oldest; so do we,
# by the same newest-first info files, never touching a connected device.
if [ "${cmd}" = trim ]; then
	kept=0
	# shellcheck disable=SC2045 # ls -t is the ordering; the paths are MAC addresses
	for info in $(ls -t "${BT_STATE}"/*/*/info 2>/dev/null); do # nothing paired yet
		kept=$((kept + 1))
		[ "${kept}" -gt "${BT_KEEP:-8}" ] || continue
		addr=${info%/info}
		addr=${addr##*/}
		bluetoothctl info "${addr}" </dev/null 2>/dev/null | grep -q 'Connected: yes' && continue # no bluetoothd: not connected
		echo "[bt] pairing list full: forgetting ${addr}" >>"${LOG}"
		timeout 5 bluetoothctl remove "${addr}" </dev/null >>"${LOG}" 2>&1
	done
	exit 0
fi

printf '%s\n' 'menu player' "${cmd}" 'quit' | timeout 5 bluetoothctl >>"${LOG}" 2>&1
