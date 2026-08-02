#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

NTPD_PID=${NTPD_PID:-/run/soundtap-ntpd.pid}

# Where the time comes from, in order (docs/owners/faq.md lists it for owners):
#  1. The NTP servers the DHCP lease names (option 42): the udhcpc hook writes them to
#     DHCP_NTP. A network that blocks outside NTP usually names its own.
#  2. The owner's own servers in NTP_SERVERS, when that file names any; else the default.
# The default is not the NTP Pool: its terms do not let a distributed device use the
# default zone (pool.ntp.org, 0-3.pool.ntp.org), a vendor needs a zone of its own. Until
# SoundTap has one, the default is Cloudflare's and Google's public time services, which
# any device may use for free and without registering; two operators, so an outage of one
# leaves the other. Should the pool grant SoundTap a vendor zone, NTP_VENDOR_ZONE is the
# one line to set (soundtap.pool.ntp.org): its names then come first in the default.
DHCP_NTP=${DHCP_NTP:-/run/soundtap/ntp-servers}
NTP_SERVERS=${NTP_SERVERS:-/mnt/nv/ntp-servers}
NTP_VENDOR_ZONE=${NTP_VENDOR_ZONE:-}
NTP_PUBLIC="time.cloudflare.com time.google.com"

# ntp_names prints, one a line, the words of its input that can be a host name or an
# address; # starts a comment. The input is data: a word ntpd could take for an option
# (-S runs a script) is dropped.
ntp_names() {
	sed 's/#.*//' | tr -s ' \t' '\n\n' | grep -E '^[A-Za-z0-9][A-Za-z0-9.:-]*$'
}

# ntp_fallback prints the servers asked after the lease's: the owner's, else the default.
ntp_fallback() {
	own=$([ -r "$NTP_SERVERS" ] && ntp_names <"$NTP_SERVERS") # no file: the default
	if [ -n "$own" ]; then
		printf '%s\n' "$own"
		return 0
	fi
	[ -z "$NTP_VENDOR_ZONE" ] || printf '%s.%s ' 0 "$NTP_VENDOR_ZONE" 1 "$NTP_VENDOR_ZONE" 2 "$NTP_VENDOR_ZONE"
	printf '%s\n' "$NTP_PUBLIC"
}

# ntp_peers prints ntpd's -p arguments: the lease's servers first, then the fallback.
ntp_peers() {
	lease=$([ -r "$DHCP_NTP" ] && ntp_names <"$DHCP_NTP") # no file: no lease named one
	for s in $lease $(ntp_fallback); do
		printf -- '-p %s ' "$s"
	done
	echo
}

credible_clock() {
	year=$(date +%Y)
	[ "$year" -ge 2020 ]
}

sync_clock() {
	waited=0
	while ! ip route | grep -q '^default'; do
		[ "$waited" -ge "${CLOCK_ROUTE_WAIT_SECONDS:-90}" ] && return 1
		waited=$((waited + 1))
		sleep 1
	done

	for _ in 1 2 3; do
		# shellcheck disable=SC2046 # one argument per word
		if timeout 20 ntpd -n -q $(ntp_peers) && credible_clock; then
			return 0
		fi
		sleep 2
	done
	return 1
}

# The one-shot sync steps the clock once; after it a long-running ntpd keeps it, so weeks
# of uptime do not drift the alarm and the standby clock. Started once: the sync after a
# Wi-Fi join runs again on every rejoin.
keep_clock() {
	pid=$(cat "$NTPD_PID" 2>/dev/null) # not started yet
	[ -n "$pid" ] && [ "$(cat "/proc/$pid/comm" 2>/dev/null)" = ntpd ] && return 0 # gone = restart it
	# Two syncs can finish together (the one after a Wi-Fi join and init's); the mkdir is the
	# claim, so only one of them starts the daemon.
	mkdir "$NTPD_PID.claim" 2>/dev/null || return 0
	# shellcheck disable=SC2046 # one argument per word
	ntpd -n $(ntp_peers) </dev/null >/dev/null 2>&1 &
	echo $! >"$NTPD_PID"
	rmdir "$NTPD_PID.claim"
}

# clocksync.Path: the standby clock and the alarm trust the clock only once this exists.
CLOCK_SYNCED=${CLOCK_SYNCED:-/run/soundtap/clock-synced}

sync_clock_and_save() {
	sync_clock || return 1
	touch "$CLOCK_SYNCED" 2>/dev/null || true # no /run/soundtap: nothing reads it either
	keep_clock
	save_action=${CLOCK_SAVE_ACTION:-/etc/init.d/soundtap}
	[ ! -x "$save_action" ] || "$save_action" save-clock
}

# Keep retrying after a successful DHCP lease: route can precede usable DNS/WAN.
# One loop at a time: the sync after a join calls this on every rejoin that finds NTP still blocked,
# and each call must not leave another loop running for good.
CLOCK_RETRY_PID=${CLOCK_RETRY_PID:-/run/soundtap-clock-retry.pid}
sync_clock_in_background() {
	pid=$(cat "$CLOCK_RETRY_PID" 2>/dev/null) # none yet
	[ -n "$pid" ] && kill -0 "$pid" 2>/dev/null && return 0 # gone: start a new one
	(
		while ! sync_clock_and_save; do
			sleep "${CLOCK_RETRY_SECONDS:-60}"
		done
		rm -f "$CLOCK_RETRY_PID"
	) &
	echo $! >"$CLOCK_RETRY_PID"
}
