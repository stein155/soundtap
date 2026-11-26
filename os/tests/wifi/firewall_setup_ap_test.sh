#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Behavioural check of the production input filter: loads firewall.nft in a network
# namespace whose "wlan1" is a veth, then sends a DHCPDISCOVER-shaped broadcast to 67 and a
# DNS query to 53 from the client side, plus one to a closed port. Then SSDP (1900), which
# is answered only from the speaker's own subnets: refused before lan-allow.sh adds the
# subnet, accepted after, and still refused from a routed address; then the same over IPv6
# with lan_nets6. Needs root and nft:
#   docker run --rm --privileged -v "$PWD":/w alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc sh /w/os/tests/wifi/firewall_setup_ap_test.sh
#
# The namespace is a process's (unshare + nsenter), not `ip netns`: `ip netns exec` remounts
# /sys, which rootless docker (the CI runner) refuses.
set -eu
apk add -q nftables iproute2 socat util-linux-misc >/dev/null
RULES=${1:-/w/os/board/rootfs/firewall.nft}
unshare -n sleep 600 &
ns=$!
trap 'kill $ns' EXIT
sleep 0.2
spk() { nsenter --net="/proc/${ns}/ns/net" "$@"; }
ip link add wlan1 type veth peer name phone
ip link set wlan1 netns "${ns}"
ip addr add 192.168.42.10/24 dev phone && ip link set phone up
spk ip addr add 192.168.42.1/24 dev wlan1
spk ip link set wlan1 up
spk ip link set lo up
spk nft -f "${RULES}"
got() { # port: does the speaker side receive a UDP datagram on it?
	rm -f /tmp/got
	spk timeout 3 socat -u "UDP4-RECV:$1,reuseaddr" OPEN:/tmp/got,creat &
	recv=$!
	sleep 1
	echo probe | socat -u - "UDP4-DATAGRAM:$2:$1,broadcast,sp=$3" || true
	wait "${recv}" || true
	[ -s /tmp/got ]
}
fail=0
# shellcheck disable=SC2310 # the probe's status is what this test checks
{ got 67 192.168.42.255 68 && echo "DHCP to 67: accepted"; } || { echo "DHCP to 67: DROPPED"; fail=1; }
# shellcheck disable=SC2310 # the probe's status is what this test checks
{ got 53 192.168.42.1 40000 && echo "DNS to 53: accepted"; } || { echo "DNS to 53: DROPPED"; fail=1; }
# shellcheck disable=SC2310 # the probe's status is what this test checks
got 9999 192.168.42.1 40001 && { echo "closed port 9999: accepted"; fail=1; } || echo "closed port 9999: dropped"
# shellcheck disable=SC2310 # the probe's status is what this test checks
got 1900 192.168.42.1 40002 && { echo "SSDP before lan-allow: accepted"; fail=1; } || echo "SSDP before lan-allow: dropped"
mkdir -p /tmp/bin
printf '#!/bin/sh\nexec nsenter --net=/proc/%s/ns/net nft "$@"\n' "${ns}" >/tmp/bin/nft
chmod 0755 /tmp/bin/nft
SOUNDTAP_NFT=/tmp/bin/nft SOUNDTAP_LAN_STATE=/tmp/lan-net \
	sh "${LAN_ALLOW:-/w/os/overlay/opt/soundtap/lan-allow.sh}" add wlan1 192.168.42.1 24
# shellcheck disable=SC2310 # the probe's status is what this test checks
{ got 1900 192.168.42.1 40003 && echo "SSDP from own subnet: accepted"; } || { echo "SSDP from own subnet: DROPPED"; fail=1; }
# A host behind a router: its address is not on the speaker's link.
ip addr add 10.9.9.9/32 dev phone
spk ip route add 10.9.9.9/32 dev wlan1
routed() {
	rm -f /tmp/got
	spk timeout 3 socat -u UDP4-RECV:1900,reuseaddr OPEN:/tmp/got,creat &
	recv=$!
	sleep 1
	echo probe | socat -u - UDP4-DATAGRAM:192.168.42.1:1900,bind=10.9.9.9:40004 || true
	wait "${recv}" || true
	[ -s /tmp/got ]
}
# shellcheck disable=SC2310 # the probe's status is what this test checks
routed && { echo "SSDP from a routed address: accepted"; fail=1; } || echo "SSDP from a routed address: dropped"
# IPv6 (ADR 0134): the same inet table. A container starts with IPv6 off on its links.
sysctl -qw net.ipv6.conf.phone.disable_ipv6=0
spk sysctl -qw net.ipv6.conf.wlan1.disable_ipv6=0
ip -6 addr add fe80::10/64 dev phone nodad
ip -6 addr add 2001:db8:42::10/64 dev phone nodad
ip -6 addr add 2001:db8:99::10/128 dev phone nodad
spk ip -6 addr add fe80::1/64 dev wlan1 nodad
spk ip -6 addr add 2001:db8:42::1/64 dev wlan1 nodad
spk ip -6 route add 2001:db8:99::10/128 dev wlan1
got6() { # <port> <source>: does the speaker side receive a UDP datagram from source?
	rm -f /tmp/got
	spk timeout 3 socat -u "UDP6-RECV:$1,reuseaddr" OPEN:/tmp/got,creat &
	recv=$!
	sleep 1
	echo probe | socat -u - "UDP6-DATAGRAM:[$3]:$1,bind=[$2]" || true
	wait "${recv}" || true
	[ -s /tmp/got ]
}
# shellcheck disable=SC2310 # the probe's status is what this test checks
got6 1900 2001:db8:42::10 2001:db8:42::1 && { echo "IPv6 SSDP before lan_nets6: accepted"; fail=1; } \
	|| echo "IPv6 SSDP before lan_nets6: dropped"
# What soundtapd's lanfilter writes for wlan1 with these addresses.
printf 'flush set inet filter lan_nets6\nadd element inet filter lan_nets6 { "wlan1" . fe80::/10, "wlan1" . 2001:db8:42::/64 }\n' \
	| spk nft -f -
# shellcheck disable=SC2310 # the probe's status is what this test checks
{ got6 1900 2001:db8:42::10 2001:db8:42::1 && echo "IPv6 SSDP from own prefix: accepted"; } \
	|| { echo "IPv6 SSDP from own prefix: DROPPED"; fail=1; }
# shellcheck disable=SC2310 # the probe's status is what this test checks
{ got6 1900 fe80::10%phone fe80::1%phone && echo "IPv6 SSDP over link-local: accepted"; } \
	|| { echo "IPv6 SSDP over link-local: DROPPED"; fail=1; }
# shellcheck disable=SC2310 # the probe's status is what this test checks
got6 1900 2001:db8:99::10 2001:db8:42::1 && { echo "IPv6 SSDP from a routed address: accepted"; fail=1; } \
	|| echo "IPv6 SSDP from a routed address: dropped"
# shellcheck disable=SC2310 # the probe's status is what this test checks
got6 9999 2001:db8:42::10 2001:db8:42::1 && { echo "IPv6 closed port 9999: accepted"; fail=1; } \
	|| echo "IPv6 closed port 9999: dropped"
# Router advertisements from the open setup AP are dropped before the hop-limit-255 accept.
ra=$(spk nft list chain inet filter input | grep -n 'nd-router-advert' | cut -d: -f1 | tr '\n' ' ')
case "$(spk nft list chain inet filter input | grep 'nd-router-advert' | head -n 1)" in
*'iifname "wlan1"'*drop*) echo "IPv6 router advertisement on wlan1: dropped first (${ra})" ;;
*) echo "IPv6 router advertisement on wlan1: NOT dropped before the accept"; fail=1 ;;
esac
exit "${fail}"
