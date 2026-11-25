# 0118. A speaker updates itself from the project's releases, without a press

Status: accepted — 2026-11-25

## Context

Releases come in two lanes, the SoundTap app (`app-v*`, soundtapd with the UI) and SoundTap
Core (`os-v*`, an STU) ([ADR 0035](0035-releases.md)). Without a route of its own a speaker
gets a release only when its owner hears about it, downloads the asset and its `.sig` and
uploads both; a production image has no SSH ([ADR 0119](0119-no-ssh-in-production.md)), so a
fix reached nobody by itself.

Dangerous routes wait for a key held on the speaker ([ADR 0055](0055-household-token-and-windows.md)).
Applied to the routine Update button, that made every app update a trip to the speaker. The
latest app release is a narrow target: the update manager fetches only tags the project
publishes, and only a package signed with a release key installs
([ADR 0048](0048-signed-updates.md)), so someone who presses the button can only move the
speaker to that release.

## Decision

### The app

On an installed speaker `internal/update` follows the app lane. It lists the `app-v*`
releases of this repository (`update.Repo`) that are neither drafts nor withdrawn (a release
whose notes have a line starting with `Withdrawn:`,
[RELEASING.md](../../RELEASING.md#withdrawing-a-release)); the highest one the speaker is
offered is "latest": a prerelease only when the running app is an alpha itself
([ADR 0035](0035-releases.md#alphas-prereleases-before-the-hardware-approval)). It downloads
`SoundTap-app-<version>-armv7.bin` and its `.sig` and checks the statement before anything
else: `kind=app`, `profile=production`, and the tag's own version.

The verified binary goes through the install step of an upload (`firmware.Handler.InstallApp`):
the ELF and module check, the Core-level contract ([ADR 0035](0035-releases.md)), the gzipped
override on `/mnt/nv` and the init script's trial and rollback
([ADR 0121](0121-one-supervisor-per-process.md)). There is one install path, whichever way the
binary arrived. The download is staged on the tmpfs the app runs from, the upload's own staging
file; `/tmp` is too small.

- `POST /api/update` without a tag, and Home Assistant's update entity, install the latest
  release **without a press**, and never an older one.
- With a tag it may install an older release, also **without a press**: the speaker fetches
  it from the project's releases itself and installs it only when its `.sig` verifies
  ([ADR 0048](0048-signed-updates.md)). Naming the tag is what asks for the older one.

### SoundTap Core

`GET /api/releases` also reports `core`: the running version (`/etc/soundtap/os-version`) and
the highest non-draft, not withdrawn `os-v` release the speaker is offered when it is newer: a
prerelease only when the running Core is an alpha itself (ADR 0035). Both lanes share one
release list and its ten-minute cache. A development image whose version is not a release or
alpha version is offered nothing.

`POST /api/core-update` installs that release and only that one, without a press: the
speaker fetches it itself and it is signed ([ADR 0048](0048-signed-updates.md)). It writes the spare slot and the boot chain. The speaker fetches the release's `.sig`, refuses it
unless it is production Core at the tag's version, then streams the `.stu` from GitHub straight
into the install of `POST /api/firmware?install=1` with the plain `install` confirmation.
Nothing is stored whole, and a browser that closes halfway does not stop it. The answer is the
install's report. Once the new slot is written and selected and the report saved, the speaker
restarts into it on its own, a moment after the answer (`restarting: true`); it never restarts
after a failure, nor when the report could not be saved. The boot confirmation and rollback of
[ADR 0010](0010-ab-slots-boot-confirm-rollback.md) keep the way back. `POST /api/reboot`
itself keeps its press for a manual restart.

On the Updates page, *Update* on a newer app or Core asks once in a short sheet (it takes a few
minutes and the speaker restarts; Start or Cancel), as the Bose app asks, and then starts with
no press. The page over the app then follows the speaker away and back, for the app and for
Core.

The uploads `POST /api/firmware?install=1` and `POST /api/soundtapd` wait for the press for
every package, signed or not: the `.sig` of a public release can be sent with another body,
and the STU route writes the body into the fallback slot before it compares the digest
([ADR 0048](0048-signed-updates.md), [ADR 0055](0055-household-token-and-windows.md)). Every
other route of ADR 0055 keeps its press.

Nothing here asks GitHub before the clock is set ([ADR 0125](0125-time-sources-and-clock-gating.md)).

Alternatives that lost: upload only (a production speaker should update itself without a
computer); a second install path inside `internal/update` (the Core-level check, the stored
override and the rollback would exist twice); the press on the tagless app update too (a trip
to the speaker for the routine button); the press on a picked or older release and on the Core
update (until 2026-11-25: the owner decided a fetched signed release is enough, and accepted that
anyone on the LAN can put back an older one); a separate reboot button after a Core update,
behind its own press (a release should update after one question in the app); no press on a signed upload (until
2026-11-25: a replayed `.sig` let anyone on the LAN wipe the fallback slot).

## Consequences

- The Updates page shows "Update now" for the app and a newer Core next to the uploads; Home
  Assistant's update entity installs the app only.
- A production speaker updates its app from the web UI, an app or a script with nobody at
  the speaker. Someone on the LAN can start that update at a bad moment; the speaker restarts
  on the new release and nothing else changes.
- A Core update takes one confirmation in the app, and the request stays open for the minutes the download and
  the NAND write take; the speaker then restarts by itself, so playback stops without a
  further question. Someone on the LAN can start that at a bad moment too.
- Anyone on the LAN can install any published, not withdrawn release, an older one included,
  with nobody at the speaker ([ADR 0113](0113-threat-model.md)).
- A release the owner has not published (a draft, signed or not) or has withdrawn is never
  offered.
- Speakers see releases anonymously only while the repository is public; otherwise the release
  list is unreachable and the UI says so.

## Code

- `app/internal/update/update.go`: `update.Repo`, `Manager.Start` (the tag),
  `Manager.Releases`, `Release.Withdrawn`, `Manager.LatestCore`, `Manager.OpenCore`,
  `admitApp`, `installRelease`.
- `app/internal/web/web.go`: `updateApp` (`POST /api/update`) and `updateCore`
  (`POST /api/core-update`).
- `app/internal/web/about.go:releases`: `GET /api/releases`.
- `app/internal/web/firmware/soundtapd.go:InstallApp` and
  `app/internal/web/firmware/firmware.go:InstallStream`: the shared install steps, and
  `restartAfterInstall`, the restart after a Core update.
- `app/frontend/src/hooks/useCoreUpdate.ts`, `useSelfUpdate.ts`: the updates the Updates page
  follows, started after its one question
  (`components/organisms/settings/UpdateSettings/UpdateConfirm.tsx`).
