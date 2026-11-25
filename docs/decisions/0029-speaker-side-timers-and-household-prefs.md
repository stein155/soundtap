# 0029. Speaker-side sleep timer, alarm and multiroom preferences for the multi-device UI

Status: accepted — 2026-10-04

## Context

The web UI controls every SoundTap speaker in the house from one page. It offers a sleep timer, a
wake-up alarm, an on/off switch and two group preferences (keep groups on standby, move the
followers' volume with the leader). A page in a phone browser is the wrong place for anything that
has to happen later: the tab is closed, the screen locks, the battery saver suspends it. There is
no household server: every speaker is its own authority
([ADR 0013](0013-multiroom-through-snapcast.md)).

## Decision

soundtapd owns all of it, per speaker.

### Sleep timer and power

- `GET`/`POST /api/sleep` (`{"minutes":1..240}`, 0 cancels) is an in-memory timer on the speaker.
  On expiry it does what `POST /api/stop` does; a standby that fails is tried again up to three
  times, 10 s apart, and the timer shows as running until it succeeds or the tries run out. It
  does not survive a reboot: a rebooted speaker is already in standby.
- `POST /api/power` `{"on":bool}`: off is `POST /api/stop`; on is `Backend.Wake`, which resumes
  a replayable source (a tuned station, AUX) and otherwise wakes silent.
- Standby means the same thing however it is reached. The speaker state reports every transition
  into standby (the POWER key, auto-off, MQTT, `POST /api/stop`, `POST /api/power`,
  `POST /api/source` STANDBY, the sleep timer, a source session ending) to the web server, which
  cancels a running sleep timer and, when `keepGroups` is off, releases the followers. No path
  to standby skips that.

### Alarm

The alarm is a persisted setting (`alarm: {enabled, time "HH:MM", slot 1..6}`), checked every
20 s by soundtapd in the time zone of the standby-clock town, and plays its preset once per day.
On a night when a clock change skips the alarm minute, it goes off in the first minute after the
jump; on a night when the minute repeats, it goes off once.

### Group preferences

`multiroom: {keepGroups, syncVolume}` is a persisted setting on every speaker. The UI writes the
same values to each one; each speaker acts only on its own copy. A `PUT /api/settings` is one
atomic write: every stored field in the request is validated under the store lock and lands
together or not at all.

- Volume: a `POST /api/volume` on a leader with `syncVolume` on is forwarded only when its body
  says so (`{"volume":N,"group":true}`), which the UI's group slider sends and a per-room slider
  never does. The leader forwards the step (new level minus old), not the level, so each follower
  keeps its own balance, and marks the call with `X-SoundTap-Volume-Sync` so it goes no further.
  Steps that arrive while earlier ones are on their way are summed, and one worker at a time
  applies the sum to each follower, so a fast drag neither loses nor reorders steps.
- Groups: a leader without `keepGroups` tells its followers to stop following on every standby.
- Both are best effort and asynchronous. A stereo pair overrides both: it keeps the follower on
  standby and synchronises group volume ([ADR 0107](0107-stereo-pair-acts-as-one-speaker.md)).

Alternatives that lost: timers in the browser (they die with the tab); a household settings store
on one speaker (a permanent master is what the multi-device design avoids; any speaker must work
alone); absolute volume sync (followers would lose their own balance against the leader).

## Consequences

- Timers and the alarm work with every page closed, and keep working when the phone that set them
  leaves the house.
- Household-wide preferences can disagree for a moment if a speaker was offline when the UI wrote
  them; each speaker then follows the copy it has, a visible and fixable state rather than a
  hidden dependency on a peer.
- Speakers call each other for volume and release as well as invite, so a follower must keep
  accepting `POST /api/volume` and `POST /api/multiroom/follow` from the LAN.

## Code

- `app/internal/sleeptimer`: the timer; `app/internal/web/sleep.go`: `postSleep`, `power`,
  `WatchStandby` and `standbyEntered`.
- `app/internal/web/alarm.go`: `runAlarm`, `checkAlarm` and `alarmDue`;
  `app/internal/settings/settings.go:Alarm`.
- `app/internal/web/groupprefs.go`: `volumeSyncWanted` and `volumeSyncHeader`;
  `app/internal/multiroom/followers.go`: `Group.SyncFollowerVolume` and `Group.ReleaseFollowers`;
  `app/internal/settings/settings.go:Multiroom`.
