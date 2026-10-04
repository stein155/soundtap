# 0127. /api/health counts restarts, underruns and failed writes; alpha images serve pprof on loopback

Status: accepted — 2026-11-21

## Context

The questions asked of a speaker that misbehaved are mostly counts: did soundtapd restart, how
often did librespot die, were there dropouts, did an install or a BCO flash fail, did the boot
confirmation have to wait. The answers were lines in a log that keeps 32 KiB per boot and lives in
RAM (`/tmp` is a tmpfs), so by the time anyone looked they had often rotated away. A production
speaker has no shell to look with ([ADR 0119](0119-no-ssh-in-production.md)).

Profiling soundtapd's memory and CPU on a 128 MiB board needs Go's profiler, and the profiler shows
the process's memory and command line, which a stranger on the LAN must not read
([ADR 0113](0113-threat-model.md)).

## Decision

**Counters.** `app/internal/counters` keeps atomic counts in memory, and `GET /api/health` reports
them under `counters` (the support bundle's resources section prints the same line):

| Field | Counted where |
| --- | --- |
| `soundtapdRestarts` | soundtapd's starts in this boot, less the first: each start adds one to `/run/soundtap/soundtapd-starts` (a tmpfs, so a reboot clears it). The init script's `soundtapd_loop` is untouched. |
| `daemonRestarts` | per media daemon, each exit on its own or failed start in `app/internal/supervise`; a deliberate stop (preempt, follow change) is not one |
| `bufferUnderruns` | the Mixer's jitter buffer running dry while a source plays (`app/internal/audio`) |
| `alsaUnderruns` | `underrun!!!` in aplay's output on its way to the log |
| `installFailures` | firmware installs that were admitted and then failed (`app/internal/web/firmware`) |
| `bcoFlashFailures` | failed flashes of the ST20's BCO image |
| `bootConfirmRetries` | looks of the boot confirmation that found the boot not good yet ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)) |

Every count but `soundtapdRestarts` starts over when soundtapd does; `sinceSeconds` says how long
that is. Counting costs one atomic add and writes nothing to flash, so no file is added to
`/mnt/nv`. A count that must outlive a reboot stays where it already is: how the previous boot
ended (`last-boot/`), the BCO image marker's attempt count, U-Boot's boot count.

**pprof.** On an alpha image (`/etc/soundtap/profile` says `alpha`; post-build writes it into the
read-only rootfs) soundtapd serves `net/http/pprof` on `127.0.0.1:6060`, on a mux of its own. It is
reached over the alpha image's SSH:

```sh
ssh -L 6060:127.0.0.1:6060 root@<speaker>
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
```

A production image serves no profiler: it has no SSH to reach loopback with, and the profile file
cannot change without root on a read-only rootfs. A port already in use is logged and the speaker
runs on without it.

Alternatives that lost: persisting the counts on `/mnt/nv` (a write per event on a small UBI
volume, for numbers that matter per boot); pprof under `/api/debug` behind the press on the speaker
(one press per request, and a profile is several); pprof on every image behind the press (a
production image would carry a memory reader on the LAN for the bench's sake).

## Consequences

- `/api/health` is open to the LAN; the counts say how a speaker is doing, not what it plays or
  where it is, like the rest of that answer.
- A new event worth counting is one field in `counters.Snapshot`, its increment, the OpenAPI
  schema and the regenerated UI types.
- The binary carries `net/http/pprof` on production images too, unused.

## Code

- `app/internal/counters`: the counts, `counters.Take`, `counters.Matches` (aplay) and
  `counters.CountStart` (soundtapd's starts).
- `app/internal/web/health.go:healthCounters`: the `counters` object of `/api/health`.
- `app/cmd/soundtapd/observe.go`: `countSoundtapdStart`, `startPprof` and `pprofWanted`.
