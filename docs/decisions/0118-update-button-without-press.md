# 0118. A speaker updates itself from the project's releases; the latest app needs no press

Status: accepted — 2026-11-16

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
- With a tag it waits for the press: an older release could bring back a fixed bug. The UI
  sends the tag in the query too, so it shows the press prompt only then.

### SoundTap Core

`GET /api/releases` also reports `core`: the running version (`/etc/soundtap/os-version`) and
the highest non-draft, not withdrawn `os-v` release the speaker is offered when it is newer: a
prerelease only when the running Core is an alpha itself (ADR 0035). Both lanes share one
release list and its ten-minute cache. A development image whose version is not a release or
alpha version is offered nothing.

`POST /api/core-update` installs that release and only that one, and waits for the press: it
writes the spare slot and the boot chain. The speaker fetches the release's `.sig`, refuses it
unless it is production Core at the tag's version, then streams the `.stu` from GitHub straight
into the install of `POST /api/firmware?install=1` with the plain `install` confirmation.
Nothing is stored whole, and a browser that closes halfway does not stop it. The answer is the
install's report; the Updates page then offers the reboot, and the boot confirmation and
rollback of [ADR 0010](0010-ab-slots-boot-confirm-rollback.md) follow.

Every other route of [ADR 0055](0055-household-token-and-windows.md) keeps its press, the signed
upload `POST /api/firmware?install=1` among them: it takes any signed package, bench builds and
older releases included. Going back to an older Core or to Bose's firmware is such an upload.

Nothing here asks GitHub before the clock is set ([ADR 0125](0125-time-sources-and-clock-gating.md)).

Alternatives that lost: upload only (a production speaker should update itself without a
computer); a second install path inside `internal/update` (the Core-level check, the stored
override and the rollback would exist twice); the press on the tagless app update too (a trip
to the speaker for the routine button).

## Consequences

- The Updates page shows "Update now" for the app and a newer Core next to the uploads; Home
  Assistant's update entity installs the app only.
- A production speaker updates its app from the web UI, an app or a script with nobody at
  the speaker. Someone on the LAN can start that update at a bad moment; the speaker restarts
  on the new release and nothing else changes.
- A Core update takes a click and a press, and the request stays open for the minutes the
  download and the NAND write take.
- A release the owner has not published (a draft, signed or not) or has withdrawn is never
  offered.
- Speakers see releases anonymously only while the repository is public; otherwise the release
  list is unreachable and the UI says so.

## Code

- `app/internal/update/update.go`: `update.Repo`, `Manager.Start` (the tag and the press),
  `Manager.Releases`, `Release.Withdrawn`, `Manager.LatestCore`, `Manager.OpenCore`,
  `admitApp`, `installRelease`.
- `app/internal/web/web.go`: `updateApp` (`POST /api/update`) and `updateCore`
  (`POST /api/core-update`).
- `app/internal/web/about.go:releases`: `GET /api/releases`.
- `app/internal/web/firmware/soundtapd.go:InstallApp` and
  `app/internal/web/firmware/firmware.go:InstallStream`: the shared install steps.
