# 0121. The init script supervises soundtapd; soundtapd supervises the media daemons

Status: accepted — 2026-11-13

## Context

Restarting a crashed process needs a rule, and two supervisors with different rules for the media
daemons, talking through marker files (one side writing a restart marker and killing a process the
other owns), produce bugs in the gap between them: a process restarted forever, a second
supervisor started by a restart, a requested restart counted as a failure. Shell has no unit tests,
and [ADR 0115](0115-shell-only-for-boot.md) keeps shell to the boot and to what must run without
soundtapd.

These speakers have 128 MB and no swap. An unbounded restart loop can take a speaker down, so every
restart loop needs a backoff and a limit. A limit that turns a process off for good loses too much:
librespot with stored credentials exits within seconds of a start that cannot reach Spotify, so any
internet outage longer than the limit would end Spotify Connect until someone intervened.

## Decision

Each process has exactly one supervisor, and there are two supervisors:

| Supervisor | Processes | Why there |
|---|---|---|
| init script, `soundtapd_loop` | soundtapd | Something has to restart soundtapd itself. Its loop also drops a failing uploaded override and reboots an unconfirmed boot towards rollback ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)). |
| init script, `start_root_helper` | soundtap-root | The root helper soundtapd asks for what its own user may not do ([ADR 0066](0066-production-hardening.md)). Restarted 2 s after it ends, forever: soundtapd's requests fail while it is down and work again once it is back. |
| soundtapd, `supervise.Run` | shairport-sync, librespot, nqptp, snapserver, snapclient | They only matter while soundtapd runs: soundtapd writes the audio they serve and reads the audio they bring. |

Everything else the init script starts runs once and is not restarted by it: the Bluetooth stack
(`bt.sh`, with its own bound), dbus, avahi, the alpha SSH server. Wi-Fi and the setup AP are
soundtapd's ([ADR 0063](0063-wifi-lease-and-setup-ap.md)); wpa_supplicant and hostapd outlive a
soundtapd restart, and the next soundtapd adopts them.

**soundtapd's rule** is one rule for every process it supervises: the wait doubles from 3 s to at
most a minute; a run of 30 s or more resets it; after 20 shorter runs in a row (about 16 minutes)
the process rests for 10 minutes, then the count starts over. A start that fails counts as a short
run. A restart soundtapd asks for itself (a source change, following another speaker, the source
becoming MULTIROOM again) is not a failure: the next run starts at once and the backoff starts
over. soundtapd starts its children so that they die with it, so a new soundtapd never finds an
old child still running.

soundtapd itself runs as the user `soundtapd`: `soundtapd_loop` starts it through
`soundtap-root exec-soundtapd`, which drops to that user with soundtapd's capabilities and execs it
in place, so the loop's pid is soundtapd's and its liveness kill reaches it as before. soundtapd
starts the media daemons as their own users with `CAP_SETUID` and `CAP_SETGID` instead of as root,
and stops them with `CAP_KILL`.

**The init script prepares only what needs root and the boot order:** it writes
`/run/soundtap/snapserver.conf` and hands it over in `SOUNDTAP_SNAPSERVER_CONF`, and exports
`SOUNDTAP_AIRPLAY_MODE` and `SOUNDTAP_SNAPCLIENT_ID`. soundtapd builds snapclient's command line at
every start from the follow setting, so following another speaker is a plain restart, and starts it
as the `snapcast` user itself: through BusyBox `setuidgid` the kernel would clear the parent-death
signal and leave an orphan behind a soundtapd restart.

**`soundtapd_loop`** gives up only by rebooting an unconfirmed boot. A confirmed soundtapd that
keeps crashing is restarted forever, because giving up would leave the speaker with no API and no
way in short of a power cycle. The wait doubles from 2 s to at most 30 s while runs stay short, and
a run over 60 s starts it at 2 s again.

Alternatives that lost: everything under the init script (the policy would stay in untested shell,
and soundtapd would still need to restart a snapclient it does not own); keeping the split and only
adding a limit to the Go side (two rules and the marker-file handshake stay); a separate supervisor
such as runit or a second Go binary (a new resident process on a 128 MB board for work two existing
loops do).

## Consequences

- One restart rule with unit tests, and no marker files between the init script and soundtapd.
- A soundtapd restart restarts snapserver, snapclient and nqptp too. A multiroom group loses a
  second or two of audio, which it loses anyway: the leader's mixer is inside soundtapd. nqptp
  restarts with shairport-sync, which registers with nqptp only at its own start.
- Stopping soundtapd stops the media daemons. Never stop the init script over SSH on a speaker
  whose only link is Wi-Fi: it holds wpa_supplicant.

## Code

- `app/internal/supervise/supervise.go`: `Run`, `restartPolicy` and `Processes.Restart`.
- `app/internal/childproc/childproc_linux.go`: `DieWithParent` and `RunAs`.
- `app/cmd/soundtapd/procs.go:snapclientArgs`: snapclient's command line at every start.
- `os/overlay/etc/init.d/soundtap`: `soundtapd_loop` and the snapserver configuration.
