#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Behavioural check of the production input filter: loads firewall.nft in a network
# namespace whose "wlan1" is a veth, then sends a DHCPDISCOVER-shaped broadcast to 67 and a
# DNS query to 53 from the client side, plus one to a closed port. Then SSDP (1900), which
# is answered only from the speaker's own subnets: refused before lan-allow.sh adds the
# subnet, accepted after, and still refused from a routed address. Needs root and nft:
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
spk() { nsenter --net=/proc/$ns/ns/net "$@"; }
ip link add wlan1 type veth peer name phone
ip link set wlan1 netns "$ns"
ip addr add 192.168.42.10/24 dev phone && ip link set phone up
spk ip addr add 192.168.42.1/24 dev wlan1
spk ip link set wlan1 up
spk ip link set lo up
spk nft -f "$RULES"
got() { # port: does the speaker side receive a UDP datagram on it?
	rm -f /tmp/got
	spk timeout 3 socat -u "UDP4-RECV:$1,reuseaddr" OPEN:/tmp/got,creat &
	recv=$!
	sleep 1
	echo probe | socat -u - "UDP4-DATAGRAM:$2:$1,broadcast,sp=$3" || true
	wait "$recv" || true
	[ -s /tmp/got ]
}
fail=0
got 67 192.168.42.255 68 && echo "DHCP to 67: accepted" || { echo "DHCP to 67: DROPPED"; fail=1; }
got 53 192.168.42.1 40000 && echo "DNS to 53: accepted" || { echo "DNS to 53: DROPPED"; fail=1; }
got 9999 192.168.42.1 40001 && { echo "closed port 9999: accepted"; fail=1; } || echo "closed port 9999: dropped"
got 1900 192.168.42.1 40002 && { echo "SSDP before lan-allow: accepted"; fail=1; } || echo "SSDP before lan-allow: dropped"
mkdir -p /tmp/bin
printf '#!/bin/sh\nexec nsenter --net=/proc/%s/ns/net nft "$@"\n' "$ns" >/tmp/bin/nft
chmod 0755 /tmp/bin/nft
SOUNDTAP_NFT=/tmp/bin/nft SOUNDTAP_LAN_STATE=/tmp/lan-net \
	sh "${LAN_ALLOW:-/w/os/overlay/opt/soundtap/lan-allow.sh}" add wlan1 192.168.42.1 24
got 1900 192.168.42.1 40003 && echo "SSDP from own subnet: accepted" || { echo "SSDP from own subnet: DROPPED"; fail=1; }
# A host behind a router: its address is not on the speaker's link.
ip addr add 10.9.9.9/32 dev phone
spk ip route add 10.9.9.9/32 dev wlan1
routed() {
	rm -f /tmp/got
	spk timeout 3 socat -u UDP4-RECV:1900,reuseaddr OPEN:/tmp/got,creat &
	recv=$!
	sleep 1
	echo probe | socat -u - UDP4-DATAGRAM:192.168.42.1:1900,bind=10.9.9.9:40004 || true
	wait "$recv" || true
	[ -s /tmp/got ]
}
routed && { echo "SSDP from a routed address: accepted"; fail=1; } || echo "SSDP from a routed address: dropped"
exit $fail
