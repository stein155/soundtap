# 0126. Crash reports are opt-in GitHub issues the owner submits; there is no server

Status: accepted — 2026-11-21

## Context

A speaker that crashed keeps what it knows: soundtapd's last panic in `panic.log`
([ADR 0052](0052-soundtapd-lifecycle.md)), the previous boot's ramoops records in `last-boot/`
when the kernel panicked or oopsed, and soundtapd's supervisor knows when a media daemon
crash-looped ([ADR 0121](0121-one-supervisor-per-process.md)). Nobody hears of it unless the
owner downloads the support bundle and files an issue, which few do.

A crash collector would need a server, an account to run it and a privacy promise for what it
receives. SoundTap has no server and no telemetry ([FAQ](../owners/faq.md#does-it-phone-home)), and the
owner decided that crash reports get none either.

## Decision

**Off by default.** The owner turns *Offer crash reports* on in Settings, this speaker, Advanced
Setup, System. It is stored in the settings file (`crashReports.enabled`, absent from an older file,
which reads as off) and carried by a backup.

**The speaker finds the crash, the owner sends it.** With reports on, `GET /api/crash-report`
answers the first crash not yet reported or dismissed, in this order:

| Kind | Found in | Id |
| --- | --- | --- |
| `panic` | `panic.log`, soundtapd's last panic | `panic-` and a hash of the file |
| `kernel` | `last-boot/how` says `panic` or `oops`; the oops record is quoted when there is one | `kernel-` and a hash of the records |
| `daemon` | a media daemon put to rest after 20 short runs in a row (`counters.CrashLoops`) | `daemon-` and a hash of the boot id and the daemon |

The answer carries the issue's title and body and a link to
`https://github.com/stein155/soundtap/issues/new?title=…&body=…`. The web UI shows a notice on
the speaker's settings page; *Report this crash* shows the title and body word for word first,
and only *Open the report on GitHub* lets them leave, as a link the owner opens in their own
browser and submits there, signed in to GitHub as themselves. The speaker contacts nothing.

**What the report says.** What happened (a sentence), the app version, the Core version and
commit, the model, the speaker's and soundtapd's uptime, the crash id, and the trace: the panic
and its stack, the end of the kernel record, or the daemon's last exit and the agent log's lines
about it. The title, the sentence and the trace go through the support bundle's redactor
(`app/internal/web/redact.go`) first, so Wi-Fi names and keys, the MQTT password, Spotify's login,
tokens and public addresses become placeholders. The trace is quoted from its start for as long
as the link stays within 6000 bytes (browsers and GitHub refuse or cut much longer ones), each line
at most 240 bytes; a cut says so. The body asks the owner to attach the support bundle, which has
everything else and needs a press on the speaker.

**Offered once.** *Open the report on GitHub* records the crash as reported, *Dismiss* as dismissed
(`POST /api/crash-report`); either way its id goes into `crashReports.handled` in the settings file
(the last 32), and the speaker does not offer it again, across restarts and updates. A new panic,
another kernel crash or a loop in a later boot has a new id. The POST accepts only an id the
speaker currently holds.

Alternatives that lost: a collector the speaker posts to (a server, and data leaving without the
owner reading it); the full logs in the issue (far over any link length, and the bundle exists);
reports on by default with an opt-out (the owner decided against anything leaving by default).

## Consequences

- `GET /api/crash-report` is open to the home network, like `/api/health`: once the owner turned
  reports on, anyone on the LAN can read the redacted report. It holds less than the support
  bundle, which needs a press because it carries the whole logs
  ([ADR 0113](0113-threat-model.md)). While reports are off it answers `crash: null` without
  looking.
- The issue is public on GitHub once submitted; the owner reads it first, and the redactor is the
  same one the support bundle is held to (`support_bundle_test.go`, `crashreport_test.go`).
- Nothing new is written to `/mnt/nv`: the opt-in and the handled ids are two fields of the settings
  file ([nv-state](../contributors/nv-state.md)).

## Code

- `app/internal/web/crashreport.go`: `getCrashReport`, `postCrashReport`, `findCrashes` and the
  report text (`crashHead`, `crashIssue`).
- `app/internal/settings/settings.go:CrashReports`: the opt-in and the handled ids.
- `app/internal/counters`: the daemons' crash loops, recorded by `app/internal/supervise`.
- `app/frontend/src/components/organisms/settings/CrashReport`: the notice, the preview and the switch.
