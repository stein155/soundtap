#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Reject artifact/model/profile combinations not approved for release."""

import argparse

TARGETS = ("st10", "st20", "wla", "st300")
ARTIFACTS = ("stu", "nand")
PROFILES = ("alpha", "production")
PHASES = ("candidate", "promotion")


def validate(target, artifact, profile, phase="candidate", hardware_evidence=False,
             wla_rf_config_evidence=False):
    """Raise SystemExit unless this combination is approved for release.

    The argument checks are not redundant with argparse's choices: test_release_gate
    and the build scripts import validate() directly, so a typo'd target would
    otherwise sail through every rule below and report a gate pass.
    """
    if target not in TARGETS:
        raise SystemExit(f"RELEASE REJECT: unknown target {target!r}, want one of {TARGETS}")
    if artifact not in ARTIFACTS:
        raise SystemExit(f"RELEASE REJECT: unknown artifact {artifact!r}, want one of {ARTIFACTS}")
    if profile not in PROFILES:
        raise SystemExit(f"RELEASE REJECT: unknown profile {profile!r}, want one of {PROFILES}")
    if phase not in PHASES:
        raise SystemExit(f"RELEASE REJECT: unknown phase {phase!r}, want one of {PHASES}")

    if target in ("st20", "wla", "st300") and artifact == "nand":
        raise SystemExit(f"RELEASE REJECT: {target.upper()} NAND installation is not approved")

    if target == "wla" and phase == "promotion" and not wla_rf_config_evidence:
        raise SystemExit(
            "RELEASE REJECT: WLA promotion requires exact WLA RF configuration evidence"
        )

    if phase == "promotion" and not hardware_evidence:
        raise SystemExit(
            f"RELEASE REJECT: {target} {artifact} promotion requires exact model hardware evidence"
        )


def main():
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
        args.hardware_evidence_validated, args.wla_rf_config_evidence_validated,
    )
    label = "RELEASE" if args.phase == "promotion" else "CANDIDATE"
    print(f"{label} GATE OK: {args.target} {args.profile} {args.artifact}")


if __name__ == "__main__":
    main()
