#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Bluetooth A2DP audio sink for the SoundTap-only firmware.
#   TI WiLink BT radio → bluetoothd → bluealsa (A2DP sink) → ALSA → the Bose amp.
# In stock firmware the closed /opt/Bose/Bluetooth binary did the TI attach; here we use
# the standard open path. The TI attach (which ttyO*, TIInit firmware), pairing and audio
# are HARDWARE-ONLY (no BT controller in QEMU) — this script is what the speaker runs.
# The FIFO init.d/soundtap drains into syslogd; the file when there is no syslogd.
LOG=/run/soundtap/log.fifo
[ -p "${LOG}" ] || LOG=/tmp/soundtap.log
log() { echo "[bt] $*" >>"${LOG}" 2>&1; }

# Pairing policy -- the agent, the pairing window, the adapter's name, clearing pairings --
# is soundtapd's (internal/bluez, ADR 0114). This script starts the stack it talks to.

# TI WiLink shared-transport BT is attached over the ST10 UART proven by the stock IRQ map.
# No ST20 BT transport is proven; never probe other UARTs because ST20 uses one for BCO control.
# If ti-st is built into the kernel the HCI may already exist — hciattach then no-ops.
if [ "${MODEL:-st10}" = st10 ] && [ ! -e /sys/class/bluetooth/hci0 ] && command -v hciattach >/dev/null 2>&1; then
	for tty in ${BT_UARTS:-/dev/ttyO2}; do
		[ -e "${tty}" ] || continue
		hciattach -s 115200 "${tty}" texas 3000000 2>>"${LOG}" && { log "hciattach ${tty} texas 3000000"; break; }
	done
fi

for _ in 1 2 3 4 5; do
	[ -e /sys/class/bluetooth/hci0 ] && break
	sleep 1
done

if [ ! -e /sys/class/bluetooth/hci0 ]; then
	log "no bluetooth adapter detected"
	exit 0
fi

mkdir -p /run/dbus

# bluetoothd, bluealsa and the ALSA bridge are one stack, not three daemons: bluealsa
# registers its A2DP endpoints with bluetoothd over D-Bus at startup and never re-registers.
# (soundtapd's agent watches bluetoothd on the bus and registers again by itself.)
# So if bluetoothd restarts -- a crash, an adapter reset -- everything above it keeps
# running and Bluetooth is silently dead: the phone still shows the speaker, still connects,
# and no audio arrives, with nothing in the log to say why. Start them together, and when
# bluetoothd goes, take the rest down with it and start the whole stack again.
# Buildroot installs bluetoothd under /usr/libexec, the Debian-built mainline rootfs under
# /usr/lib. Hardcoding the first left the ST10 restarting a daemon that was not there.
BLUETOOTHD=/usr/libexec/bluetooth/bluetoothd
[ -x "${BLUETOOTHD}" ] || BLUETOOTHD=/usr/lib/bluetooth/bluetoothd
# bluetoothd writes every message to syslog itself and again to stderr: with syslogd
# running (the FIFO above), its stderr would put each line in the log twice.
BT_OUT=${LOG}
[ -p "${LOG}" ] && BT_OUT=/dev/null
start_bt_stack() {
	"${BLUETOOTHD}" -n >>"${BT_OUT}" 2>&1 &
	BLUETOOTHD_PID=$!
	log "bluetoothd (pid ${BLUETOOTHD_PID})"
	sleep 1

	# A2DP sink: accept audio, decode SBC, play to ALSA.
	#
	# --a2dp-volume puts AVRCP absolute volume in native mode: the phone's volume keys set
	# the PCM's Volume property and bluealsa does NOT attenuate the stream itself. The
	# speaker's own volume stays the single control, so the amp, the UI and the OLED move
	# with the phone instead of a second, invisible attenuation sitting in front of them.
	# The poll loop below is what carries that property across to /api/volume.
	bluealsa -p a2dp-sink --a2dp-volume >>"${LOG}" 2>&1 &
	BLUEALSA_PID=$!
	log "bluealsa a2dp-sink (pid ${BLUEALSA_PID})"
	sleep 1

	bt_up_since=$(cut -d. -f1 /proc/uptime)
}

# The restart budget is for a boot loop, not for the speaker's whole uptime. A bluetoothd that
# had run BT_STABLE_SECONDS before it died was working, so the count starts over; only deaths in
# quick succession add up. Without that, the sixth crash in weeks left Bluetooth down for good.
BT_STABLE_SECONDS=${BT_STABLE_SECONDS:-300}
note_bt_death() {
	now_up=$(cut -d. -f1 /proc/uptime)
	[ $((now_up - bt_up_since)) -lt "${BT_STABLE_SECONDS}" ] || restarts=0
	restarts=$((restarts + 1))
}

# The device class comes from /etc/bluetooth/main.conf (Class = 0x240414, Loudspeaker),
# which bluetoothd 5.79 reads at start. Without it iOS took the speaker for a headphone and
# sent the stream about 22 dB down. btmgmt is not used for it: run from here at boot it hung
# and held the management socket.

# As in stock, a phone never takes the speaker: Bluetooth plays only while it is the selected
# source. soundtapd keeps this file for as long as it is, and wakes the loop when that changes.
BT_SELECTED=/run/soundtap/bt-selected

# Routes a phone to the amp while Bluetooth is selected. Not running otherwise, or a phone
# pressing play would write into the bus under another source's audio.
follow_selection() {
	if [ -e "${BT_SELECTED}" ]; then
		[ -n "${APLAY_PID}" ] && kill -0 "${APLAY_PID}" 2>/dev/null && return 0 # gone means start it again
		# --single-audio: one phone plays at a time, as on stock; a second one waits rather than
		# mixing into the first (round-2 gap review).
		bluealsa-aplay --single-audio 00:00:00:00:00:00 >>"${LOG}" 2>&1 &
		APLAY_PID=$!
	elif [ -n "${APLAY_PID}" ]; then
		kill "${APLAY_PID}" 2>/dev/null # already exited
		APLAY_PID=
	fi
}

stop_bt_stack() {
	for pid in "${APLAY_PID}" "${BLUEALSA_PID}" "${BLUETOOTHD_PID}"; do
		[ -n "${pid}" ] && kill "${pid}" 2>/dev/null # already exited
	done
	# No `wait` here: this shell also owns the watchers and the poll loop, so waiting
	# for every child would wait forever.
	sleep 1
	APLAY_PID= BLUEALSA_PID= BLUETOOTHD_PID=
}

start_bt_stack

# Signal soundtapd the BLUETOOTH source only WHILE a phone is actually connected — not at
# boot, which would leave standby into a phantom BLUETOOTH source held forever. Poll
# bluealsa for connected PCMs and emit on/off transitions. If bluealsa-cli is missing we
# don't signal at all (safe: no false claim).
#
# Only the selected Bluetooth source reports a phone streaming: a PCM exists as soon as A2DP
# is connected, but its Running property only turns true when BlueZ reports the transport
# active, i.e. the phone sent AVDTP start. That moves the selected source to playing. With
# another source selected a phone is ignored, connected or streaming, as in stock. A
# disconnect pauses the source; it stays BLUETOOTH until the phone is back.
if command -v bluealsa-cli >/dev/null 2>&1; then
	prev=0
	streaming=0
	meta=
	btvol= btfail=
	restarts=0
	# The loop wakes on events instead of every 3 s: bluealsa-cli monitor says
	# when a phone's PCM comes or goes and when its Volume changes, dbus-monitor when
	# AVRCP changes the track or play status. Each prints a line into one FIFO; the
	# loop reads it with a 10 s timeout, which is also how often bluetoothd's health is
	# looked at when nothing happens. The FIFO is held open read-write so neither side
	# blocks or gets SIGPIPE when the other is away.
	BT_WAKE=/run/soundtap-bt.wake
	rm -f "${BT_WAKE}"
	mkfifo "${BT_WAKE}" && exec 8<>"${BT_WAKE}"
	start_bt_watchers() {
		BT_WATCH_PIDS=
		bluealsa-cli monitor --properties=Volume,Running >&8 2>/dev/null & # ends with the stack; restarted with it below
		BT_WATCH_PIDS=$!
		if command -v dbus-monitor >/dev/null 2>&1; then
			dbus-monitor --system \
				"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',arg0='org.bluez.MediaPlayer1'" \
				>&8 2>/dev/null & # ends with the stack; restarted with it below
			BT_WATCH_PIDS="${BT_WATCH_PIDS} $!"
		fi
	}
	stop_bt_watchers() {
		# shellcheck disable=SC2086 # a list of our own pids
		[ -z "${BT_WATCH_PIDS}" ] || kill ${BT_WATCH_PIDS} 2>/dev/null
		BT_WATCH_PIDS=
	}
	# The speaker's volume to the phone: stock sends it over AVRCP so the phone's
	# slider follows Vol+/Vol-, and without it the phone's next step jumps back. A speaker
	# volume other than the one last synced either way is written to the PCM property,
	# 0-127 against our 0-100; the +50 rounds so the read below maps it back to the same
	# value, and btvol is set to it so that property change is not posted back. The first
	# sync of a connection (btvol empty) goes this way too: as stock answers a phone's volume
	# registration with the speaker's level, a phone left at 100% does not blast the room.
	push_speaker_volume() {
		cur=$(wget -qO- -T 2 http://127.0.0.1/api/volume 2>/dev/null | sed -n 's/.*"volume": *\([0-9][0-9]*\).*/\1/p') # soundtapd restarting: keep the last volume
		case "${cur}" in '' | *[!0-9]*) return 0 ;; *) ;; esac
		[ "${cur}" != "${btvol}" ] || return 0
		bluealsa-cli volume "${pcm}" $(((cur * 127 + 50) / 100)) >/dev/null 2>&1 && btvol=${cur}
		return 0
	}
	start_bt_watchers
	while :; do
		# The stack's health check rides on the poll loop that is already running rather
		# than a watchdog process of its own. kill -0 on a pid we started ourselves, never
		# a match against the process table: a pattern wide enough to find bluetoothd is
		# also wide enough to find the shell that is looking for it.
		# shellcheck disable=SC2250 # os/support/buildroot_wla_nand_check.py matches this text as written
		if ! kill -0 "$BLUETOOTHD_PID" 2>/dev/null; then
			# Bounded, because an adapter that never initialises turns this into a
			# fork bomb on a speaker with 128 MB: the OOM killer takes soundtapd and
			# the watchdog reboots the speaker over a radio nobody is using.
			note_bt_death
			# The connection went with the daemon. Say so before anything else: with
			# prev reset to 0 and no phone back yet, the loop below never saw the
			# 1 -> 0 edge, and BLUETOOTH kept playing with nothing behind it.
			# A pause, as for a lost link below: the phone comes back after the restart.
			if [ "${prev}" = 1 ]; then
				/opt/soundtap/bt-source.sh pause
				prev=0
				streaming=0
			fi
			stop_bt_watchers
			if [ "${restarts}" -gt 5 ]; then
				log "bluetoothd died ${restarts} times; leaving Bluetooth down"
				stop_bt_stack
				return 0
			fi
			log "bluetoothd gone; restarting the Bluetooth stack (${restarts})"
			stop_bt_stack
			start_bt_stack
			start_bt_watchers
			meta=
		fi
		follow_selection
		pcms=$(bluealsa-cli list-pcms 2>/dev/null) # bluealsa not up yet
		[ -n "${pcms}" ] && n=1 || n=0
		if [ "${n}" != "${prev}" ]; then
			# A lost phone pauses Bluetooth rather than releasing it: the source
			# waits for the phone, `on` resumes it, and the idle auto-off ends the wait.
			[ "${n}" = 1 ] || /opt/soundtap/bt-source.sh pause
			# Stock chimes when a device connects; soundtapd debounces and mutes in standby.
			[ "${n}" = 0 ] || wget -qO /dev/null -T 2 --post-data= http://127.0.0.1/api/bluetooth/connected >/dev/null 2>&1 || true
			[ "${n}" = 0 ] || timeout 60 /opt/soundtap/bt-ctl.sh trim || true # bounded; keeps the eight newest pairings
			prev=${n}
			meta=
			btvol=
			streaming=0
		fi
		# Streaming: any PCM whose transport is active. Metadata and volume below wait for
		# it too, so a phone that is only connected cannot move the speaker's volume or
		# overwrite the track of what is really playing.
		s=0
		if [ -e "${BT_SELECTED}" ]; then
			for p in ${pcms}; do
				bluealsa-cli info "${p}" 2>/dev/null | grep -q '^Running: true' && s=1 && break # PCM went away between list and info
			done
		fi
		if [ "${s}" != "${streaming}" ]; then
			# Idle again while still connected: the phone paused. Without an AVRCP player
			# nothing else tells soundtapd, and the UI kept PLAY_STATE.
			if [ "${s}" = 1 ]; then /opt/soundtap/bt-source.sh on; else /opt/soundtap/bt-source.sh pause; fi
			# shellcheck disable=SC2250 # os/tests/playback/test_bt_source_on_stream.py matches this text as written
			streaming=$s
			meta=
			btvol=
		fi
		# What the phone is playing. BlueZ already has it -- AVRCP fills a MediaPlayer1
		# object with title, artist, album and play status -- and nothing was reading it,
		# so the UI showed BLUETOOTH with an empty track and STOP_STATE while music was
		# audibly playing. Pushed the same way librespot pushes Spotify's metadata, and
		# only on a change, because /api/nowplaying is also what flips playStatus to
		# PLAY_STATE and there is no reason to do that forty times a minute.
		if [ "${streaming}" = 1 ]; then
			now=$(/opt/soundtap/bt-nowplaying.sh 2>/dev/null) # no player connected
			# Remembered only once soundtapd took it: a push refused while soundtapd
			# restarts is sent again on the next pass.
			if [ -n "${now}" ] && [ "${now}" != "${meta}" ]; then
				/opt/soundtap/bt-nowplaying.sh --push "${now}" >/dev/null 2>&1 && meta=${now}
			fi
			# Volume from the phone. AVRCP absolute volume arrives as the PCM's Volume
			# property, 0-127 for A2DP; bluealsa-cli prints it as "Volume: L: n R: n" for
			# a stereo PCM and "Volume: n" for mono. Only the left channel is read: the
			# speaker has one volume and a phone sends one value to both channels.
			#
			# Pushed only on a change. The speaker's own volume keys go the other way in
			# push_speaker_volume.
			pcm=$(bluealsa-cli list-pcms 2>/dev/null | head -1)
			push_speaker_volume
			raw=$(bluealsa-cli volume "${pcm}" 2>/dev/null | # PCM went away; the next event retries
				sed -n 's/^Volume: L: \([0-9][0-9]*\).*/\1/p; s/^Volume: \([0-9][0-9]*\)$/\1/p' |
				head -1)
			case "${raw}" in
			'' | *[!0-9]*) ;;
			*)
				want=$(((raw * 100 + 63) / 127))
				# Not before the first sync: a phone's level does not count until the speaker's
				# went out, which the next poll tries again.
				# shellcheck disable=SC2250 # os/tests/playback/test_bt_volume_sync.py matches this text as written
				if [ -n "$btvol" ] && [ "$want" != "$btvol" ]; then
					# BusyBox wget, like the other hooks: the image has no curl.
					# Remembered only once soundtapd took it: a failed push is tried again
					# on the next poll, not lost until the phone changes volume.
					if wget -qO /dev/null -T 2 --header='Content-Type: application/json' \
						--post-data="{\"volume\":${want}}" http://127.0.0.1/api/volume >/dev/null 2>&1; then
						btvol=${want}
					elif [ "${want}" != "${btfail}" ]; then
						btfail=${want} # one log line per value, not one per poll
						log "bluetooth volume ${want} could not be applied"
					fi
				fi
				;;
			esac
		fi
		# Wait for the next event; then let a burst (dbus-monitor prints several lines
		# per signal) settle so it costs one pass, not one per line.
		# shellcheck disable=SC3045 # BusyBox ash has read -t
		if read -r -t 10 _ <&8; then
			# shellcheck disable=SC3045 # BusyBox ash has read -t
			while read -r -t 1 _ <&8; do :; done
		fi
	done
else
	log "bluealsa-cli absent; BT source signalling disabled"
fi
