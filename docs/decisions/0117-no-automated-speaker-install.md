# 0117. No automated job installs on a speaker

Status: accepted — 2026-11-08

## Context

Writing a speaker's flash, rebooting it or installing a package on it needs explicit permission
and a proven way back ([the contract](../../CONTRIBUTING.md#absolute-safety-rules)). CI already proves what can be proven without hardware: the
checks, the image, and the install gate on simulated NAND for every model. What it cannot prove is
that a build boots and confirms on a real speaker, and a job that installed each green build on a
bench speaker would do that without anyone deciding it, on a speaker that may be in use for
something else. On a production image every install also waits for a key held on the speaker
([ADR 0055](0055-household-token-and-windows.md)), so such a job could not run there unattended.

## Decision

No automated job installs on a speaker, and CI never touches one. The workflows run on
GitHub-hosted runners ([ADR 0011](0011-one-buildroot-pipeline.md)), with no route to a speaker:
they run the checks, build the image and run the install gate. That a build boots on real hardware
is proven by hand, in an authorised session, on the speakers a change is about, with the bench
scripts in `tools/hil/`.

Alternatives that lost: a scheduled job that installs each green build of `main` on a bench
speaker through the firmware API (it installs without anyone deciding it, on a speaker that may be
in other use, and cannot run on a production image without a person at the speaker anyway).

## Consequences

- Before a PR that touches the OS merges, someone installs it by hand on the speakers it is about
  (`tools/hil/install-candidate.sh`) and records the result in the
  [hardware checklist](../hardware/hardware-test.md).
- The 7-day soak on the maintainer's bench speakers ([`RELEASING.md`](../../RELEASING.md)) stays
  the last hardware gate.
- The bench scripts must never be scheduled from CI ([install gate](../contributors/testing/install-gate.md)).

## Code

- `.github/workflows/build-stu.yml`, `.github/workflows/build.yml`: CI and Build, on hosted
  runners, with no speaker step.
- `tools/hil/install-candidate.sh`, `tools/hil/lib.sh`: the install by hand, which asks for the
  hold on the speaker.
