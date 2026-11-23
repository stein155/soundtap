# Tools

Host tooling. Nothing here goes into the image or the STU
([ADR 0040](../docs/decisions/0040-repository-layout.md)). Build-time checkers and the U-Boot
environment code the installer must match live in `os/support/`, not here.

## `ci/`: what CI runs

| Tool | What it does |
| --- | --- |
| `run-ci.sh` | The checks of every pull request, by step: `make lint`, `make test`, `make vuln`. |
| `run-build.sh` | Builds Core, packages the STU and runs the install gate (`os stu run gate`). |
| `buildroot-reuse.sh` | Decides how much of a previous Buildroot tree a build may keep. |
| `fake_speaker.py` | A fake speaker that the `hil/` scripts are tested against. |
| `go_licenses.py`, `npm_licenses.py`, `license_hashes.py` | Licence checks for Go modules, npm packages and local packages ([ADR 0124](../docs/decisions/0124-third-party-code.md)). |
| `image_pins.py` | Every container image is pinned by digest. |
| `shell_scripts.py` | Lists the tracked shell scripts for `make lint-shell`. |

## `dev/`: the developer loop

| Tool | What it does |
| --- | --- |
| `dev.sh` | `make dev`: soundtapd on the simulated speaker with the Vite dev server. |

## `hil/`: a speaker on the bench, by hand

Through the HTTP API, with the speaker's owner at the speaker for every press
([install gate, layer 3](../docs/contributors/testing/install-gate.md#layer-3-hardware-in-the-loop-by-hand)).
No automated job runs these ([ADR 0117](../docs/decisions/0117-no-automated-speaker-install.md)).

| Tool | What it does |
| --- | --- |
| `install-candidate.sh` | Installs a signed STU and proves the speaker came up. |
| `install-app.sh` | Replaces only soundtapd, without a Core install or a reboot. |
| `rollback-check.sh` | Proves the boot-count rollback ([ADR 0010](../docs/decisions/0010-ab-slots-boot-confirm-rollback.md)). |
| `boot-soak.sh` | Repeated installs and boots, without sound. |
| `lib.sh` | What the scripts share. |

## `release/`: making a release

The order is in [`RELEASING.md`](../RELEASING.md).

| Tool | What it does |
| --- | --- |
| `dry-run.sh` | Every pre-publish check on one machine, stopping before anything is published. |
| `changelog.py`, `notes.py`, `screenshots.py` | The changelog section, the release notes and the README screenshots. |
| `os_cves.py` | Known CVEs of the packages in Core, from the SBOM. |
| `release_approval.py`, `hardware_evidence.py`, `release_gate.py` | Refuse to sign without passing hardware reports for every model against the exact asset. |
| `sign-release.sh`, `sign.sh` | Signs a draft and publishes it; `sign.yml` runs them in the `release` environment, the one place the release key is ([RELEASING.md](../RELEASING.md#the-release-environment)). |
| `feed_check.py`, `mock_update_server.py` | The first-install feed, and a stand-in for Bose's updater in tests. |

## `recovery/`: developer recovery

Not owner recovery: owners use the USB stick
([the contract, recovery invariant](../CONTRIBUTING.md#recovery-invariant)). These tools are for a board
whose SPL or U-Boot is damaged, and for the backup that must exist before any bootloader work.

| Tool | Reads or writes | What it does |
| --- | --- | --- |
| `nand-backup-ssh.sh` | reads | A full NAND backup including OOB over root SSH (alpha image). Required before bootloader work. |
| `rom-usb-restore.py` | writes | Repairs the boot chain over mask-ROM USB, for `--model st10\|st20\|wla\|st300`; `--dry-run` first. |
| `rom-usb-read.sh` | reads | Reads one NAND range back over ROM USB (WLA; the ST10 unproven), to check a restore. |
| `rom-usb-loader/` | neither | The USB loader the two scripts above drive ([README](recovery/rom-usb-loader/README.md)). |

What each model has proven is in
[Developer recovery tools](../docs/hardware/install-records.md#developer-recovery-tools).
