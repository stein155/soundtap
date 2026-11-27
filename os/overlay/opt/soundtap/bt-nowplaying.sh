#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# What the connected phone is playing, from BlueZ's AVRCP player, pushed to soundtapd.
#
# BlueZ exposes the remote player as a MediaPlayer1 object and bluetoothctl prints it
# with "player.show". Nothing read it, so a speaker playing music over Bluetooth showed
# an empty track and STOP_STATE in the UI. /api/nowplaying is the same door librespot
# uses for Spotify metadata, and posting to it is also what flips playStatus.
#
#   bt-nowplaying.sh                 print "status\ttitle\tartist\talbum" (empty if no player)
#   bt-nowplaying.sh --push "LINE"   post that line to soundtapd
#
# Split in two so the caller can compare before it posts: bt.sh polls every three
# seconds and only a change is worth waking soundtapd for.
API="${API:-http://127.0.0.1:80}"

# One field per line out of bluetoothctl's "Key: value" output, matched on the whole
# key. A track called "Status: paused" must not be able to answer for the player.
# A tab inside a value becomes a space: the tab separates the fields of our own line.
field() {
	printf '%s\n' "$1" | sed -n "s/^[[:space:]]*$2: \(.*\)$/\1/p" | head -n1 | tr -d '\r' | tr '\t' ' '
}

# JSON string escaping for a hand-built body. Track names with quotes are common enough
# (12") that posting them raw would silently drop metadata with a 400. Control characters
# (0x00-0x1F) are dropped first, as esc() in spotify-source.sh does: a raw one in an AVRCP
# title is invalid JSON, and the same refused body would be posted again on every change.
json() {
	printf '%s' "$1" | tr -d '\000-\037' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

read_player() {
	show=$(printf 'player.show\nquit\n' | bluetoothctl 2>/dev/null) || return 1 # no player connected
	status=$(field "${show}" "Status")
	[ -n "${status}" ] || return 1
	printf '%s\t%s\t%s\t%s\n' "${status}" "$(field "${show}" "Track.Title" || true)" \
		"$(field "${show}" "Track.Artist" || true)" "$(field "${show}" "Track.Album" || true)"
}

push() {
	line=$1
	title=$(printf '%s' "${line}" | cut -f2)
	artist=$(printf '%s' "${line}" | cut -f3)
	album=$(printf '%s' "${line}" | cut -f4)
	body=$(printf '{"source":"BLUETOOTH","track":"%s","artist":"%s","album":"%s"}' \
		"$(json "${title}" || true)" "$(json "${artist}" || true)" "$(json "${album}" || true)")
	wget -qO /dev/null --header='Content-Type: application/json' \
		--post-data="${body}" "${API}/api/nowplaying" >/dev/null 2>&1
}

case "${1:-}" in
	--push) [ -n "${2:-}" ] || exit 2; push "$2" ;;
	--demo)
		out=$(field "Status: playing
	Track.Title: In Your Eyes
	Track.Artist: Alesso" "Track.Title")
		[ "${out}" = "In Your Eyes" ] || { echo "field() broke: ${out}" >&2; exit 1; }
		out=$(field "$(printf 'Track.Title: Part One\tPart Two')" "Track.Title")
		[ "${out}" = "Part One Part Two" ] || { echo "field() kept a tab: ${out}" >&2; exit 1; }
		out=$(json 'a "12\" mix"')
		[ "${out}" = 'a \"12\\\" mix\"' ] || { echo "json() broke: ${out}" >&2; exit 1; }
		echo "demo ok"
		;;
	"") read_player ;;
	*) echo "usage: $0 [--push LINE]" >&2; exit 2 ;;
esac
