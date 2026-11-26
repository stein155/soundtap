# 0134. The speaker runs IPv6 next to IPv4, behind the same filter

Status: accepted — 2026-11-26

## Context

The kernel was built without IPv6. On a home network that has it, the speaker was then the one
device that could not be reached at its IPv6 addresses, could not reach a station or server
that only has one, and did not answer the AAAA queries of an Apple device, which prefers IPv6
for AirPlay when the network offers it. The owner decided for IPv6 in full, with the input
filter covering it ([ADR 0066](0066-production-hardening.md)).

IPv6 changes more than the kernel. Every address the speaker answers or checks may now be an
IPv6 one: the filter's own-subnet rule, the media URL rule of
[ADR 0041](0041-radio-one-ffmpeg-one-url-rule.md), the Host check against DNS rebinding, the
connection cap per client, and the follower allowance of multiroom. The kernel also has a size
limit of 4.75 MiB for its uImage (`os/support/kernel_size_check.py`, which warns with less than 128 KiB left), and IPv6 built in costs every
model its share of that.

## Decision

The kernel is built with IPv6 (`CONFIG_IPV6`): link-local addresses and SLAAC from router
advertisements, no tunnels (SIT), no Mobile IPv6 and no DHCPv6 client. Connections out prefer a
temporary address (`use_tempaddr=2`, set by the init script on every interface and the default),
so a server out there does not see the stable address that carries the interface's MAC.

IPv4 stays the speaker's one address for the API, its A record, UPnP, multiroom and the peer
list: every SoundTap speaker has one, and peers keep finding each other over IPv4 mDNS. The IPv6
addresses come on top: soundtapd listens on `:80` for both, and avahi announces the speaker's
AAAA records over IPv4 and IPv6 mDNS.

The production input filter is one `inet` table, so each port is opened for IPv4 and IPv6 with
the same rule. The own-subnet rule holds for IPv6 too: `lan_nets6` holds interface and prefix
pairs, `fe80::/10` and each SLAAC prefix of the LAN interfaces (`eth0`, `wlan0`, the setup AP
`wlan1`). The kernel configures those addresses without a hook to call, so soundtapd
(`internal/lanfilter`) looks every five seconds and replaces the set in one nft transaction when
they changed; a script nft refused is tried again after a minute, or at once when the prefixes
change. Neighbour discovery and router advertisements are accepted from the link itself
(hop limit 255), multicast listener queries from a link-local router, and the usual ICMPv6
errors and echo. The open setup AP takes no router advertisements: the filter drops them on
`wlan1`, and soundtapd sets its `accept_ra` to 0 when it creates the interface, so a phone there
cannot hand the speaker an address or a route, on an alpha image too. Snapcast followers on IPv6 go into `snap_followers6`.

Addresses are classed as their IPv4 twins: loopback (`::1`) and link-local (`fe80::/10`) like
127/8 and 169.254/16, a ULA (`fc00::/7`) like a private address, a global address like a public
one, and an IPv4-mapped address as the IPv4 address it maps. A NAT64 address (`64:ff9b::/96`)
counts as the IPv4 address it reaches. An IPv6 literal may carry a zone (`fe80::1%wlan0`); every
check reads it as an address, never as a name. A client's IPv6 `/64` counts as one host against
the connection cap, since it picks new temporary addresses at will; a link-local address counts
by itself. A request to a link-local address of the speaker is treated as one on the setup AP,
since every link has such an address. Support bundles replace a public IPv6 address as they do a
public IPv4 one.

Alternatives that lost: IPv4 only (the state before; the owner wants IPv6); IPv6 as a module
loaded at boot like the ST300's ([ADR 0132](0132-modules-locked-after-boot.md)), which would
spare the kernel limit but nf_tables and connection tracking are built in and need IPv6 built in
with them; a separate `ip6` table (two rulesets to keep equal, where one missed rule opens a port
over IPv6 only); accepting all of `fe80::/10` and `fc00::/7` without a set (it would answer the
USB gadget's link and the ST20's BCO link, and never a SLAAC global address from the LAN);
a hook script for IPv6 like `lan-allow.sh` (nothing calls it: SLAAC has no DHCP client); IPv6
peers and an IPv6 address in `/api/info` (every speaker has IPv4, and the API shape holds from
0.1.0, [ADR 0108](0108-http-api-compatibility-for-1-0.md)).

## Consequences

- A LAN client reaches the web UI, AirPlay and Spotify Connect over IPv6 as over IPv4, and the
  speaker reaches IPv6-only servers. On a network without IPv6 nothing changes.
- A host on the internet that can route to the speaker's global address still gets nothing in
  production: it is not on a listed prefix. Alpha loads no filter, so an alpha speaker on a
  network with global IPv6 and no router firewall is reachable from the internet; alpha is for
  the bench.
- Every rule in `firewall.nft` that names an address (`ip saddr`) needs its IPv6 twin, or a
  service works over IPv4 only. A new LAN interface goes into `lanfilter`'s list as well as
  calling `lan-allow.sh`.
- Code that checks an address parses it with `net/netip` (zones, IPv4-mapped spellings) and
  classes IPv6 as above; `net.ParseIP` on a value from the network is a bug.
- The kernel grows by IPv6 and its netfilter family on every model, against the 4.75 MiB limit
  that `post-image.sh` enforces; a build that exceeds it fails rather than ships.
- The ST20's network card is its BCO ([ADR 0031](0031-bco-network-card.md)): IPv6 works there
  only as far as the BCO app passes IPv6 multicast and unicast frames.
- A stock kernel has no IPv6: avahi logs one socket error at start and runs over IPv4, and the
  init script finds no IPv6 settings to write.

## Code

- `os/board/image/linux.fragment`: `CONFIG_IPV6`, `CONFIG_NF_TABLES_INET`, `CONFIG_NF_TABLES_IPV6`,
  no SIT, tunnel or MIP6; `os/support/kernel_config_check.py` requires and refuses them.
- `os/board/rootfs/firewall.nft`: the `inet` table, `lan_nets6`, `snap_followers6`, ICMPv6;
  `os/overlay/opt/soundtap/lan-allow.sh`; `os/support/rootfs_check.py` refuses a filter that is
  not one `inet` table.
- `app/internal/lanfilter`: `lanfilter.Sync`, `lanfilter.Elements`, started from
  `app/cmd/soundtapd/main.go` on a production image.
- `app/internal/web/firewall.go`: the health check lists `inet filter input`.
- `app/internal/multiroom/firewall.go:AllowFollower`; `app/internal/multiroom/watch.go:streamingFrom`
  reads `/proc/net/tcp6` too.
- `os/overlay/etc/avahi/avahi-daemon.conf`: `use-ipv6=yes`, AAAA over IPv4.
- `os/overlay/etc/init.d/soundtap`: `set_kernel_params` sets `use_tempaddr`.
- Address checks: `app/internal/web/guard.go` (`hostOnly`, `wrongHostPage`),
  `app/internal/web/web.go:reachedOverLAN`, `app/internal/web/devices.go:literalIP`,
  `app/internal/upnp/media.go:fetchableHost`, `app/internal/upnp/events.go:callbackURL`,
  `app/internal/safehttp/safehttp.go:PublicIP`, `app/cmd/soundtapd/conn_limit.go:limitKey`.
