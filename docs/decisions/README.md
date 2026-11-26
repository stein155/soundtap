# Architecture Decision Records

Every architectural choice in this repository gets an ADR here, in the same PR that makes the
choice. The overall design and how these decisions fit together is in
[`../contributors/design.md`](../contributors/design.md).

**Stock is the baseline.** Where the stock firmware had a feature, SoundTap behaves as stock
does: the same buttons, lamps, volume steps, sound, setup, reset and recovery flow. When stock
behaviour is unknown, it is read out of the stock firmware (clean room, outside the repository,
[ADR 0049](0049-no-stock-bytes.md)) before it is built, rather than guessed. "Do it like stock"
needs no ADR. An ADR records a departure from stock, with the reason it was chosen; something
stock did not have; or a hard-to-reverse choice in how it is built. A behaviour that differs from
stock and has no ADR is a bug. The deliberate differences are listed in
[`../contributors/design.md`](../contributors/design.md#stock-behaviour-is-the-baseline).

**What counts as architectural.** A choice that is hard to reverse, crosses a component
boundary, or constrains future work. Examples: a new process on the speaker, a new install or
update route, a change to what runs where on flash, a new external protocol, a new build
pipeline, or a safety boundary. A bug fix or a refactor inside one package is not one.

## Principles

1. **One decision per ADR.** An ADR answers one question. Choices that only make sense
   together are one decision, a section each; a choice that could change on its own gets its
   own ADR.
2. **It says what holds now.** When a decision changes, update its ADR in the same PR, or
   merge the newer ADR into the older one. There are no chains of superseded ADRs, and an ADR
   whose subject SoundTap no longer does is deleted. History lives in git, not in the body.
3. **It cites the code that implements it.** The Code section names the files (repository
   paths such as `app/internal/release/release.go`) and, where it helps, the symbols
   (`release.Verify`, or `app/internal/release/release.go:Verify`). A move or rename updates
   the ADR in the same PR. [`test_adr_code_refs.py`](../../os/tests/docs/test_adr_code_refs.py)
   checks that these references resolve; its docstring gives the exact rules.
4. **No dates or hardware evidence in the body.** The Status line is the one place for a
   date. Measurements, bench runs and per-model proof live in
   [`../hardware/hardware-test.md`](../hardware/hardware-test.md) and the pages it links; an ADR links there
   instead of retelling them.
5. **Numbers stay.** An ADR keeps its number for life. A deleted number is not reused and the
   others are not renumbered, so the gaps in the index are deleted ADRs. Code and docs cite
   only numbers that exist ([`test_adr_numbers_in_code.py`](../../os/tests/docs/test_adr_numbers_in_code.py)).

## Format

Copy [`template.md`](template.md) to `NNNN-short-slug.md`, taking the next free number, and add
a row to the index below with the ADR's own title. A new or rewritten ADR has these parts:

- **Status**: one line under the title, `proposed` or `accepted`, with the date of that state.
- **Context**: the forces at play: what the hardware, the users or the project need.
- **Decision**: what SoundTap does, in one or two paragraphs, and the alternatives that lost, in a
  sentence each.
- **Consequences**: what becomes easier, what becomes harder, and what must not be done now.
- **Code**: where the decision is implemented, as paths and symbols (principle 3).

Keep it short. Add a Mermaid diagram when a picture says it faster.

## Index

| # | Decision | Status |
|---|---|---|
| [0001](0001-soundtapd-one-go-daemon.md) | soundtapd is one Go daemon for every model, on :80 | accepted |
| [0003](0003-apple-home-via-airplay-no-cast-no-sonos.md) | Apple Home through AirPlay 2; no HomeKit bridge, Google Cast or Sonos | accepted |
| [0008](0008-one-universal-stu.md) | One universal STU for every model | accepted |
| [0009](0009-mainline-linux-own-bootloaders.md) | Mainline Linux and SoundTap's own SPL and U-Boot | accepted |
| [0010](0010-ab-slots-boot-confirm-rollback.md) | A/B slots, boot confirmation and rollback | accepted |
| [0011](0011-one-buildroot-pipeline.md) | One Buildroot pipeline, with alpha and production profiles | accepted |
| [0012](0012-single-dac-writer.md) | soundtapd is the only writer to the DAC | accepted |
| [0013](0013-multiroom-through-snapcast.md) | Multiroom through Snapcast | accepted |
| [0014](0014-spotify-through-librespot.md) | Spotify through the librespot session | accepted |
| [0029](0029-speaker-side-timers-and-household-prefs.md) | Speaker-side sleep timer, alarm and multiroom preferences for the multi-device UI | accepted |
| [0031](0031-bco-network-card.md) | The BCO is the ST20's network card and runs SoundTap's own app | accepted |
| [0035](0035-releases.md) | Releases: two lanes, versioned in the repo, built cold, signed in a protected environment | accepted |
| [0040](0040-repository-layout.md) | Repository layout: app/, os/, bco/, docs/ and a slim tools/; Buildroot sees only os/ | accepted |
| [0041](0041-radio-one-ffmpeg-one-url-rule.md) | Radio: one ffmpeg and one media URL rule | accepted |
| [0043](0043-mdns-through-avahi.md) | mDNS goes through avahi-daemon over D-Bus; the device registry holds each speaker id to one address | accepted |
| [0047](0047-wilink8-firmware-and-conf.md) | WiLink8 firmware from linux-firmware, board configuration from wlconf | accepted |
| [0048](0048-signed-updates.md) | A release the speaker fetches installs signed without a press; every upload waits for one | accepted |
| [0049](0049-no-stock-bytes.md) | Bose-free: no Bose files in the repository or the package | accepted |
| [0052](0052-soundtapd-lifecycle.md) | soundtapd lifecycle: one operations lock, ordered shutdown, watchdog | accepted |
| [0055](0055-household-token-and-windows.md) | Who may change things: the home network, the Bluetooth window, a key held on the speaker | accepted |
| [0056](0056-state-flows-by-change.md) | State flows by change: one SSE stream per page | accepted |
| [0063](0063-wifi-lease-and-setup-ap.md) | Wi-Fi: soundtapd's manager, renewed leases and an open, temporary setup access point | accepted |
| [0066](0066-production-hardening.md) | Production hardening: read-only root, inbound filter, unprivileged daemons, bounded logs | accepted |
| [0091](0091-web-api-split-into-lanes.md) | The install lane is its own package; the API has one error shape and a level | accepted |
| [0107](0107-stereo-pair-acts-as-one-speaker.md) | A stereo pair is a multiroom group with a channel per speaker, and acts as one speaker through its leader | accepted |
| [0108](0108-http-api-compatibility-for-1-0.md) | HTTP API compatibility from the first public release | accepted |
| [0109](0109-st300-soundbar-processor-link.md) | The ST300: the Wireless Link's module, and soundtapd speaks to the soundbar processor | accepted |
| [0110](0110-mqtt-bridge.md) | Home automation goes through one optional MQTT bridge with discovery | accepted |
| [0111](0111-st20-standby-clock.md) | The ST20 standby screen shows the time alone | accepted |
| [0113](0113-threat-model.md) | What a stranger can do to a production speaker, and what SoundTap accepts | accepted |
| [0114](0114-bluetooth-agent-in-soundtapd.md) | soundtapd is the Bluetooth pairing agent, over godbus | accepted |
| [0115](0115-shell-only-for-boot.md) | Shell runs the boot and the recovery paths; decisions live in Go | accepted |
| [0116](0116-st300-tv-input-share.md) | The ST300 shares its TV input by capturing the processor's return on McASP0 | accepted |
| [0117](0117-no-automated-speaker-install.md) | No automated job installs on a speaker | accepted |
| [0118](0118-update-button-without-press.md) | A speaker updates itself from the project's releases, without a press | accepted |
| [0119](0119-no-ssh-in-production.md) | Production images have no SSH and nothing replaces it | accepted |
| [0120](0120-on-box-state-migrates.md) | From 0.1.0 on, every update carries the on-box state over | accepted |
| [0121](0121-one-supervisor-per-process.md) | The init script supervises soundtapd; soundtapd supervises the media daemons | accepted |
| [0122](0122-multiroom-group-package.md) | Multiroom group state lives in internal/multiroom, not in the web server | accepted |
| [0123](0123-service-logos-and-tunein.md) | Names and brands: the product is SoundTap; the Spotify and TuneIn logos and TuneIn's open endpoint stay | accepted |
| [0124](0124-third-party-code.md) | Third-party code: libraries are welcome, Bose files never | accepted |
| [0125](0125-time-sources-and-clock-gating.md) | Time comes from the network's NTP servers, then public services that allow devices; nothing asks GitHub before the clock is set | accepted |
| [0126](0126-opt-in-crash-reports-without-a-server.md) | Crash reports are opt-in GitHub issues the owner submits; there is no server | accepted |
| [0127](0127-health-counters-and-alpha-pprof.md) | /api/health counts restarts, underruns and failed writes; alpha images serve pprof on loopback | accepted |
| [0128](0128-bad-block-marking.md) | A slot block that fails to erase or program is marked bad | accepted |
| [0129](0129-first-install-keeps-settings.md) | A first install keeps the speaker's name, presets, Wi-Fi and clock | accepted |
| [0130](0130-usb-stick-recovery.md) | A button chord at power-on installs Update.stu from USB, SoundTap's or Bose's, in SoundTap's U-Boot | accepted |
| [0131](0131-st20-boot-frame.md) | The ST20's U-Boot paints the first boot frame on the OLED | accepted |
| [0132](0132-modules-locked-after-boot.md) | The kernel loads modules only while the speaker boots | accepted |
| [0133](0133-more-soundtouch-models-from-stock.md) | More SoundTouch models, worked out from the stock packages alone | accepted |
| [0134](0134-ipv6-on-the-speaker.md) | The speaker runs IPv6 next to IPv4, behind the same filter | accepted |
| [0135](0135-bco-standard-firmware-features.md) | BCO network card: standard firmware features | accepted |
