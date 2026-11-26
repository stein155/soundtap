# Releasing

This repository releases on two lanes ([ADR 0035](docs/decisions/0035-releases.md)):

- **SoundTap Core**, the universal STU `SoundTap-Core-X.Y.Z.stu`. The same package installs on the
  SoundTouch 10, the Wireless Link, the SoundTouch 20 and the SoundTouch 300. Each version is a GitHub release
  "SoundTap Core X.Y.Z", tag `os-vX.Y.Z`, with the package and its SHA-256. The steps below are
  for this lane.
- **SoundTap app**, soundtapd with the UI: `gh workflow run release.yml -f lane=app` builds it
  from the commit and creates a draft "SoundTap app X.Y.Z" (`app-vX.Y.Z`,
  `SoundTap-app-X.Y.Z-armv7.bin`). It is installed from the speaker's Updates page and refused
  on a Core whose `/etc/soundtap/os-api` is lower than the app needs.

The version numbers live in the repository ([ADR 0035](docs/decisions/0035-releases.md)):
`os/VERSION` for SoundTap Core, `app/VERSION` for the app. Raise the one you release in a PR
first; the Release workflow reads it and refuses a tag that already exists. Release notes are
generated per lane by `tools/release/notes.py`. A version `X.Y.Z-alpha.N` is an **alpha**, a
GitHub prerelease published before the release's hardware approval ([Alphas](#alphas)).

## Changelog

[`CHANGELOG.md`](CHANGELOG.md) tells owners what changed, in Keep a Changelog style. Every tag,
on either lane, gets its own section, written for owners rather than copied from commit
subjects. Changes collect under `## [Unreleased]` as they merge; the PR that raises
`os/VERSION` or `app/VERSION` moves the ones that ship into a section headed by the tag and the
release date:

```md
## [os-v0.1.0] - 2026-12-01
```

An alpha's tag gets its own section the same way (`## [os-v0.1.0-alpha.1] - 2026-11-23`).
`tools/release/changelog.py check <tag>` refuses a tag without that section, without a date
or without a `- ` entry. `dry-run.sh` runs it for the candidate's `os-vX.Y.Z` on the commit it
checks, and both lanes of `release.yml` run it before they create the draft.

Both lanes publish only a commit on which CI (`build-stu.yml`) passed on `main`
([ADR 0035](docs/decisions/0035-releases.md)).

## Route to public

A release goes out only when the maintainer says so, every time. Before that it travels one route:

1. **CI on `main`.** Every push to `main` runs CI (`build-stu.yml`) and, unless it changes
   only docs or a release's hardware reports, Build (`build.yml`): the checks, the image and the install gate on every model.
   No speaker is touched ([ADR 0117](docs/decisions/0117-no-automated-speaker-install.md)). A
   red run on `main` is a bug to fix before anything moves on.
2. **Soak.** The production candidate (step 1 of [Steps](#steps)) runs for **7 days** on the maintainer's
   bench speakers: ST10, ST20, ST300 and WLA, in daily use. A fix for a serious bug in that week
   makes a new candidate and starts the 7 days again.
3. **Dry run.** `tools/release/dry-run.sh` runs the pre-publish checks on the candidate
   (see [Dry run](#dry-run)). It publishes nothing.
4. **Public.** With the [release criteria](#release-criteria) met, the maintainer gives the OK
   and steps 4 to 8 of [Steps](#steps) publish it: signed as a draft, installed on the maintainer's bench speakers through the
   signed upload route, then published, then named in the update feed.

Approval is needed to start the soak, to sign, and to publish; each is a separate yes.

## Dry run

```sh
# in a checkout of the candidate's commit
tools/release/dry-run.sh SoundTap-Core-X.Y.Z.stu <commit> SoundTap-Core-X.Y.Z.cdx.json SoundTap-Core-X.Y.Z.stu.sig
```

It checks the version and the tag's [CHANGELOG section](#changelog), runs `stu_check.py accept` on the package, runs `os_cves.py` on the
SBOM, verifies the `.sig` against `release.PublicKey` (its statement: kind `core`, profile `production`, this version and digest) and writes the static
update feed into a temporary directory to check it. It never reads the private key and never
contacts GitHub. Without an SBOM or a `.sig`, or while the commit's `release.ReservePublicKey`
is still the empty placeholder, it says which step is incomplete and exits 3: a skipped step
is not a pass. The `.sig` is the release's own, which `sign.yml` puts on the draft (step 5);
run the dry run again with it before publishing.

## Release criteria

All of these hold on the commit that is released:

- no open issue labelled `P0` (bricks a speaker, loses data or settings, or a security hole)
  or `P1` (a main feature broken on a model);
- every CI and Build run on `main` green during the 7 days of the soak, and CI green on the
  released commit (its `vuln` job runs `govulncheck` on soundtapd and
  `npm audit --omit=dev --audit-level=high` for the UI, `make vuln`);
- the security audit closed;
- `BUILDROOT_COMMIT` in the `Makefile` is the newest point release of its Buildroot LTS
  branch, and `make os-cves SBOM=SoundTap-Core-X.Y.Z.cdx.json` on the candidate's SBOM passes:
  no open CVE scoring 7.0 or more, or not scored yet, in an OS package. That includes the
  host packages whose output ships (SPL/U-Boot and the BCO app); shipped packages without a
  CPE, and those whose CPE NVD does not know, are listed as not checked, and an empty or
  partial SBOM fails. It asks NVD over the network. A CVE that does not apply to us goes into
  the package's `<PKG>_IGNORE_CVES` in `os/ignore-cves.mk`, one per line, with its reason in a
  form `os/support/ignore_cves.py` checks (the file's header lists them);
- the version is real: `os/VERSION` (and `app/VERSION` for an app release) raised in a PR to a
  number never released before, `X.Y.Z` or for an alpha `X.Y.Z-alpha.N`. The build writes
  `os/VERSION` into the image (`os/board/image/post-build.sh`) and `sign.sh` into the `.sig`;
  `dry-run.sh` fails on a malformed version or an existing `os-v<version>` tag;
- `CHANGELOG.md` has the release's dated section ([Changelog](#changelog));
- `release.ReservePublicKey` holds the reserve key's public half, not the empty placeholder
  (see [Release keys](#release-keys));
- the contributor contract in `CONTRIBUTING.md` names no maintainer speaker, host, network
  or private permission, and `os/tests/docs/test_public_tree.py` passes on it.

The first public release is cut from a new public repository that starts from one commit; the
development history stays private. Releases are built by GitHub Actions: Build, CI
(`build-stu.yml`) and both Release lanes run on GitHub-hosted `ubuntu-24.04` runners
([ADR 0011](docs/decisions/0011-one-buildroot-pipeline.md)). Fork PRs use read-only
permissions and no secrets. Development builds run the same entry points
(`tools/ci/run-build.sh`) on any Linux machine with Docker.

## Steps

Before step 1, once the web UI is final for this release, refresh the README screenshots
from it, in the PR that raises the version or one after it: `make screenshots` builds the web
UI from the checkout (`make web`), serves it with a fake speaker, photographs it in a pinned
Chromium ([`docs/tools/`](docs/tools/)) and rewrites the hero, features, screens, speakers and
social-preview images in `docs/assets/`. Next to them it writes `docs/assets/screenshots.source`, the git
trees of `app/frontend` and `docs/tools` they were made from; it refuses uncommitted changes
under either. Look at the images, then commit `docs/assets`. Both lanes of `release.yml` and
`dry-run.sh` refuse a commit whose `app/frontend` or `docs/tools` differs from that record
(`tools/release/screenshots.py check`), so a UI fix during the soak needs `make screenshots`
again. The check proves which UI the images come from, not that they look right.

1. On `main`, run **Build** (`build.yml`) with `profile=production`. It builds SoundTap Core (`make os`)
   (kernel, root filesystem, WLA and ST20 bootloaders) and packages it with `make stu`, which
   also builds the preview package `SoundTap-Core-X.Y.Z-preview.stu`: the same payloads with
   the preview build of the installer, the only package a preview model installs from
   ([ADR 0133](docs/decisions/0133-more-soundtouch-models-from-stock.md)). The result is the run's `candidate` artifact,
   together with the commit it was built from and its profile. Release refuses an alpha
   candidate ([ADR 0011](docs/decisions/0011-one-buildroot-pipeline.md)); pushes to
   `main` build alpha. Each hosted run starts with a fresh Buildroot output tree. It restores only source
   downloads and compiler-cache entries; it does not restore a configured Buildroot tree,
   package build directories or a previous root filesystem. "Cold" here means a fresh
   output tree, not an empty compiler cache. Source hashes and ccache's input keys govern
   reuse; this is not proof of equivalence with a cache-free build. A production build also
   runs `make legal-info`: `SoundTap-Core-X.Y.Z-sources.tar` (license texts and the exact source
   of every package, which the GPL requires next to the binaries; for the BCO firmware, the
   bootloaders and the kernel's ST10 audio driver, the repository files their recipes read sit
   under `soundtap-inputs/` at their repository paths, and `make legal-info` fails without them) and
   `SoundTap-Core-X.Y.Z.cdx.json` (CycloneDX SBOM). Release refuses a candidate without them and
   publishes both. The build also attests the package's
   provenance (`gh attestation verify SoundTap-Core-X.Y.Z.stu -R stein155/soundtap`).
2. Install that exact candidate on the ST10, ST20, WLA and ST300 (see
   [`docs/owners/install.md`](docs/owners/install.md)) and run `docs/hardware/hardware-test.md`.
   Test the exact bytes you will publish. Do not rebuild or rename them. Every applicable
   open hardware check must be recorded against this candidate. Historical passes
   need candidate regression evidence; unavailable hardware and prohibited tests remain
   explicit limitations for the maintainer to resolve in release scope, never implied passes.
3. Write one schema 2 report per model, `hardware-st10.json`, `hardware-st20.json`,
   `hardware-wla.json` and `hardware-st300.json`, with its evidence files, into
   `releases/os-vX.Y.Z/` and open a PR with them (see [Hardware approval](#hardware-approval)).
   Merge it only after step 4: it moves `main`, and step 4 releases the candidate's own commit.
4. Run **Release** (`release.yml`) on the same `main` commit. It checks that the candidate
   was built from that commit, verifies the checksums and the STU structure, checks that the
   preview package differs from the package in its installer alone (`stu_check.py
   same-payloads`), and creates a **draft** release with the package, the preview package,
   each with its `.sha256`, sources, SBOM and the release notes.
   The hosted job allows 60 minutes for downloading and uploading the sources archive;
   actual transfer time still needs measuring on the production candidate.
5. Sign: merge the reports PR, then run **Sign** (`sign.yml`) on `main` with the tag. In the
   [release environment](#the-release-environment) it runs `tools/release/sign-release.sh`,
   which downloads the draft, checks the digests, refuses to sign unless all four reports in
   `releases/os-vX.Y.Z/` pass against those exact bytes and uploads `<asset>.sig`. It signs
   the preview package too, with the same statement fields over its own digest, once
   `stu_check.py same-payloads` shows its payloads are the package's, and uploads its `.sig`.
   The release stays a **draft**: no speaker but the maintainer's can see it.
6. **Staged rollout: the maintainer's bench speakers first.** Download the signed draft
   (`gh release download os-vX.Y.Z -p 'SoundTap-Core-*'`) and install it on each of the
   maintainer's bench speakers through its Updates page, choosing the `.stu` and its `.sig`
   together: that is `POST /api/firmware` with the release's own signature, the route an
   owner uses, which a soak candidate installed over SSH or unsigned does not exercise. Each
   speaker must come back on the new version and confirm its boot (ADR 0010). A failure
   here stops the release: fix it, and the fix is a new candidate from step 1.
7. Publish: run **Sign** again with `publish=true`. It signs again, which gives the same
   `.sig` (an ed25519 signature is deterministic), and `sign-release.sh --publish` checks that
   the draft holds the `.sig` that states this tag's kind, version and digest, for the package
   and for the preview package, then makes the
   release public and latest (an [alpha](#alphas): a prerelease, never latest). From now on speakers on SoundTap see it on their Updates page (within
   the ten minutes soundtapd caches the release list).
8. Point the update feeds at it, in a PR. A speaker still on Bose's software installs from
   `updates/soundtouch` on `main`: the SoundTap app and `docs/installer` name that file
   in `swUpdateStart`, and it names the release asset. A preview model installs from
   `updates/soundtouch-preview`, which names the preview package; the installers send a
   speaker there only after its owner confirmed the preview. Until this PR merges they install the
   previous release. The repository has to be public for this route to work. Run
   `tools/release/feed_check.py updates/soundtouch` and
   `tools/release/feed_check.py --preview updates/soundtouch-preview` before opening the PR; the **Update feed**
   workflow runs both again on the PR (see [The first-install feed](#the-first-install-feed)).

```sh
# raise os/VERSION and give the tag its CHANGELOG.md section in a PR, merge it
make screenshots    # once the UI is final; commit docs/assets in a PR, merge it
gh workflow run build.yml -f profile=production
# ...hardware test; the reports and evidence in releases/os-vX.Y.Z/ in a PR, not merged yet...
gh workflow run release.yml -f lane=os
# ...merge the reports PR...
gh workflow run sign.yml -f tag=os-vX.Y.Z
gh release download os-vX.Y.Z -p 'SoundTap-Core-*' -D ~/soundtap-stage/os-vX.Y.Z
# ...install the .stu with its .sig on each of your own speakers through its Updates page...
gh workflow run sign.yml -f tag=os-vX.Y.Z -f publish=true
gh release download os-vX.Y.Z -p 'SoundTap-Core-*.stu' -D /tmp/feed
mkdir -p updates
python3 tools/release/mock_update_server.py --serve /tmp/feed/SoundTap-Core-X.Y.Z.stu \
  --write-index updates/soundtouch --host https://github.com \
  --urlpath stein155/soundtap/releases/download/os-vX.Y.Z
python3 tools/release/mock_update_server.py --serve /tmp/feed/SoundTap-Core-X.Y.Z-preview.stu \
  --preview --write-index updates/soundtouch-preview --host https://github.com \
  --urlpath stein155/soundtap/releases/download/os-vX.Y.Z
python3 tools/release/feed_check.py updates/soundtouch
python3 tools/release/feed_check.py --preview updates/soundtouch-preview
```

An app draft is signed separately with `gh workflow run sign.yml -f tag=app-vX.Y.Z`, which
signs it as app `X.Y.Z` (kind `app`, the tag's version); a speaker installs an app release
only with that statement. It follows the same staged order: install
`SoundTap-app-X.Y.Z-armv7.bin` with its `.sig` on the maintainer's bench speakers (Updates page,
`POST /api/soundtapd`), then `gh workflow run sign.yml -f tag=app-vX.Y.Z -f publish=true`. The
signing does not require the Core reports for that lane; this is not an exemption from the release approval
or applicable hardware validation for changes carried by the app.

If `main` moves between steps 1 and 4, the Release workflow refuses to publish. Run
step 1 again.

## Alphas

An alpha is a release published before its hardware approval: version `X.Y.Z-alpha.N` in
`os/VERSION` and `app/VERSION` (N from 1 up, no leading zero), tag `os-vX.Y.Z-alpha.N` or
`app-vX.Y.Z-alpha.N`, published on GitHub as a **prerelease** and never as latest
([ADR 0035](docs/decisions/0035-releases.md#alphas-prereleases-before-the-hardware-approval)).
It follows the [Steps](#steps) above with two differences:

- **No hardware reports.** Steps 2 and 3 are not required, so `release.yml` has `sign.yml`
  sign the draft right after making it, without reports, saying that it does; reports in
  `releases/os-vX.Y.Z-alpha.N/` would still be checked. It stays a draft. Everything else
  holds: the release criteria, CI on the commit, a `profile=production` candidate from Build on
  that commit with its sources and SBOM, the CHANGELOG section, the release key under its
  passphrase, and the maintainer's own speakers before publishing.
- **A prerelease.** `release.yml` creates the draft as a prerelease, and
  `sign-release.sh --publish` publishes it as one, never as latest.

An alpha is a production image like a release; it has nothing to do with the alpha *profile*
(root SSH, bench builds), which is never published.

What speakers are offered: a speaker on a release is never offered an alpha, on either lane. A
speaker on an alpha is offered the highest newer alpha or release, so the release that follows
moves it off the alpha line. Versions order as semver: `0.1.0-alpha.1 < 0.1.0-alpha.2 < 0.1.0`,
so installing an older alpha over a newer one, or an alpha over its release, is a downgrade.

```sh
# set os/VERSION and app/VERSION to X.Y.Z-alpha.N with CHANGELOG.md sections for both tags, merge
gh workflow run build.yml -f profile=production
gh workflow run release.yml -f lane=os     # makes the draft and signs it
gh workflow run release.yml -f lane=app
# ...install both with their .sig on your own speakers through the Updates page...
gh workflow run sign.yml -f tag=os-vX.Y.Z-alpha.N -f publish=true
gh workflow run sign.yml -f tag=app-vX.Y.Z-alpha.N -f publish=true
# then point the first-install feed at the alpha (step 8), while no release is out
```

The first-install feed may name an alpha: new owners install it while no release is out. The
installer page shows the newest release, and the newest alpha only while there is none.

## Withdrawing a release

A published release that turns out bad is withdrawn, not deleted: its page, notes, checksum
and files stay, so it is clear what was out there and why it went.

1. **Mark it.** Edit the release notes on GitHub and add, at the top, a line that starts with
   `Withdrawn:` followed by the reason, for example
   `Withdrawn: the SoundTouch 20 loses Wi-Fi after a reboot; use 0.2.1`. The marker must
   start the line; the generated notes list their changes as `- ` items, so they never do.
   If it was GitHub's latest release, make the previous good one latest
   (`gh release edit os-vPREV --latest`).
2. **Pull it from the update feed.** If `updates/soundtouch` names it, point the feed back at
   the previous good Core release, or on to the fix, in a PR, and `updates/soundtouch-preview`
   with it (see
   [Steps](#steps), step 8). Until that PR merges, speakers still on Bose's software
   install the withdrawn release. GitHub may serve the old file for a few minutes after the
   merge. The feed check refuses an index that names a withdrawn release, so the PR that
   points the feed elsewhere is the one that passes.
3. **Release the fix** as a new, higher version through the normal route.

What soundtapd does with it (`internal/update`): a release whose notes have a `Withdrawn:`
line is handled as if it were not there, on both lanes. It is never "latest", not installed
by its tag (`POST /api/update` answers that the release is unknown), not offered as a Core
update, and the Updates page and Home Assistant's update entity do not show it. soundtapd
caches the release list for ten minutes, so a speaker can still offer it for that long. A
release that disappears entirely (deleted, or turned back into a draft) is treated the same
way; a prerelease is offered only to a speaker that runs an [alpha](#alphas), and can still be
installed by its tag, so it is not a withdrawal.

What withdrawing does not do:

- **Speakers that installed it stay on it.** Nothing moves a speaker back by itself: the
  latest release is never older than what runs. The way off is the fix release. An owner who
  wants the previous version back uploads it with its `.sig`, confirms the downgrade and
  presses once on the speaker: every upload waits for that press, signed or not (ADR 0048).
- **Its signature stays valid.** Anyone with the `.stu` or app and its `.sig` can still
  upload it, but only with the press on the speaker. Without a press, the speaker installs
  only what it fetches from the releases itself, and a withdrawn release is no longer among
  them. Only a key rotation makes the signature invalid
  ([Release keys](#release-keys)), and then for every release signed with the retired key,
  within the limits of [what revocation does](#what-revocation-does-and-does-not-do). Short
  of that, the fix release's higher version is what keeps speakers from being offered it.

## The first-install feed

`updates/soundtouch` on `main`, with its twin for the preview models,
`updates/soundtouch-preview`, is the one input that reaches a speaker without our
signature. A speaker still on Bose's software installs whatever package that index names, and
Bose's updater checks only the length and CRC32 the index states (ADR 0048). Whoever can
change that file on `main` chooses the software every speaker started from the installers
gets. It is protected in three ways:

- **Code owners.** `.github/CODEOWNERS` makes the maintainer the owner of `updates/`, the
  installers in `docs/installer/` that name the feed, the release keys and the updater's
  repository (`app/internal/release/`, `app/internal/update/`), `tools/release/` and the
  workflows that check and publish.
- **The feed check.** `tools/release/feed_check.py` holds the index to one release: every
  supported model gets the same image, its URL is
  `https://github.com/stein155/soundtap/releases/download/os-vX.Y.Z/SoundTap-Core-X.Y.Z.stu`
  (`X.Y.Z` may be an alpha's `X.Y.Z-alpha.N`, a published prerelease), its length and CRC are that asset's, the release is published and not withdrawn, and the
  asset's `.sig` states production Core `X.Y.Z` over its SHA-256 and verifies with a key
  compiled into soundtapd. The **Update feed** workflow (`feed.yml`) runs it on every PR and
  push that touches the feed, the keys or the check; `dry-run.sh` runs it on the local
  candidate. A failing check means the feed names something no speaker on SoundTap would
  accept. The preview feed `updates/soundtouch-preview`
  ([ADR 0133](docs/decisions/0133-more-soundtouch-models-from-stock.md)) gets the same check
  with `--preview`: the preview models only, and the release's
  `SoundTap-Core-X.Y.Z-preview.stu` with its own `.sig`. Neither feed may list a model of the
  other.
- **Branch protection (maintainer, after the move to the public repository).** CODEOWNERS
  only asks for a review; it blocks nothing until `main` is protected. In the public
  repository's settings, protect `main` with: require a pull request, require review from
  code owners, require the **Update feed** check to pass, and do not allow bypassing these
  settings, administrators included. Without that, anyone with write access can still
  change the feed directly.

The check proves the feed names a signed release; it does not make the stock updater check
the signature. A PR that passes it can still point the feed at an older signed release, which
is why the review stays required.

## Repository settings

The workflows run on GitHub-hosted runners only and declare every permission they use; the
public repository needs these settings once, by the maintainer:

- **Name.** Code, docs and installers name the repository `stein155/soundtap` and the site
  `https://stein155.github.io/soundtap/`. GitHub's own URLs ignore the case of the name; the
  path of a Pages project site follows it. Keep the repository's name lowercase, or change
  every URL with it (`git grep -n stein155`).
- **Pages.** Settings → Pages → Source: *GitHub Actions*. `docs.yml` builds the site with the
  installer at `/installer/` and deploys it through the `github-pages` environment, which
  Pages creates; leave its deployment branches at `main`.
- **Actions.** Allow actions created by GitHub (every workflow uses only `actions/*`, pinned by
  commit SHA) and require full-length SHA pins. Workflow permissions: read repository contents
  only; leave "Allow GitHub Actions to create and approve pull requests" off. Fork pull
  requests from first-time contributors wait for approval.
- **Secrets.** No repository secret is required. `NVD_API_KEY` (an NVD API key) is optional:
  Build's CVE check runs without it at NVD's slower keyless rate. The release key is an
  environment secret, never a repository one ([The release environment](#the-release-environment)).
- **Features.** Discussions on (the issue form links them), private vulnerability reporting on
  ([SECURITY.md](SECURITY.md)), Dependabot alerts and security updates on.
- **Branch protection** of `main`, as [The first-install feed](#the-first-install-feed) says,
  with CI's checks required.

## No CI storage

CI keeps no storage release (ADR 0011). Candidates and rescue tools are artifacts of the run
that built them: fetch one with `gh run download <run> -n <name>`, and rebuild it once it has
expired.

## Offline gates

```sh
# From the repository root on Linux/GNU host tools. Tests do not build a release candidate.
tools/ci/run-ci.sh    # make lint, make test and make vuln, side by side
# After the cold production build, select the one intended artifact explicitly.
python3 os/support/stu_check.py accept dist/SoundTap-Core-X.Y.Z.stu
python3 os/support/rootfs_check.py --security-only --profile production output/os/target
```

## Production security gate

Alpha images deliberately allow passwordless root SSH. A release is a production image, which
has no SSH and nothing in its place ([ADR 0119](docs/decisions/0119-no-ssh-in-production.md)):
`make SOUNDTAP_PROFILE=production os` drops Dropbear and root login, and
`rootfs_check.py --profile production` rejects any SSH server, client, key tool, host key,
`authorized_keys`, init that sets one up, a blank root password and a filter rule for port 22.
It also reads Buildroot's `.config` next to the rootfs and rejects any enabled package
whose recipe says `REDISTRIBUTE = NO`: that setting only keeps the source out of legal-info,
the binary would still ship.
Confirm on the installed candidate that nothing listens on port 22.

## Signing

Each installable Core STU or app binary gets `<asset>.sig` (ADR 0048). The signing script
selects the one asset with a companion `.sha256`; it does not create separate signatures
for the source archive or SBOM. `release.yml` never holds the key: it only makes a draft.
**Sign** (`sign.yml`) signs it, in the [release environment](#the-release-environment), from
`main` only: it writes the secret `SOUNDTAP_RELEASE_KEY` to a file only the runner's user can
read under `$RUNNER_TEMP`, and runs `tools/release/sign-release.sh` with
`SOUNDTAP_RELEASE_KEY_FILE` naming that file and `SOUNDTAP_RELEASE_KEY_PASSIN=env:SOUNDTAP_RELEASE_KEY_PASSPHRASE`,
so openssl reads the passphrase from the environment, never from its arguments. A step that
always runs, failure or not, removes the file. `tools/release/sign.sh` refuses a key whose
public half is not `release.PublicKey`. The release key is kept under a passphrase: for a
production signature, which is what `sign-release.sh` makes, `sign.sh` refuses a key that
opens without one. To put a passphrase on a key: `openssl pkey -in <key> -aes256 -out <new key>`.
An alpha (bench) signature may use a key without one; openssl asks for a passphrase, or reads
it as `SOUNDTAP_RELEASE_KEY_PASSIN` says (openssl's `-passin` form, such as `env:VAR`).
A running speaker installs a fetched STU or app only with a release's `.sig`. An upload installs
with its `.sig`, or without one after the press on the speaker, on any image
([installing your own builds](#installing-your-own-builds)).

### The release environment

The release key reaches one job: `sign.yml`'s, in the GitHub Actions environment `release`.
The maintainer sets it up once, and again with new secrets after a rotation:

1. Create the environment and limit its deployment branches to `main` (Settings →
   Environments → *New environment* `release` → *Deployment branches and tags*: *Selected
   branches and tags*, add `main`), or:

   ```sh
   echo '{"deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}}' |
     gh api -X PUT repos/stein155/soundtap/environments/release --input -
   gh api -X POST repos/stein155/soundtap/environments/release/deployment-branch-policies \
     -f name=main -f type=branch
   ```

2. Optionally, add yourself as a required reviewer: each signing run then waits for your
   approval, so pushing to `main` alone cannot sign. Without one, anyone who can run a workflow
   on `main` can sign. In the settings page, *Required reviewers*; or, together with the branch
   rule above:

   ```sh
   printf '{"reviewers": [{"type": "User", "id": %s}], "deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}}' \
     "$(gh api user -q .id)" | gh api -X PUT repos/stein155/soundtap/environments/release --input -
   ```

3. Set the two secrets in the environment, never in the repository. The key is the PEM
   encrypted under its passphrase (`sign.sh` refuses one without); `gh` asks for the passphrase
   without echoing it, so it stays out of the shell history:

   ```sh
   gh secret set SOUNDTAP_RELEASE_KEY --env release < ~/.soundtap-release-key.pem
   gh secret set SOUNDTAP_RELEASE_KEY_PASSPHRASE --env release
   ```

`gh secret list --env release` shows both names, never their values. Nothing else reads them:
`release.yml` has no secret, and the environment refuses any other branch, pull requests and
forks. GitHub masks the secrets in logs; `sign.yml` never prints them or turns on shell
tracing, and a test checks that it does not.

### Installing your own builds

An owner who builds SoundTap themselves needs no key. This is not a release: nothing is
published or signed.

1. Build: `make os stu` gives `dist/Update-soundtap-buildroot.stu`.
2. Upload: on the speaker's Updates page, *Other ways* → *Upload a file*, pick the `.stu` alone.
   The speaker checks it and says it is not signed.
3. Press: choose *Install firmware*, then hold a button on the speaker (not volume) for two
   seconds. Every upload waits for that press, signed or not, and it is what lets an unsigned
   package in (ADR 0048); nothing automatic and nothing from the network alone installs one.
4. Restart: choose *Reboot into the new firmware* and press once more on the speaker. Only a
   release the speaker fetches itself (*Update* on the Updates page) restarts into the new
   slot on its own.

This works on alpha and production images alike. A USB stick at power-on takes the same `.stu`
renamed `Update.stu`, also without a signature ([ADR 0130](docs/decisions/0130-usb-stick-recovery.md)).
`sign.sh` signs with the release key only.

## Release keys

There are two ed25519 release keys, and soundtapd carries the public half of both
(`app/internal/release/release.go`):

- **The active key**, `release.PublicKey`. Every release is signed with it; `sign.sh`
  refuses any other key.
- **The reserve key**, `release.ReservePublicKey`. Nothing is signed with it while the
  active key is in service. It exists so that a speaker in the field already trusts a
  second key on the day the active one is lost or leaked: a speaker checks an upload against
  the keys in the software it runs, so a key it does not know yet can reach it only inside a
  release signed with one it does.

`release.Verify` accepts a statement signed by either key and skips an empty one. U-Boot
carries the same keys for the USB stick in `os/bootloader/uboot/soundtap-keys.h`, generated
from `release.go`: after a change to either key, run
`UPDATE_UBOOT_KEYS=1 go test -run TestUbootKeys ./internal/release` in `app/` and commit the
header in the same PR (`TestUbootKeysAreTheReleaseKeys` fails until then). A new key list
changes `u-boot.img`, so the next Core release writes `NAND.U-Boot` on every speaker.

Each private half has its own offline copy under its own passphrase, on separate media in
separate places, so that one loss or theft does not take both. The active key, still under its
passphrase, is also the secret of the [release environment](#the-release-environment), where
`sign.yml` signs with it; the reserve is in no secret until it becomes the active key. Neither is
ever a repository secret, on a build host or in the repository.

### Before the first public release: the reserve key

`release.ReservePublicKey` is an empty placeholder until the maintainer fills it in. It has
to hold the reserve's public half before the first public release, because a speaker only
trusts the keys of the software it was installed with. `dry-run.sh` reports an empty reserve
as an incomplete step. On an offline machine:

```sh
openssl genpkey -algorithm ed25519 -out soundtap-reserve-key.pem
openssl pkey -in soundtap-reserve-key.pem -pubout -outform DER | tail -c 32 | base64
```

Put the printed value into `ReservePublicKey` in a PR, regenerate U-Boot's key header as
above, and move `soundtap-reserve-key.pem` to its own offline medium. `TestCompiledKeys`
fails on a value that is not an ed25519 key or that equals the active key.

### Rotation: the reserve becomes the active key

The same procedure covers a planned change, a leaked key and a lost key; only the urgency
differs. None of it has been exercised yet.

1. Generate a new reserve key as above.
2. In a PR, move the old reserve's public half into `PublicKey` and put the new reserve's
   into `ReservePublicKey`, and regenerate U-Boot's key header. The retired active key is
   no longer in the list.
3. Set the [release environment](#the-release-environment)'s two secrets to the old reserve,
   now the active key, and its passphrase. Release Core and the app from that commit as usual.
   `sign.sh` checks the key against the `PublicKey` of the checkout it runs from, so `sign.yml`
   on `main` from that commit on signs with the old reserve. Speakers in the field accept it
   because they already trust the reserve.
4. A speaker that installs this release trusts the new pair from then on. A speaker that
   skips it still trusts its old pair, which includes the new active key, so it can take
   any later release until the next rotation. A speaker two rotations behind trusts neither
   key and can no longer update through its update page.

A **leaked** key is rotated at once, and its secrets are deleted from the release
environment first (`gh secret delete SOUNDTAP_RELEASE_KEY --env release`), so nothing signs
with it in the meantime. A **lost** active key is
rotated the same way; nothing is lost but the key. If **both** keys are lost, no release can
be signed for speakers in the field: their update page is closed for good. What remains is
the power-on USB stick, which needs no signature
([ADR 0130](docs/decisions/0130-usb-stick-recovery.md)), and the way back through Bose's
software on the Updates page, and from Bose's software
a first install of a release signed with the new keys ([`docs/owners/install.md`](docs/owners/install.md)).
That this works on a production speaker is not proven on every model.

### What revocation does and does not do

Dropping a key from `release.go` revokes it only in the software that is replaced. On a
speaker the key list lives in several places, and each keeps the old list until it is
rewritten:

- **The app override.** An app release with the new list revokes the key for the running
  soundtapd. The soundtapd built into the Core image still has the old list and runs again
  when the override is rolled back or removed.
- **Both Core slots.** A Core release writes only the inactive slot. The other slot keeps the
  old soundtapd until the next Core install overwrites it, and a boot fallback to it brings
  the old list back.
- **Not U-Boot.** The USB stick at power-on checks no signature (ADR 0130), so U-Boot holds no
  key list and a revoked key changes nothing there: whoever holds the speaker's keys at
  power-on can install from a stick, with or without a key.

So a revoked key stays usable against a speaker until it has installed two Core releases
carrying the new list (one per slot); until then someone holding the key and with the
speaker on their network or in their hands can still give it software.

## Hardware approval

`sign-release.sh` refuses a Core release, an [alpha](#alphas) excepted, unless
`tools/release/release_approval.py` accepts the reports directory, which `sign.yml` takes from
`releases/os-vX.Y.Z/` on `main`: one report per model (`hardware-st10.json`, `hardware-st20.json`,
`hardware-wla.json`, `hardware-st300.json`), each schema 2, naming the exact asset and its SHA-256, the device
(ST10 `rhino`/`sm2`, ST20 `spotty`/`scm`, WLA `binky`/`sm2`, ST300 `ginger`/`sm2`), every check of that model as
`pass`, and evidence files whose hashes still match. The WLA report must also carry its RF
configuration evidence (`rf_config`) and pass `rf-config`, `usb-recovery` and
`bootcount-rollback`. The ST300 report must pass the soundbar processor link
(`soundbar-link`), the TV input (`tv-input`) and the bass module (`bass-module`), plus
`usb-recovery` and `bootcount-rollback`. A report records what was tested; it never
authorises a hardware action.

`releases/os-vX.Y.Z/` holds the four reports and their evidence files side by side, each
evidence file named `hardware-<model>-<what>` as its report names it. They are public. Redact
them before their hashes go into the report, as recorded hardware evidence is redacted
(`os/tests/fixtures/`): no credentials or account identifiers, serials as `SERIAL0000000000A`,
MACs as `02:00:00:xx:xx:xx`, addresses as `192.0.2.x`; `os/tests/docs/test_public_tree.py`
fails on the maintainer's own. No Bose bytes: no firmware, dump or codec script
([ADR 0049](docs/decisions/0049-no-stock-bytes.md)). Once a release is signed against them,
they do not change.

On each model, keep the serial boot log, `dmesg`, `/tmp/soundtap.log` and the SHA-256 of
the tested package. Cover at least:

- three cold boots and three warm reboots;
- Wi-Fi setup, a wrong password and reconnect;
- persistence across both A/B slots;
- every source (TuneIn, AirPlay, Spotify, Bluetooth where present, AUX) and switching between them;
- every button and LED state;
- going back to stock firmware.

A green QEMU job on its own does not approve a release.
