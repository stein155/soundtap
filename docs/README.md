# SoundTap documentation

The documentation is split by reader. Each folder has a README that lists its pages.

| Folder | For | Start with |
| --- | --- | --- |
| [`owners/`](owners/README.md) | People who own a SoundTouch | [Installing SoundTap](owners/install.md), then the [FAQ](owners/faq.md) |
| [`contributors/`](contributors/README.md) | People who change the code | The [technical design](contributors/design.md) |
| [`hardware/`](hardware/README.md) | Per-model facts, release testing and recovery | The page of your model: [ST10](hardware/st10/README.md), [ST20](hardware/st20/README.md), [Wireless Link](hardware/wla/README.md), [ST300](hardware/st300/README.md) |
| [`decisions/`](decisions/README.md) | Anyone asking why it works the way it does | The [ADR index](decisions/README.md) |

At the repository root: the [home page](../README.md), the [changelog](../CHANGELOG.md), the
[runbook](../RUNBOOK.md) (remotes, Apple Home, Snapcast), the [security policy](../SECURITY.md),
and for contributors [CONTRIBUTING.md](../CONTRIBUTING.md), with [the contract](../CONTRIBUTING.md#the-contract),
and [RELEASING.md](../RELEASING.md).

The same pages are published as a site at <https://stein155.github.io/soundtap/>
(`mkdocs.yml`, `.github/workflows/docs.yml`), with the browser installer at `/installer/`, the README as its
home page and the changelog, runbook and security policy beside these pages
(`docs/tools/root_pages.py`).

## Where information belongs

| Question | Maintained in |
| --- | --- |
| What may a contributor do to a speaker? | [the contract](../CONTRIBUTING.md#the-contract) |
| Which hardware facts must code on a model keep to? | Hardware contracts: [ST10](hardware/st10/hardware-contract.md), [ST20](hardware/st20/hardware-contract.md), [infrared](hardware/st10/remote-control.md#the-infrared-contract), [Wireless Link](hardware/wla/README.md) |
| What runs now, and why? | [technical design](contributors/design.md) and the [accepted ADRs](decisions/README.md) |
| How does an owner install or restore? | [Install guide](owners/install.md) |
| What has run on which speaker during install and restore? | [Install records](hardware/install-records.md) |
| What did we read or measure? | Model notes, indexed by the [RE source register](../os/stock/PROVENANCE.md#reverse-engineering-source-register), under the [evidence rules](contributors/evidence.md) |
| Which features have evidence? | [Compatibility matrix](owners/compatibility.md#features), with per-model evidence links or explicit gaps |
| What must a release candidate prove? | [Hardware checklist](hardware/hardware-test.md) and [release procedure](../RELEASING.md) |
| Which offline tests cover the code? | [Test map](contributors/testing/README.md) |

Update the owning document and link to it from summaries. A checklist is a plan, not a run
record; an ADR's dated observation is a historical report, not approval of later bytes.

## Other folders

`installer/` is the browser installer and the `install.sh` / `install.ps1` scripts served from
the site; their URLs are public, so they stay where they are. `tools/` builds the site and the
images.

### Brand assets

[`assets/`](assets/) holds the logo (`logo.svg`), the README hero images and a
1280 × 640 `social-preview.png` for the repository's social card. The images come from
[`docs/tools/`](tools/): a fake speaker with invented music, photographed in the real
web app. `make screenshots` makes them again from the checkout's web UI, at every release
([RELEASING.md](../RELEASING.md#steps)), and records in `assets/screenshots.source` which UI
that was.
