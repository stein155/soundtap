#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Reject artifact/model/profile combinations not approved for release."""

import argparse
from dataclasses import dataclass

TARGETS = ("st10", "st20", "wla", "st300")
ARTIFACTS = ("stu", "nand")
PROFILES = ("alpha", "production")
PHASES = ("candidate", "promotion")


@dataclass(frozen=True)
class Evidence:
    """The validated evidence a promotion needs: model hardware and, for the WLA, its RF configuration."""

    hardware: bool = False
    wla_rf_config: bool = False


NO_EVIDENCE = Evidence()


def validate(target: str, artifact: str, profile: str, phase: str = "candidate",
             evidence: Evidence = NO_EVIDENCE) -> None:
    """Raise SystemExit unless this combination is approved for release.

    The argument checks are not redundant with argparse's choices: test_release_gate
    and the build scripts import validate() directly, so a typo'd target would
    otherwise sail through every rule below and report a gate pass.
    """
    if target not in TARGETS:
        msg = f"RELEASE REJECT: unknown target {target!r}, want one of {TARGETS}"
        raise SystemExit(msg)
    if artifact not in ARTIFACTS:
        msg = f"RELEASE REJECT: unknown artifact {artifact!r}, want one of {ARTIFACTS}"
        raise SystemExit(msg)
    if profile not in PROFILES:
        msg = f"RELEASE REJECT: unknown profile {profile!r}, want one of {PROFILES}"
        raise SystemExit(msg)
    if phase not in PHASES:
        msg = f"RELEASE REJECT: unknown phase {phase!r}, want one of {PHASES}"
        raise SystemExit(msg)

    if target in ("st20", "wla", "st300") and artifact == "nand":
        msg = f"RELEASE REJECT: {target.upper()} NAND installation is not approved"
        raise SystemExit(msg)

    if target == "wla" and phase == "promotion" and not evidence.wla_rf_config:
        msg = "RELEASE REJECT: WLA promotion requires exact WLA RF configuration evidence"
        raise SystemExit(
            msg,
        )

    if phase == "promotion" and not evidence.hardware:
        msg = f"RELEASE REJECT: {target} {artifact} promotion requires exact model hardware evidence"
        raise SystemExit(
            msg,
        )


def main() -> None:
    """Check the combination given on the command line and report the gate result."""
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", choices=TARGETS, required=True)
    parser.add_argument("--artifact", choices=ARTIFACTS, required=True)
    parser.add_argument("--profile", choices=PROFILES, default="alpha")
    parser.add_argument("--phase", choices=PHASES, default="candidate")
    parser.add_argument("--hardware-evidence-validated", action="store_true")
    parser.add_argument("--wla-rf-config-evidence-validated", action="store_true")
    args = parser.parse_args()
    validate(
        args.target, args.artifact, args.profile, args.phase,
        Evidence(hardware=args.hardware_evidence_validated,
                 wla_rf_config=args.wla_rf_config_evidence_validated),
    )
    label = "RELEASE" if args.phase == "promotion" else "CANDIDATE"
    print(f"{label} GATE OK: {args.target} {args.profile} {args.artifact}")


if __name__ == "__main__":
    main()
