# Maintenance

SoundTap is a spare-time project that runs on speakers Bose no longer maintains. This page says
what that means for you, without promising more than one project can keep.

## Supported speakers

The [compatibility page](compatibility.md) records feature evidence per model. The README's
alpha support label means daily use on the maintainer's bench speakers; release support additionally
requires the exact candidate to pass the [hardware test](../hardware/hardware-test.md) on that model.
A checklist or historical observation alone is not release approval.

## Releases

There is no fixed schedule. A release is a build from `main` that has passed the install gate and
the hardware test; [RELEASING.md](https://github.com/stein155/soundtap/blob/main/RELEASING.md)
has the steps. Published releases are signed; production images refuse unsigned updates. Alpha builds
have an explicit development opt-in for unsigned candidates (ADR 0048).

## Security

During alpha, fixes target the latest release of each lane. From 1.0, the previous minor
also receives security and data-loss fixes for 90 days after a new minor release
([ADR 0108](../decisions/0108-http-api-compatibility-for-1-0.md)). Report a problem as [SECURITY.md](https://github.com/stein155/soundtap/blob/main/SECURITY.md)
describes; it also sets the response times. Security fixes come before other work.

## Dependencies

SoundTap builds on Buildroot's long-term line, Linux, U-Boot, librespot, shairport-sync, snapcast
and others, all pinned to exact versions. Dependency changes are deliberate and need testing on all four
models (ST10, ST20, Wireless Link and ST300) for release approval; the [open hardware checks](../hardware/hardware-test.md#open-hardware-checks)
record validation that is still outstanding.

Spotify, TuneIn and Apple can change their services without notice. When a service closes a route,
that feature can stop working until the project catches up, or for good. Spotify search is an
example: the in-app search action opens Spotify's own search. The server's best-effort
`/api/spotify/search` route uses the device session and can be refused by Spotify; it is not
a promise of working catalogue search.

## Going back

Every supported model has a documented way back to Bose's firmware; [the install guide](install.md#going-back-to-bose)
says what each one needs before you start. If the project stops, the speakers keep working on your
network, and the source stays available under its licence.
