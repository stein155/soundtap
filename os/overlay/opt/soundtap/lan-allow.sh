#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Keeps the production filter's set lan_nets (firewall.nft) equal to the subnets the speaker
# has an address on, so that it answers only hosts on its own links, as stock's firewall did.
#   lan-allow.sh add <iface> <ip> <netmask>   or   add <iface> <ip>/<prefix>
#   lan-allow.sh del <iface>
# Called by the udhcpc hook, soundtapd's setup AP and the ST20's static eth0. Without the
# production ruleset (alpha) there is no set and this does nothing.
set -eu
STATE=${SOUNDTAP_LAN_STATE:-/run/soundtap/lan-net}
NFT=${SOUNDTAP_NFT:-nft}
LOG=/run/soundtap/log.fifo
[ -p "${LOG}" ] || LOG=/tmp/soundtap.log
log() { echo "[firewall] $*" >>"${LOG}" 2>&1 || true; }  # an unwritable log loses the line only

command -v "${NFT}" >/dev/null 2>&1 || exit 0
"${NFT}" list set inet filter lan_nets >/dev/null 2>&1 || exit 0  # alpha: no ruleset loaded

octet() { case "$1" in '' | *[!0-9]*) return 1 ;; *) ;; esac; [ "$1" -le 255 ]; }

# <ip> <netmask|prefix> -> network/prefix, or nothing for a malformed address
network() {
	ip=$1 mask=$2
	case "${mask}" in
	*.*) ;;
	'' | *[!0-9]*) return 0 ;;
	*)
		[ "${mask}" -le 32 ] || return 0
		m=$(( mask == 0 ? 0 : (0xffffffff << (32 - mask)) & 0xffffffff ))
		mask="$((m >> 24 & 255)).$((m >> 16 & 255)).$((m >> 8 & 255)).$((m & 255))"
		;;
	esac
	old=${IFS}
	IFS=.
	# shellcheck disable=SC2086 # split on the dots on purpose
	set -- ${ip} ${mask}
	IFS=${old}
	[ $# -eq 8 ] || return 0
	# shellcheck disable=SC2310 # a test: false is an answer, not an error
	for o in "$@"; do octet "${o}" || return 0; done
	bits=0
	for o in "$5" "$6" "$7" "$8"; do
		while [ "${o}" -gt 0 ]; do bits=$((bits + (o & 1))); o=$((o >> 1)); done
	done
	echo "$(($1 & $5)).$(($2 & $6)).$(($3 & $7)).$(($4 & $8))/${bits}"
}

# Two interfaces on one subnet (wlan0 and eth0 on the same LAN) share one element: an
# interval set refuses a second, overlapping one, so each is added once and removed with
# its last user.
used_elsewhere() { # <iface> <net>
	for f in "${STATE}".*; do
		[ -s "${f}" ] && [ "${f}" != "${STATE}.$1" ] && [ "$(cat "${f}" || true)" = "$2" ] && return 0
	done
	return 1
}

drop() { # <iface>
	[ -s "${STATE}.$1" ] || return 0
	gone=$(cat "${STATE}.$1")
	# Emptied, not removed: init makes the usual interfaces' files root's and writable for
	# soundtapd's DHCP too, in a sticky directory where only the owner may remove one (ADR 0066).
	true >"${STATE}.$1" || log "$1: could not clear ${STATE}.$1"
	# shellcheck disable=SC2310 # a test: false is an answer, not an error
	used_elsewhere "$1" "${gone}" && return 0
	"${NFT}" delete element inet filter lan_nets "{ ${gone} }" 2>>"${LOG}" || log "could not remove ${gone}"
	log "$1: no longer answering ${gone}"
}

mkdir -p "$(dirname "${STATE}")"
case "${1:-}" in
add)
	iface=${2:-} addr=${3:-} mask=${4:-}
	case "${addr}" in */*) mask=${addr#*/} addr=${addr%/*} ;; *) ;; esac
	net=$(network "${addr}" "${mask}")
	[ -n "${iface}" ] && [ -n "${net}" ] || { log "add: no usable address '${3:-}' '${4:-}' on '${iface}'"; exit 1; }
	[ "$(cat "${STATE}.${iface}" 2>/dev/null || true)" = "${net}" ] && exit 0  # a renewal of the same lease
	drop "${iface}"
	echo "${net}" >"${STATE}.${iface}"
	# shellcheck disable=SC2310 # a test: false is an answer, not an error
	used_elsewhere "${iface}" "${net}" && exit 0
	"${NFT}" add element inet filter lan_nets "{ ${net} }" 2>>"${LOG}" || { log "could not add ${net}"; exit 1; }
	log "${iface}: answering ${net}"
	;;
del)
	[ -n "${2:-}" ] || exit 1
	drop "$2"
	;;
*)
	echo "usage: $0 add <iface> <ip> <netmask> | add <iface> <ip>/<prefix> | del <iface>" >&2
	exit 2
	;;
esac
