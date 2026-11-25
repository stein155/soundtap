#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# SoundTap installer for Linux and macOS.
#
#   curl -fsSL <site>/installer/install.sh | sh
#
# Finds SoundTouch speakers that still run Bose's software on this computer's networks,
# lets you pick which ones get SoundTap, and asks each to download and install it from
# SoundTap's update feed, as the browser installer does. Nothing is installed on this
# computer; it needs only curl.
set -u
FEED='https://raw.githubusercontent.com/stein155/soundtap/main/updates/soundtouch'
# A preview model installs only from the preview package (ADR 0133), which this feed names; a
# speaker is sent here only after its owner picked it and typed "preview".
PREVIEW_FEED='https://raw.githubusercontent.com/stein155/soundtap/main/updates/soundtouch-preview'
# This script's own site: its versions.json names the models SoundTap installs on, for this
# script, install.ps1 and the browser installer alike.
SITE='https://stein155.github.io/soundtap/installer'
WAIT_MINUTES=10

say() { printf '%s\n' "$*"; }
# Opens a page in the default browser, where there is one; the address is printed anyway.
# (On Linux, "open" can be openvt, so it is used on macOS only.)
open_page() {
	if [ "$(uname)" = Darwin ]; then open "$1"; else xdg-open "$1"; fi >/dev/null 2>&1 || true
}
ask() { printf '%s' "$1" >/dev/tty; read -r reply </dev/tty || reply=''; }

# The /24 networks of this computer's private IPv4 addresses, one prefix per line ("192.168.1").
# Container and VM bridges (Docker, libvirt) hold no speakers.
prefixes() {
	{
		ip -4 -o addr show 2>/dev/null | awk '$2 !~ /^(docker|br-|veth|virbr|cni|lxc)/ {print $4}'
		command -v ip >/dev/null 2>&1 || ifconfig 2>/dev/null | awk '$1 == "inet" {print $2}'
	} |
		sed 's#/.*##; s/^addr://' |
		awk -F. '($1 == 10) || ($1 == 172 && $2 >= 16 && $2 <= 31) || ($1 == 192 && $2 == 168) {print $1 "." $2 "." $3}' |
		sort -u
}

# tag <name> <xml>: the text of the first <name> element.
tag() { printf '%s' "$2" | sed -n "s#.*<$1>\([^<]*\)</$1>.*#\1#p" | head -n 1; }

# rule_field <name> <rule>: a string field of one versions.json rule (one rule per line).
rule_field() { printf '%s' "$2" | sed -n "s/.*\"$1\": *\"\([^\"]*\)\".*/\1/p"; }

# rule_matches <rule> <type> <moduleType>: the speaker's /info type and module type fit the rule.
rule_matches() {
	_t=$(rule_field type "$1" || true) _c=$(rule_field typeContains "$1" || true) _m=$(rule_field moduleType "$1")
	[ -z "${_t}" ] || [ "$2" = "${_t}" ] || return 1
	[ -z "${_c}" ] || case "$2" in *"${_c}"*) ;; *) return 1 ;; esac
	[ -z "${_m}" ] || [ "$3" = "${_m}" ]
}

# classify <versions.json> <type> <moduleType>: sets status to ok, preview (installable, but
# nobody has run SoundTap on the model) or no (not supported yet), and note to what the list
# says about the speaker. The Bose software version does not count. Unsupported rules count
# first, then preview rules, then supported ones: a SoundTouch 20 that reports the Series III's
# module is the Series III preview.
classify() {
	status=no note='  (not supported yet: SoundTap has not run on this model; skipped)'
	for _pass in unsupported preview supported; do
		while IFS= read -r _rule; do
			case "${_rule}" in *'"preview": true'*) _kind=preview ;; *'"supported": true'*) _kind=supported ;; *) _kind=unsupported ;; esac
			{ [ "${_kind}" = "${_pass}" ] && rule_matches "${_rule}" "$2" "$3"; } || continue
			_name=$(rule_field model "${_rule}")
			[ "${_kind}" != unsupported ] || { note="  (${_name}: not supported yet; skipped)"; return 0; }
			if [ "${_kind}" = preview ]; then
				status=preview note="  (${_name}: preview, worked out from Bose's software; nobody has run SoundTap on this model yet)"
			else
				status=ok note=''
			fi
			return 0
		done <<EOF
$(printf '%s\n' "$1" | grep '{"model"' || true)
EOF
	done
}

# feed_for <ip> <list>: the feed to send that speaker to. The preview feed only for a speaker
# the list (n|ip|name|type|status) marks preview, which the owner picked and confirmed; every
# other speaker gets the feed of the supported models.
feed_for() {
	if [ "$(awk -F'|' -v ip="$1" '$2 == ip { print $5; exit }' "$2" || true)" = preview ]; then
		printf '%s\n' "${PREVIEW_FEED}"
	else
		printf '%s\n' "${FEED}"
	fi
}

# Tests load the functions above without running the installer.
[ -z "${SOUNDTAP_INSTALLER_FUNCTIONS:-}" ] || return 0

command -v curl >/dev/null 2>&1 || { say "This installer needs curl."; exit 1; }
[ -r /dev/tty ] || { say "Run this in a terminal: it asks which speakers to install."; exit 1; }
tmp=$(mktemp -d) || exit 1
trap 'rm -rf "$tmp"' EXIT INT TERM

table=$(curl -fsSL -m 15 "${SITE}/versions.json" 2>/dev/null)
case "${table}" in *'"supported": true'*) ;; *)
	say "Could not load the list of supported speakers (${SITE}/versions.json). Is this computer online?"
	exit 1
	;;
esac

say "Looking for SoundTouch speakers with Bose's software…"
nets=$(prefixes)
[ -n "${nets}" ] || { say "This computer has no private IPv4 network address."; exit 1; }
for net in ${nets}; do
	i=1
	while [ "${i}" -le 254 ]; do
		ip="${net}.${i}"
		( curl -s -m 2 "http://${ip}:8090/info" >"${tmp}/${ip}" 2>/dev/null || rm -f "${tmp}/${ip}" ) &
		i=$((i + 1))
	done
	wait
done

n=0
for f in "${tmp}"/*; do
	[ -s "${f}" ] || continue
	xml=$(tr -d '\r\n' <"${f}")
	case "${xml}" in *"<info "*) ;; *) continue ;; esac
	ip=${f##*/}
	# One speaker answers on each of its addresses: list it once.
	id=$(printf '%s' "${xml}" | sed -n 's#.*<info deviceID="\([^"]*\)".*#\1#p')
	[ -n "${id}" ] && grep -q "^${id}\$" "${tmp}/ids" 2>/dev/null && continue
	echo "${id}" >>"${tmp}/ids"
	name=$(tag name "${xml}"); type=$(tag type "${xml}")
	classify "${table}" "${type}" "$(tag moduleType "${xml}" || true)"
	n=$((n + 1))
	printf '%s|%s|%s|%s|%s\n' "${n}" "${ip}" "${name}" "${type}" "${status}" >>"${tmp}/list"
	printf '  %d) %s — %s at %s%s\n' "${n}" "${name:-unnamed}" "${type:-unknown model}" "${ip}" "${note}"
done
[ "${n}" -gt 0 ] || {
	say "No speaker with Bose's software answered. Is it on, and on the same network as this computer?"
	say "Speakers that already run SoundTap are not listed: update those from their own web page."
	exit 1
}

ok=$(grep -c '|ok$' "${tmp}/list")
previews=$(grep -c '|preview$' "${tmp}/list")
[ $((ok + previews)) -gt 0 ] || { say "None of these speakers can get SoundTap (see above)."; exit 1; }
say ""
# A preview speaker is never in "all of them": it is picked by its number, then confirmed.
picked='' picked_preview=0
if [ "${ok}" -eq 1 ] && [ "${previews}" -eq 0 ]; then
	picked=$(grep '|ok$' "${tmp}/list" | cut -d'|' -f2)
	ask "Install SoundTap on $(grep '|ok$' "${tmp}/list" | cut -d'|' -f3 || true)? (y/n) "
	case "${reply}" in [yYjJ]*) ;; *) say "Stopped; nothing was changed."; exit 1 ;; esac
else
	if [ "${ok}" -gt 0 ]; then
		ask "Which speakers? Press Enter for all supported ones, or type their numbers (like: 1 3): "
	else
		ask "Only previews here. Type the numbers of the speakers to install (like: 1 3), or press Enter to stop: "
	fi
	choice=${reply}
	[ -n "${choice}" ] || choice=$(grep '|ok$' "${tmp}/list" | cut -d'|' -f1)
	for k in ${choice}; do
		line=$(grep "^${k}|" "${tmp}/list") || { say "There is no speaker ${k}."; exit 1; }
		case "$(printf '%s' "${line}" | cut -d'|' -f5)" in
		ok) ;;
		preview) picked_preview=1 ;;
		*) say "Speaker ${k} cannot get SoundTap (see above); skipped."; continue ;;
		esac
		picked="${picked} $(printf '%s' "${line}" | cut -d'|' -f2)"
	done
	[ -n "${picked}" ] || { say "Nothing to install."; exit 1; }
fi
if [ "${picked_preview}" = 1 ]; then
	say ""
	say "A preview model: SoundTap is worked out from Bose's own software and nobody has run it on"
	say "this model yet. It may not play, its buttons or lamps may be wrong, and its way back may be"
	say "unproven. Read first:"
	say "  https://stein155.github.io/soundtap/owners/compatibility/#previews"
	ask "Type preview to install on it anyway: "
	[ "${reply}" = preview ] || { say "Stopped; nothing was changed."; exit 1; }
fi

say ""
say "SoundTap replaces Bose's software on the speaker. You can go back to Bose later:"
say "  https://stein155.github.io/soundtap/owners/install/#going-back-to-bose"
ask "Continue? (y/n) "
case "${reply}" in [yYjJ]*) ;; *) say "Stopped; nothing was changed."; exit 1 ;; esac

for ip in ${picked}; do
	feed=$(feed_for "${ip}" "${tmp}/list")
	if curl -sf -m 15 -o /dev/null -X POST -H 'Content-Type: text/plain' \
		--data "<swUpdateStart indexUrl=\"${feed}\"/>" "http://${ip}:8090/swUpdateStart"; then
		say "${ip}: install started."
	else
		say "${ip}: the request was not confirmed; it may still have arrived."
	fi
done

say ""
say "Installing. Leave the speakers alone and keep them plugged in; this takes about five minutes."
start=$(date +%s)
end=$((start + WAIT_MINUTES * 60))
shown=0
left=${picked}
while [ -n "${left}" ] && [ "$(date +%s || true)" -lt "${end}" ]; do
	sleep 10
	still=''
	for ip in ${left}; do
		v=$(curl -s -m 4 "http://${ip}/api/version" 2>/dev/null)
		case "${v}" in
			*'"api"'*)
				say "${ip}: done, it runs SoundTap. Opening http://${ip}/ to set it up."
				open_page "http://${ip}/"
				;;
			*) still="${still} ${ip}" ;;
		esac
	done
	left=${still}
	m=$((($(date +%s) - start) / 60))
	[ -z "${left}" ] || [ "${m}" -le "${shown}" ] || { shown=${m}; say "  still installing… ${m} min"; }
done
for ip in ${left}; do
	say "${ip}: SoundTap not confirmed after ${WAIT_MINUTES} minutes. Keep it powered and open http://${ip}/ later,"
	say "    or see 'When something goes wrong' in the install guide."
done
[ -z "${left}" ]
