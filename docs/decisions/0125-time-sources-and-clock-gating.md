# 0125. Time comes from the network's NTP servers, then public services that allow devices; nothing asks GitHub before the clock is set

Status: accepted — 2026-11-20

## Context

The speakers have no battery-backed clock that survives a power cut. At boot the clock is the
one saved on flash at the last shutdown (`/mnt/nv/soundtap-clock`), which after a power cut can
be hours or days behind, until NTP sets it.

The NTP Pool's terms do not let a distributed device use the pool's default zone
(`pool.ntp.org`): a vendor asks for a zone of its own (`<vendor>.pool.ntp.org`), and SoundTap has
none.

The update check asks the GitHub API over TLS, which checks GitHub's certificate against the
speaker's clock. Before NTP has answered, that check fails with an error that says nothing useful
to the owner, or, with a clock far enough behind, accepts a certificate that is no longer valid.
The standby clock and the alarm already wait for the first sync
([ADR 0111](0111-st20-standby-clock-and-weather.md)).

## Decision

### Where the time comes from

In order:

1. The NTP servers the DHCP lease names (option 42). Every `udhcpc` asks for them
   (`-O ntpsrv`) and the udhcpc hook keeps them in `/run/soundtap/ntp-servers`. A network that
   blocks outside NTP usually names its own.
2. The owner's own servers in `/mnt/nv/ntp-servers`, when that file names any
   ([nv-state.md](../contributors/nv-state.md)). Only words that can be a host name or an address are used.
3. Otherwise the default: `time.cloudflare.com` and `time.google.com`. Both are free for any
   device to use without registration, and two operators mean one outage leaves the other.

`NTP_VENDOR_ZONE` in `clock.sh` is empty. Should the NTP Pool grant SoundTap a vendor zone
(`soundtap.pool.ntp.org`), setting it is the one change: its names then come first in the
default. The pool's default zone is never used.

### Nothing asks GitHub before the clock is set

Every release lookup of `update.Manager` (the app and Core lanes, the web API and the MQTT update
entity) first waits for the first NTP sync since boot, at most 3 s, and otherwise returns
`update.ErrClockNotSet`. Only on a speaker: a development run keeps its host's clock.
`GET /api/releases` then answers 200 with `unavailableCode: clock_not_set`, `POST /api/update`
and `POST /api/core-update` answer 503 with the error code `clock_not_set`, and the UI says the
speaker's clock is not set yet. The MQTT bridge asks again every minute, at most 30 times, before
its six-hourly check takes over. The wait never holds up boot.

Alternatives that lost: `pool.ntp.org` (against the pool's terms for a distributed device);
waiting for a vendor zone (not granted; the constant leaves room for it); one provider only (one
outage leaves the speaker without time); letting the update check fail with the TLS error (it
tells the owner nothing, and a wrong clock can make the check pass where it should not).

## Consequences

- A speaker on a network that blocks outside NTP and names no server of its own has no synced
  clock until the owner writes `/mnt/nv/ntp-servers`; until then it shows no standby clock,
  skips the alarm and does not check for updates.
- The speaker contacts Cloudflare and Google for the time unless the network names its own
  servers; `docs/owners/faq.md` lists them with the other outside contacts.
- Google smears leap seconds and the other servers may not. That only matters during a leap
  second.

## Code

- `os/overlay/opt/soundtap/clock.sh`: the server order and `NTP_VENDOR_ZONE`.
- `os/overlay/usr/share/udhcpc/default.script.d/soundtap-log`: keeps the lease's NTP servers.
- `app/internal/clocksync/clocksync.go`: `clocksync.Synced`, the first-sync mark.
- `app/internal/update/update.go`: `update.ErrClockNotSet` and `update.Manager` (`clockSet`).
- `app/internal/web/about.go`: the `clock_not_set` answers.
- `app/internal/mqttbridge/discovery.go`: the bridge's retries while the clock is not set.
