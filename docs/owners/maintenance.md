# Maintenance

SoundTap is a spare-time project for speakers Bose no longer maintains. This page says what you
can expect from it.

## Supported speakers

The [compatibility page](compatibility.md) records the evidence for each feature per model. A
model marked *Supported* in the README is one SoundTap is released for: a release is published
only after the exact candidate has passed that model's [hardware test](../hardware/hardware-test.md).
Earlier observations or a partly completed checklist do not count as release approval.

## Releases

There is no fixed schedule. A release is a build from `main` that has passed the install gate and
the hardware test; [RELEASING.md](https://github.com/stein155/soundtap/blob/main/RELEASING.md)
has the steps. Published releases are signed, and production images refuse unsigned updates.
Alpha images accept unsigned builds only after an explicit development opt-in
([ADR 0048](../decisions/0048-signed-updates.md)).

## Security

During alpha, fixes target the latest release of each lane. From 1.0, the previous minor
also receives security and data-loss fixes for 90 days after a new minor release
([ADR 0108](../decisions/0108-http-api-compatibility-for-1-0.md)). Report a problem as
[SECURITY.md](https://github.com/stein155/soundtap/blob/main/SECURITY.md) describes; it also sets
the response times. Security fixes come before other work.

## Dependencies

SoundTap builds on Buildroot's long-term line, Linux, U-Boot, librespot, shairport-sync, snapcast
and others, all pinned to exact versions. A dependency change must be tested on all four models
(ST10, ST20, Wireless Link and ST300) before a release; the
[open hardware checks](../hardware/hardware-test.md#open-hardware-checks) list what is still
outstanding.

Spotify, TuneIn and Apple can change their services without notice. When a service closes a route,
that feature can stop working until the project catches up, or for good. Spotify search is an
example: the search action on the speaker's page opens Spotify's own search. The best-effort
`/api/spotify/search` route uses the speaker's Spotify session, which Spotify can refuse, so
catalogue search is not guaranteed.

## Going back

Every supported model has a documented way back to Bose's firmware;
[the install guide](install.md#going-back-to-bose) says what each one needs before you start. If the project stops, the speakers keep working on your
network, and the source stays available under its licence.
