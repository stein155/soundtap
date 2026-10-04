# 0056. State flows by change: one SSE stream per page

Status: accepted — 2026-10-21

## Context

The speakers have one core and 128 MiB, and they play audio while they serve. Two kinds of polling
cost them for nothing:

- Inside soundtapd, the loops that follow speaker state (the player, source routing, amp
  ownership) would poll `speakerstate` every 20 to 50 ms, standby included, each poll taking the
  speaker lock: over 200 wakeups a second. In standby the mixer writes silence, and resampling it
  was over half of soundtapd's standby CPU on the ST20 (measurement under "Measuring CPU" in
  [the hardware test](../hardware/hardware-test.md#measuring-cpu)).
- The home screen shows every speaker in the household. Polling each room's `/api/now` and
  `/api/volume` through the serving speaker's `/api/devices/{id}/` proxy grows with the number of
  speakers and with every open tab. Each speaker has a push stream, `/api/events`, but a browser
  opening one per room hits the HTTP/1.1 limit of six connections per origin: every room goes
  through the same origin, the serving speaker.

## Decision

### Loops wait for a change

`speakerstate.Speaker` takes its lock through `lock`/`unlock`. `unlock` compares the fields the
loops act on (source, play status, location, volume, bass, mute, selection, playback attempt, IP)
with what `lock` saw and, when they moved, closes the channel `Changed()` handed out. A loop takes
`Changed()` before it reads the state, so a change between the read and the wait is never missed.

- Source routing and the player wait on `Changed()` (the player also on its retry backoff and its
  decoder channels); neither has a ticker.
- Amp ownership steps every 50 ms while anything is in motion (audio, an announcement, the amp or
  mute sequence). Settled in standby it waits on `Changed()`, on a wake from an announcement, or
  one second for what nothing signals (amp faults).
- The resampler skips interpolation for an all-zero block: the same number of zero frames and the
  same position afterwards. A test holds it byte- and state-equal to interpolation.

The keypad poll stays: the [ST10 hardware contract](../hardware/st10/hardware-contract.md) requires a
userspace-polled keypad. The LED, display and codec loops keep their own timing.

### The home screen follows every room on one stream

The serving speaker offers `GET /api/household/events`. While at least one browser listens, it
follows its own event hub and opens one `/api/events` stream to each online, fresh peer in its
device registry (at most 16), reconciling that set every 10 s and reopening a dropped stream after
3 s. Each push goes out as `event: state` with `data: {"id": "<peer id>", "state": {...}}`, or
`{"self": true, ...}` for the serving speaker. A peer's payload must be valid JSON or it is
dropped. Updates coalesce per speaker for a slow browser, and the last state of each room is
replayed to a new one. With no browser listening, every upstream stream is closed and the cache
dropped.

Each speaker's `/api/events` state carries two more fields, so the stream also carries the
household's groups and device list:

- `followers`: the device ids of the speakers connected to its snapserver, other than itself, as
  `GET /api/multiroom` reports them. Omitted when the snapserver could not be read; `[]` when
  nobody listens.
- `devices`: a revision of its device registry (each peer's id, name, model, firmware, address
  and online flag) and of its own name, so it changes when a speaker appears, leaves, is renamed
  or renumbered.

The UI's `useRooms` subscribes to this stream. It takes groups from `followers`, asks
`/api/multiroom` only of a speaker that has not reported them (and twice right after a group
change made on the page), and re-reads `/api/devices` when the serving speaker's `devices`
revision changes. Room state, groups and devices are polled only while the stream is down.

Alternatives that lost: one `EventSource` per room through the proxy (the six-connection limit);
CORS and direct browser-to-peer streams (every peer must be reachable from the phone by `.local`
or IP, the weak spot the device proxy exists to avoid); separate SSE event types for groups and
devices (more protocol for data the state snapshot already coalesces); slower polling (still
rooms times tabs, and a laggier UI).

## Consequences

- A state field outside the compared set that a loop starts depending on must be added to it, or
  the loop sees the change late (amp ownership) or not until another change (player, routing).
- One connection per tab regardless of household size, and no periodic room, group or device
  requests while it is open. A peer serves one extra `/api/events` subscriber per speaker that has
  a home screen open, shared through its hub.
- A peer's state reaches the browser in two hops; its hub's 1.5–3 s cadence bounds the delay, and
  a group change shows within one hub poll. Each hub poll asks the local snapserver for its status,
  one loopback JSON-RPC call while anyone listens.
- Speakers follow only a peer's `/api/events`, never its household stream, so the fan-in cannot
  loop.
- The settings file's schema version and migrations are part of the on-box state rules of
  [ADR 0120](0120-on-box-state-migrates.md), not of this decision.

## Code

- `app/internal/speakerstate/changed.go`: `Speaker.Changed`, `lock` and `unlock`.
- `app/cmd/soundtapd/audio_owner.go`, `app/cmd/soundtapd/player_setup.go`: loops that wait on
  `Changed()`.
- `app/internal/audio/resample.go`: `skipSilence`.
- `app/internal/household/household.go`: the fan-in (`New`, `Household.Run`, `Household.Subscribe`);
  `app/internal/web/household.go:householdEvents` serves it.
- `app/internal/web/events.go`: `/api/events` and `addHouseholdState` (`followers`, `devices`).
- `app/frontend/src/hooks/useRooms.ts`: the UI's subscriber.
