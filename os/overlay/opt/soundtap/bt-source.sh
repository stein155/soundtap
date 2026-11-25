#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Called by bt.sh around bluealsa: `on` when the phone starts streaming A2DP, `pause` when
# it stops or disconnects. `off` releases the source.
# Signals soundtapd via the SoundTap JSON API so the arbiter claims/releases the source.
API="${API:-http://127.0.0.1:80}"

# post retries a few times so a source event isn't lost while soundtapd is (re)starting.
post() {
	for _ in 1 2 3; do
		wget -qO /dev/null --header='Content-Type: application/json' --post-data="$1" "${API}/api/source" >/dev/null 2>&1 && return 0
		sleep 1
	done
	return 0
}

case "${1:-}" in
	# The play event takes the source out of a pause that the claim alone would leave.
	on)  post '{"source":"BLUETOOTH"}' && post '{"source":"BLUETOOTH","event":"play"}' ;;
	pause) post '{"source":"BLUETOOTH","event":"pause"}' ;;
	off) post '{"source":"BLUETOOTH","event":"stop"}' ;;
	*) ;;
esac
