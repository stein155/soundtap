# 0035. Releases: two lanes, versioned in the repo, built cold, signed offline

Status: accepted — 2026-11-20

## Context

soundtapd with its web UI is to be updated separately from the system it runs on, and there is to
be one repository. That repository builds everything: the Buildroot image, the kernel patches, the
bootloaders, the BCO app, soundtapd and the UI, and changes regularly cut across those layers: a BCO
change can touch the BCO firmware, the `soundtap_bco` kernel driver and soundtapd together, and only
makes sense reviewed as one. Projects that update an application apart from its system (Home
Assistant, Mender, SWUpdate, RAUC, OpenWrt, balenaOS) share one thing: a version contract between
the app and the system. None of them makes separate repositories a condition of separate updates.

A speaker accepts an update only when its signature verifies against the key compiled into
soundtapd ([ADR 0048](0048-signed-updates.md)), so whoever can use the release key can sign
firmware every speaker accepts. A key that CI can read can be read by anything that runs on the
runner while a release job runs.

`build.yml`, which makes the release candidate, runs no tests; CI (`build-stu.yml`) does. An owner
has to be able to tell which layer and which version a speaker runs, and the two lanes must not look
like one version stream.

## Decision

### One repository, two lanes, and a level between them

`stein155/soundtap` is the only repository: everything that runs on a speaker is built and released
from it. It releases on two lanes:

- **SoundTap Core** (tag `os-vX.Y.Z`): everything that boots, delivered as the universal STU
  ([ADR 0008](0008-one-universal-stu.md)).
- **SoundTap app** (tag `app-vX.Y.Z`): soundtapd with the UI embedded, one binary per architecture,
  installed through `/api/soundtapd`, which keeps it in `/mnt/nv` and rolls it back
  ([ADR 0048](0048-signed-updates.md)). CI builds an app release only when `app/` changed.

The contract between them is the Core level. Each image ships `/etc/soundtap/os-api`, one integer,
raised whenever the image changes something the app relies on: a driver's netdev names or ioctls,
a script's interface, a path. Every app build records the level it needs, and `/api/soundtapd`
refuses an app built for a higher one. A Core release always contains the app from the same commit,
so a Core install never needs an app update afterwards.

### Names and versions

The Core release is **SoundTap Core X.Y.Z**, file `SoundTap-Core-X.Y.Z.stu` (with `-sources.tar`,
`.cdx.json` and `.sha256` beside it). The app release is **SoundTap app X.Y.Z**, file
`SoundTap-app-X.Y.Z-armv7.bin`. "SoundTap" alone is the product; "soundtapd" is not shown to users.
The layer is called Core rather than OS because "OS" reads as a claim to be a general-purpose
operating system and collides with the plain word in prose. The user-facing word is Core
everywhere (prose, UI strings, `os-release` `NAME` and `PRETTY_NAME`, release tooling); Go packages,
identifiers, the `os/` directory, the `os-v` tags, `/etc/soundtap/os-api`, `os/VERSION` and JSON
fields keep "os".

The versions live in the repository: `os/VERSION` for Core and `app/VERSION` for the app. A release
is cut by raising the file in a PR; `release.yml` reads it, takes no version input, and refuses a
tag that already exists. Every build says what it is: the image writes the Core version to
`/etc/soundtap/os-version` and `/etc/os-release` and stamps soundtapd with `app-v` plus
`app/VERSION`; `/api/version` returns `os` and `app`, and the UI shows "SoundTap Core X.Y.Z · app
X.Y.Z". A plain `go build` says `dev`. Release notes are generated per lane by
`tools/release/notes.py`.

### What a release may publish

`release.yml` starts with a `ci-passed` job that both lanes need: it looks for a successful
`build-stu.yml` run on `main` on the commit being released, and a commit CI has not passed on is
not published. The same job refuses a commit whose README screenshots were not made from the
released UI ([RELEASING.md](../../RELEASING.md#steps)).

The Core lane publishes a production candidate built by `build.yml` on a hosted runner from an
empty Buildroot output tree; only the download directory, the toolchain and the compiler cache may
carry over, governed by pinned source hashes and ccache's input keys
([ADR 0011](0011-one-buildroot-pipeline.md)). That is what "cold" means here: a fresh output tree,
not an empty compiler cache. `build.yml` records the commit and profile next to the candidate, which
is already named for the version it would be released as; `release.yml` requires that commit to be
the release commit, the profile to be `production`, and the checksum, package structure, sources
and SBOM to pass their gates.

### Signing happens offline

The release key never reaches GitHub. Both lanes of `release.yml` publish a **draft** with the
asset and its `.sha256`; drafts are invisible to speakers. The maintainer runs
`tools/release/sign-release.sh <tag>` on their own machine: it downloads the draft, checks the
digest, signs it with `tools/release/sign.sh` and uploads `<asset>.sig`. The release stays a draft
while the maintainer installs the signed asset on their own speakers through the upload route an
owner uses; `sign-release.sh --publish <tag>` then makes it public (Core: latest; app: never
latest), only when the draft's `.sig` states that tag's kind, version and digest. `sign.sh` refuses
a key whose public half is not `release.PublicKey`, and for a production signature a key that opens
without a passphrase, so a copy of the key file alone cannot sign a release; an alpha (bench)
signature may use a plain key. A CI check fails if `release.yml` mentions the key or `sign.sh`.

A published release is withdrawn by a `Withdrawn: <reason>` line in its notes; soundtapd handles it
as if it were not there ([RELEASING.md](../../RELEASING.md#withdrawing-a-release)).

### Alphas: prereleases before the hardware approval

A version may carry one prerelease suffix, `X.Y.Z-alpha.N`, in `os/VERSION` and `app/VERSION`
alike: N is a positive number of at most nine digits without a leading zero, and no other suffix
is a version. Versions order by semver precedence, an alpha before the release of its number:
`0.1.0-alpha.1 < 0.1.0-alpha.2 < 0.1.0 < 0.1.1-alpha.1`. soundtapd (`release.Version`), the
signed statement and U-Boot's USB-stick check ([ADR 0048](0048-signed-updates.md)), the release
tooling and the installer page read this one grammar. An "alpha" is a kind of release; the
alpha *profile* of [ADR 0011](0011-one-buildroot-pipeline.md) is unrelated: an alpha is always
a production candidate, never an image with SSH.

An alpha goes the route of a release: CI on its commit, the production candidate `build.yml`
built from that commit with its checksum, package structure, sources and SBOM, its own
`CHANGELOG.md` section, the release key under profile `production`, and the maintainer's own
speakers before it is published. Two things differ:

- `release.yml` creates its draft as a GitHub **prerelease**, on both lanes, and
  `sign-release.sh --publish` publishes it as a prerelease, never as latest.
- `sign-release.sh` signs a Core alpha without the four per-model hardware reports, and says
  so when it signs; reports given for an alpha are still checked. A Core release `X.Y.Z` needs
  all four, unchanged.

What a speaker is offered, on each lane: a release (no suffix, and not marked prerelease on
GitHub) is offered to every speaker; a prerelease (an alpha, or any release GitHub marks as a
prerelease) only to a speaker whose running version on that lane is an alpha. So a speaker on a
release is never offered an alpha, by the Updates page, Home Assistant or the Core update; a
speaker on an alpha is offered the highest newer alpha or release, and leaves the alpha line by
itself once a release is out. Installing an app release by its tag or uploading a package is
unchanged: any published release, alphas included, with the press on the speaker
([ADR 0118](0118-update-button-without-press.md)). The first-install feed and the installer
page may name an alpha while no release is out ([RELEASING.md](../../RELEASING.md#the-first-install-feed)).

Alternatives that lost:

- **Separate Core and app repositories (Home Assistant style)**: every cross-layer change would
  become several synchronised PRs, for a one-person project with no separate teams.
- **One combined release only**: the app is to be updatable on its own, and an STU install is slow
  and reboots the speaker.
- **Running the tests again inside `release.yml`**: a second copy of the CI steps drifts from the
  first.
- **The version as a workflow input**: the candidate is built before the release, so the build
  cannot know a number typed in later.
- **`git describe` versions**: unreleased builds would carry names like `os-v0.1.0-12-gabc`, and
  the first build of a release would not know its own tag.
- **The key as a repository secret**: anything that runs on the runner during a release job could
  read it.
- **Alphas from the alpha profile, or signed with a bench key**: the alpha profile has root SSH,
  and a bench signature installs only on alpha images; an owner's speaker would take neither.
- **A prerelease switch in `release.yml` or on the speaker**: the version decides, so the tag,
  the draft, the signed statement and what a speaker is offered cannot disagree.

## Consequences

- Cross-layer changes stay one PR, reviewed and tested together.
- An app update is a small download with no reboot; a Core update is the STU and a reboot.
- Every change to what the image provides must raise the Core level. If one is forgotten, a newer
  app can land on a Core it cannot run.
- A release needs a version bump first; forgetting it fails the release rather than reusing a
  number. The Core and app numbers move independently.
- The published bytes are the tested bytes under the same name, and release hardware tests apply to
  the exact production candidate, including its cache inputs. A cache-free comparison is separate
  evidence (the reproducibility check of [ADR 0011](0011-one-buildroot-pipeline.md)).
- A release waits for CI on `main` to finish for that commit; a commit without a successful push run
  (for example one whose run a newer push cancelled) has to be run again before it can be released.
- Every release needs manual steps by the maintainer (sign, install on the bench speakers,
  publish); until they are taken nothing ships. The steps are in [RELEASING.md](../../RELEASING.md).
- Bench builds are signed with the release key under profile `alpha`, so only alpha images install
  them ([ADR 0048](0048-signed-updates.md)).
- An alpha has no hardware approval: an owner who installs one runs software whose hardware
  checklist has not passed on every model. Its notes say so, and no speaker on a release moves
  onto one by itself.
- Release tooling matches only the `SoundTap-Core-*` and `SoundTap-app-*` asset names.
- Speakers find releases at `stein155/soundtap` (`update.Repo`), anonymously, so they see them only
  once the repository is public.

## Code

- `app/internal/osapi/osapi.go`: the Core level (`osapi.Path`, `osapi.Need`, `osapi.Have`).
- `app/cmd/soundtapd/main.go:checkOSAPI` and `app/internal/web/firmware/soundtapd.go`: an app built
  for a higher level is refused.
- `os/package/soundtapd/soundtapd.mk`: installs the level and stamps soundtapd with `app/VERSION`.
- `os/board/image/post-build.sh`: `/etc/soundtap/os-version` and `os-release`.
- `app/internal/web/about.go:osBuild`: the versions `/api/version` reports;
  `app/frontend/src/lib/version.ts`: the label the UI shows.
- `os/VERSION`, `app/VERSION`, `tools/release/notes.py`.
- `app/internal/release/version.go`: `release.Version`, the version grammar and its order;
  `app/internal/update/update.go:offered`: what a speaker is offered.
- `.github/workflows/release.yml`: the `ci-passed` gate, the candidate checks and the drafts;
  `tools/ci/run-build.sh`: the candidate named for its version, with its commit and profile.
- `tools/release/sign-release.sh`, `tools/release/sign.sh`, `release.PublicKey`: offline signing.
- `os/tests/build/test_release_workflows.py`: names from the VERSION files, the gate, no key in CI.
- `app/internal/update/update.go:Repo`, `update.WithdrawnMarker`: where speakers look, and the
  withdrawal line.
