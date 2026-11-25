#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Called by shairport-sync sessioncontrol when AirPlay starts/stops.
# Signals soundtapd via the SoundTap JSON API so the arbiter claims/releases the source.
API="${API:-http://127.0.0.1}"

# post retries a few times so a source event isn't lost while soundtapd is (re)starting.
post() {
	for _ in 1 2 3; do
		wget -qO /dev/null --header='Content-Type: application/json' --post-data="$1" "${API}/api/source" >/dev/null 2>&1 && return 0
		sleep 1
	done
	return 1
}

case "${1:-}" in
	on)  post '{"source":"AIRPLAY"}' ;;
	off) post '{"source":"AIRPLAY","event":"stop"}' ;;
	*) ;;
esac
