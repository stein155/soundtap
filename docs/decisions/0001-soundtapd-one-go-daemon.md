# 0001. soundtapd is one Go daemon for every model, on :80

Status: accepted — 2026-10-21

## Context

The smallest board sets the budget. The ST10 is a TI AM335x: one ARM Cortex-A8 core at about
1 GHz (ARMv7 hard-float), 128 MB of RAM for everything (`mem=128M`), shared with shairport-sync,
nqptp, librespot, bluealsa, avahi, wpa_supplicant and ffmpeg, and a root volume with tens of
megabytes free. The audio loop mixes 20 ms blocks with bounded buffering for scheduling jitter
([ADR 0012](0012-single-dac-writer.md)), so runtime overhead and memory matter. Rewriting
soundtapd in Node.js, the maintainer's strongest language, was proposed.

One rootfs serves every model ([ADR 0008](0008-one-universal-stu.md)), so there is one daemon
binary. On every model the LAN reaches the main CPU directly: on the ST10, Wireless Link and ST300
through their own Wi-Fi, on the ST20 through SoundTap's BCO app, which gives the SCM its own `wlan0`
([ADR 0031](0031-bco-network-card.md)).

## Decision

### soundtapd stays in Go

soundtapd is written in Go and is not ported to Node.js. Go keeps the static ARM build and the
tested audio path. Neither Go nor Node gives hard real-time scheduling or a GC guarantee, and no
comparative Node measurement exists on these speakers; a rewrite would need its own memory,
timing and source-handover measurements on the 128 MB boards and a new runtime in the image. The
frontend is TypeScript, where that skill applies without a runtime on the device. The choice is
tied to this hardware: on a board with two cores and 512 MB it would be open again.

### One soundtapd for every model, on :80

One process, `soundtapd`, provides the JSON API, the React web UI, source control, keypad, IR,
LEDs, display, updates and multiroom on every model. It binds `:80` only and advertises `:80`
(mDNS, UPnP, `/api/devices`); `-listen` overrides the address for development. The web UI calls
the API same-origin; the loopback agents (`airplay-source.sh`, `spotify-source.sh`) call
`http://127.0.0.1`.

The model is detected at runtime from the board's variant (`/proc/variant` or `variant=` on the
kernel command line), with the init script's `MODEL` as the fallback, not compiled in. Hardware
differences are data in the model table: GPIO numbers, codec config, keypad geometry, LED map and
user sources.

Alternatives that lost: a Node.js rewrite (a runtime and unmeasured memory and timing on 128 MB);
per-model binaries or build tags (four artifacts that drift); a second port such as `:8080`, which
the stock BCO forwarded (a second address every integration must know, for a route SoundTap does
not use).

## Consequences

- One URL for every speaker: `http://<speaker>/`. The web UI and API are the same everywhere; a
  capability a model lacks is hidden, not forked.
- The Go code is held to cohesive packages, narrow interfaces and tests that describe behaviour.
- `POST /api/soundtapd` can replace the running binary in `/run/audio-bin/app` for testing without an
  STU. The accepted upload is kept as an override under `/mnt/nv/soundtap` and runs at every boot
  while it matches the Core base; a failed trial or a changed Core base returns to the built-in
  binary ([ADR 0035](0035-releases.md)).
- An ST20 on the stock BCO firmware reaches SoundTap only over the USB link; that is not
  supported.

## Code

- `app/cmd/soundtapd/main.go`: the daemon; `app/cmd/soundtapd/main.go:defaultListenAddr` and
  `app/cmd/soundtapd/network.go:listenAddrs`.
- `app/internal/hw/model/model.go:Current`: the model from the kernel variant or `MODEL`.
- `app/internal/modeltable`: the per-model data.
- `app/internal/web/firmware/soundtapd.go`: `POST /api/soundtapd` and the override.
