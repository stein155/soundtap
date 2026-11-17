# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# shellcheck shell=sh
# shellcheck disable=SC2034 # boot_* and hil_* are read by the scripts that source this.
# What the bench scripts in tools/hil/ share: reading a speaker through its HTTP API, the
# confirmation of a dangerous request, bounded waits and the serial console. Sourced, not run.
#
# A production image has no SSH (ADR 0119), so everything here works through the API alone:
# GET /api/health says which boot runs (boot.id), from which slot, whether soundtapd has
# confirmed it (ADR 0010) and how the boot before it ended; GET /api/version which build.
# A request that waits for a key held on the speaker (ADR 0055) prints a prompt and waits
# for the hold. With root SSH (an alpha image) it carries the one-time token instead, so a
# run needs nobody at the speaker.
#
# Settings, from the environment:
#   SOUNDTAP_HIL_SSH      auto (default): use root SSH when the speaker takes it; 0: never;
#                         1: required
#   SOUNDTAP_HIL_SERIAL   the speaker's console (ST10/ST20 UART, 115200 baud), optional:
#                         everything it prints during the run goes to a log, and a failure
#                         shows its last lines
#   SOUNDTAP_HIL_CONSOLE_LOG  where that log goes (default hil-console-<time>.log)
#   SOUNDTAP_HIL_POLL     seconds between two looks at the speaker (default 5)
#   SOUNDTAP_HIL_TIMEOUT  seconds a boot or a boot confirm may take (default 300)
#
# Needs curl and python3 (for the JSON) on the machine that runs it.

hil_poll=${SOUNDTAP_HIL_POLL:-5}
hil_timeout=${SOUNDTAP_HIL_TIMEOUT:-300}
console_log=''

# hil_init <speaker[:port]>: where the speaker is, whether SSH is there, the console.
hil_init() {
	hil_ip=$1
	hil_host=${1%:*}
	case ${SOUNDTAP_HIL_SSH:-auto} in
	0) hil_ssh=0 ;;
	1) hil_ssh=1
	   hil_ssh_run true || hil_fail "no root ssh to $hil_host (SOUNDTAP_HIL_SSH=1 needs an alpha image)" ;;
	*) if hil_ssh_run true; then hil_ssh=1; else hil_ssh=0; fi ;;
	esac
	if [ "$hil_ssh" = 1 ]; then
		echo "speaker: $hil_ip, root ssh (alpha image): confirmations use the one-time token" >&2
	else
		echo "speaker: $hil_ip, no ssh (production image): confirmations need a key held on the speaker" >&2
	fi
	hil_serial_start
}

# hil_ssh_run <command>: runs a command on the speaker as root, bounded.
hil_ssh_run() {
	set -- ssh -o ConnectTimeout=5 -o BatchMode=yes -o ServerAliveInterval=5 -o ServerAliveCountMax=2 "root@$hil_host" "$1"
	if command -v timeout >/dev/null 2>&1; then
		timeout 15 "$@" 2>/dev/null
	else
		"$@" 2>/dev/null
	fi
}

# hil_get <path>: the body of GET <path>, or a failure when the speaker does not answer 2xx.
hil_get() {
	curl -sS -f -m 10 "http://$hil_ip$1" 2>/dev/null
}

# hil_json <field>: one field of the JSON on stdin, dotted for nested objects. A string as
# it is, anything else as JSON (true, 12). Fails when the field is not there.
hil_json() {
	python3 -c '
import json, sys
v = json.load(sys.stdin)
for k in sys.argv[1].split("."):
    v = v[k]
print(v if isinstance(v, str) else json.dumps(v))' "$1" 2>/dev/null
}

# hil_boot: reads the running boot into boot_id, boot_slot (PING or PONG), boot_confirmed
# (true or false) and boot_previous. Fails while the speaker does not answer.
hil_boot() {
	h=''
	if h=$(hil_get /api/health) && boot_id=$(printf '%s' "$h" | hil_json boot.id); then
		boot_slot=$(printf '%s' "$h" | hil_json boot.slot)
		boot_confirmed=$(printf '%s' "$h" | hil_json boot.confirmed)
		boot_previous=$(printf '%s' "$h" | hil_json boot.previous)
		[ -n "$boot_id" ]
		return
	fi
	# An image older than /api/health's boot answers without it; over SSH the boot can
	# still be read from the shell, as these scripts did before.
	[ "$hil_ssh" = 1 ] && [ -n "${h:-}" ] || return 1
	boot_id=$(hil_ssh_run 'cat /proc/sys/kernel/random/boot_id') && [ -n "$boot_id" ] || return 1
	case $(hil_ssh_run 'grep -o "ubi.mtd=[89]" /proc/cmdline | head -n 1') in
	ubi.mtd=8) boot_slot=PING ;;
	ubi.mtd=9) boot_slot=PONG ;;
	*) boot_slot='' ;;
	esac
	if hil_ssh_run 'cat /tmp/soundtap.log* | grep -q "msg=\"boot confirmed\""'; then
		boot_confirmed=true
	else
		boot_confirmed=false
	fi
	boot_previous=$(hil_ssh_run 'cat /mnt/nv/soundtap/last-boot/how 2>/dev/null' || true)
}

# hil_since <epoch seconds>: seconds since then.
hil_since() { echo $(($(date +%s) - $1)); }

# hil_wait_boot <boot id>: waits until the speaker answers from a boot other than that
# one, at most SOUNDTAP_HIL_TIMEOUT seconds; then boot_* describe the new boot.
hil_wait_boot() {
	hil_t0=$(date +%s)
	while :; do
		if hil_boot && [ "$boot_id" != "$1" ]; then
			return 0
		fi
		[ "$(hil_since "$hil_t0")" -lt "$hil_timeout" ] ||
			hil_fail "not back $hil_timeout s after the reboot"
		sleep "$hil_poll"
	done
}

# hil_wait_confirmed: waits until soundtapd has confirmed the running boot (a minute after
# it started), at most SOUNDTAP_HIL_TIMEOUT seconds. A new boot on the way fails it.
hil_wait_confirmed() {
	hil_t0=$(date +%s) hil_boot0=$boot_id
	while :; do
		if hil_boot; then
			[ "$boot_id" = "$hil_boot0" ] || hil_fail "the speaker rebooted before it confirmed its boot"
			[ "$boot_confirmed" != true ] || return 0
		fi
		[ "$(hil_since "$hil_t0")" -lt "$hil_timeout" ] ||
			hil_fail "boot not confirmed $hil_timeout s after it answered"
		sleep "$hil_poll"
	done
}

# hil_gated <what> <method> <path> [curl arguments]: a request that waits for a key held on
# the speaker (ADR 0055). Prints the HTTP status (000 when there was no answer); the body
# goes to stderr. Over SSH it carries the one-time token, which only root can read and
# which changes with every use; otherwise it asks whoever is at the speaker for the hold.
hil_gated() {
	hil_what=$1 hil_method=$2 hil_path=$3
	shift 3
	if [ "$hil_ssh" = 1 ]; then
		set -- -H "X-SoundTap-Device-Token: $(hil_ssh_run 'cat /run/soundtap/confirm-token 2>/dev/null' || true)" "$@"
	else
		hil_prompt_hold "$hil_what"
	fi
	curl -sS -o /dev/stderr -w '%{http_code}' -X "$hil_method" "http://$hil_ip$hil_path" "$@" || true
}

# hil_prompt_hold <what>: tells whoever is at the speaker to confirm now. The request goes
# out right after, and the speaker waits 60 s for the hold.
hil_prompt_hold() {
	{
		echo
		echo ">>> Hold a button on the speaker now (not volume) for two seconds to confirm: $1"
		echo "    The speaker shows it waits: ST10 lamps blink white, the Wireless Link blinks amber,"
		echo "    the ST20 display names it. It gives up after 60 s."
	} >&2
}

# hil_manual <instruction>: a step only a person can do (a power cut); the instruction
# says when to press Enter.
hil_manual() {
	printf '\n>>> %s\n' "$1" >&2
	read -r _ || hil_fail "no answer to the manual step"
}

# hil_fail <message>: stops the run, with the console's last lines when there is one.
hil_fail() {
	echo "FAIL: $*" >&2
	if [ -n "$console_log" ] && [ -s "$console_log" ]; then
		echo "last console lines ($console_log):" >&2
		tail -n 20 "$console_log" >&2
	fi
	exit 1
}

# hil_serial_start: keeps what the console prints while the script runs. The UART shows
# SPL, U-Boot and the kernel even on a production image, which gives no login there, so a
# boot that stalls before the network says where it stopped.
hil_serial_start() {
	[ -n "${SOUNDTAP_HIL_SERIAL:-}" ] || return 0
	[ -r "$SOUNDTAP_HIL_SERIAL" ] || hil_fail "cannot read the serial device $SOUNDTAP_HIL_SERIAL"
	console_log=${SOUNDTAP_HIL_CONSOLE_LOG:-hil-console-$(date +%Y%m%d-%H%M%S).log}
	: >>"$console_log"
	# stty sets the device open as stdin, the same one cat then reads: on macOS a setting
	# made through another descriptor is gone when that one closes.
	{
		stty 115200 raw -echo 2>/dev/null ||
			echo "warning: could not set $SOUNDTAP_HIL_SERIAL to 115200 baud" >&2
		exec cat
	} <"$SOUNDTAP_HIL_SERIAL" >>"$console_log" &
	hil_console_pid=$!
	trap 'kill "$hil_console_pid" 2>/dev/null || true' EXIT
	trap 'exit 130' INT TERM
	echo "console: $SOUNDTAP_HIL_SERIAL at 115200 baud, kept in $console_log" >&2
}

# hil_volume_save / hil_volume_restore: the volume before the run, put back after it.
hil_volume_save() {
	hil_volume0=$(hil_get /api/volume | hil_json volume) || hil_volume0=''
}
hil_volume_restore() {
	[ -n "$hil_volume0" ] || return 0
	curl -sS -f -m 10 -o /dev/null -X POST "http://$hil_ip/api/volume" \
		-H 'Content-Type: application/json' -d "{\"volume\":$hil_volume0}" 2>/dev/null ||
		echo "warning: could not put the volume back to $hil_volume0" >&2
}
