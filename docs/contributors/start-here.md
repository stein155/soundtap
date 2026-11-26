# Start here

SoundTap has a long [contract](../../CONTRIBUTING.md#the-contract) and many
[decision records](../decisions/README.md). Most of them guard the boot chain and the flash, and
most changes never touch either. This page is what you need for a first pull request.

## Five rules

1. **Nothing reaches a speaker without its owner's OK.** No install, flash, reboot or sound on a
   speaker unless its owner said yes to that exact action. App and web UI work needs no speaker at
   all: `make dev` runs a simulated one ([develop without a speaker](../../CONTRIBUTING.md#develop-without-a-speaker)).
2. **No Bose files.** No firmware, dumps, images or copied code from Bose in the repository or the
   package. What was learned from stock goes into the docs as facts
   ([Bose-free](../../CONTRIBUTING.md#bose-free)).
3. **Every speaker keeps a way back.** An update writes only the slot that is not running, and a
   slot that does not confirm its boot falls back. Do not add a route around that, or around the
   signature check on updates.
4. **A design change comes with its ADR.** A change to what runs where, an install route, the
   flash layout or a safety boundary adds or updates an ADR and
   [the technical design](design.md) in the same pull request. A bug fix or a new screen does not.
5. **Tests sit next to the code, and CI runs green.** Go tests beside the package, frontend tests
   beside the component, the rest in `os/tests/` ([test map](testing/README.md)). Run
   `tools/ci/run-ci.sh` before you open the pull request.

## What to read for your change

| You change… | Read first |
| --- | --- |
| the web UI (`app/frontend/`) | [develop without a speaker](../../CONTRIBUTING.md#develop-without-a-speaker); nothing else |
| a translation | the messages in `app/frontend/src/lib/i18n/messages/`; plain words, as the speaker's own app would say them |
| soundtapd (`app/`) | [the technical design](design.md); for files on `/mnt/nv` also [state on the speaker](nv-state.md) |
| the HTTP API | [API stability](../owners/api-stability.md) |
| the OS, kernel or rootfs (`os/`) | [the contract](../../CONTRIBUTING.md#the-contract) and the [hardware test](../hardware/hardware-test.md) |
| the boot chain, flash or updates | the whole contract, ADRs [0009](../decisions/0009-mainline-linux-own-bootloaders.md), [0010](../decisions/0010-ab-slots-boot-confirm-rollback.md) and [0048](../decisions/0048-signed-updates.md) |
| the ST20's network card (`bco/`) | [ADR 0031](../decisions/0031-bco-network-card.md) and [the BCO page](../hardware/st20/bco-network-card.md) |

The rest of the contract applies once your change reaches that part of the code. When in doubt,
open a draft pull request and ask.
