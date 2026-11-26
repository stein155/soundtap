# 0043. mDNS goes through avahi-daemon over D-Bus; the device registry holds each speaker id to one address

Status: accepted — 2026-11-20

## Context

A speaker needs a name on the LAN, a `_soundtap._tcp` service, the shared `soundtap.local` name,
and a list of the other speakers behind `GET /api/devices`. avahi-daemon runs on every speaker
anyway, because shairport-sync, librespot and snapcast advertise through it, and it is the Linux
mDNS stack; a second responder of SoundTap's own would share UDP 5353 with it. The project prefers
popular, widely used projects wherever they exist ([ADR 0124](0124-third-party-code.md)).

What comes in over mDNS is unauthenticated: any host on the LAN can publish an A record or a
`_soundtap._tcp` instance carrying a real speaker's id. The UI sends Wi-Fi and MQTT passwords
"to that speaker", and the device proxy forwards the browser's requests to a peer's address.

## Decision

### avahi does the DNS

`app/internal/mdns` does no DNS itself. It asks avahi-daemon over avahi's D-Bus API, through
godbus ([ADR 0114](0114-bluetooth-agent-in-soundtapd.md)), making the calls avahi's own tools
make:

| What | How |
| --- | --- |
| own name | `Server.SetHostName soundtap-<device id>` (lowercase; "redundant" when avahi has it already); the name avahi settled on is read back with `GetHostNameFqdn` every 30 s |
| service | an entry group with `AddService <name> _soundtap._tcp <port>` and the TXT strings `id= name= model= fw= api=`, in the order `avahi-publish -s` passes them; on a collision avahi's `GetAlternativeServiceName` ("Keuken #2") |
| shared name | an entry group with `AddAddress soundtap.local <ip>` and no reverse record, only while `ResolveHostName soundtap.local` (IPv4) finds no other speaker; withdrawn on a collision, never renamed; checked again every 30 s |
| other speakers | `ServiceBrowserNew _soundtap._tcp`; each IPv4 `ItemNew` resolved with `ResolveService`, `ItemRemove` a departure |

Only `soundtap.local` is for people: every speaker competes for it and the winner serves the
whole household. The own host name exists only because avahi needs a host for the
`_soundtap._tcp` address records; nothing in the UI or the docs points at it, and renaming a
speaker does not change it. One particular speaker is reached by its IP address, or through
`soundtap.local` and the room picker.

Everything is published over one bus connection: closing it, or a new name or address, withdraws
the groups (with goodbyes) and publishes them again, as does avahi's server running again after a
host name change, or a new connection after avahi-daemon or the bus restarts. The browser runs on
a connection of its own, and is replaced every 10 minutes so that a peer that rebooted into new
firmware without a goodbye is resolved again (avahi resolves an instance once, when it appears).
avahi reports a peer once, but the registry ages entries out, so every known peer is re-reported
every `MaxBrowseDelay`. soundtapd stays a static Go binary: no cgo, no libavahi-client, and none
of avahi's command-line tools.

### The registry holds an id to one address

The browser accepts a peer only at an on-link IPv4 address that is not loopback and not this
speaker's own, and the registry only at a private LAN address; the device
proxy forwards only `/api/` paths, and asks `/api/info` for the expected speaker id before it
forwards. An id is held by the address that announced it until that entry is five minutes stale
or says goodbye, and only that address may say goodbye for it. When a known id is announced from
a new address and the model matches, the registry dials the old address once (TCP, 2 s); if
nothing answers, the new address takes the entry over, so a speaker renumbered by DHCP is
reachable again at once. A speaker reachable on two interfaces departs only when both report it
gone. The registry holds at most 64 entries.

Alternatives that lost: a responder of SoundTap's own (a second mDNS stack in SoundTap's code next to the one the
image already runs); avahi's command-line tools (up to four processes held open, collisions found
by matching their output, a line format to parse); libavahi-client through cgo (ends the static,
cgo-free build); a `<slug>.local` per speaker (nothing needs a per-speaker name people type);
static `.service` files (cover the service only; the name changes at runtime and the browse and
the shared name still need D-Bus); probing a renumbered id over HTTP `/api/info` (same answer for
a silent address, at more cost than a TCP dial); never moving an id before it is stale (a
renumbered speaker stays unreachable for minutes).

## Consequences

- One mDNS stack on the box. Conflicts, probing, announcements, goodbyes and the cache are
  avahi's. The image needs avahi-daemon with its D-Bus interface on the system bus.
- The identity check of a peer is the address filter and the claimed id; it does not
  authenticate a LAN peer against a secret or certificate.
- A host that announces a speaker's id while that speaker is off the network for a moment (a
  reboot) can take its entry until the speaker announces again: the same exposure as after the
  stale window, just sooner.
- The shared name has avahi's normal TTL, so a legacy unicast resolver can keep
  `soundtap.local` pointing at a departed speaker for up to two minutes.
- A DNS-SD instance conflict is renamed by avahi ("Keuken #2"). An AirPlay sender's DACP remote
  is found with `ResolveService` as well.

## Code

- `app/internal/mdns/mdns.go`: `mdns.Responder` (own name, service and shared name),
  `mdns.MaxBrowseDelay`.
- `app/internal/mdns/browse.go`: the browser and its peer table.
- `app/internal/mdns/avahi.go`: the D-Bus calls, `mdns.ResolveService`.
- `app/internal/web/devices.go`: the registry, `Server.SetDevice` and `takeOverIfGone`.
- `app/internal/web/deviceproxy.go`: the device proxy.
