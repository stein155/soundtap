#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Refuse to publish an OS release without passing hardware reports for every model.

SoundTap ships one universal STU (one release for ST10, ST20, WLA and ST300). sign-release.sh runs
this before it signs: each model needs a report in the reports directory, made against the
exact asset bytes being published, with every check passed and every evidence file present
and unchanged. The WLA report also carries its RF configuration evidence.

A report records what was tested; it never authorises a hardware action. Making the tests
happen is the maintainer's call, and signing stays a separate, local step.

    release_approval.py --asset Update-soundtap.stu --reports DIR
"""

import argparse
import datetime
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import hardware_evidence as hw
import release_gate

WLA_CHECKS = (
    "read-only-preflight",
    "inactive-slot-install",
    "boot-contract",
    "persistent-storage",
    "kernel-log",
    "cold-boots-3",
    "warm-reboots-3",
    "slot-persistence",
    "audio-arbiter",
    "source-lifecycle",
    "snapcast",
    "leds",
    "airplay",
    "spotify",
    "wifi",
    "rf-config",
    "security",
    "soak-24h",
    "usb-recovery",
    "bootcount-rollback",
)
ST300_CHECKS = (
    "read-only-preflight",
    "inactive-slot-install",
    "boot-contract",
    "persistent-storage",
    "kernel-log",
    "cold-boots-3",
    "warm-reboots-3",
    "slot-persistence",
    "audio-arbiter",
    "source-lifecycle",
    "snapcast",
    "infrared",
    "leds",
    "soundbar-link",
    "tv-input",
    "bass-module",
    "bluetooth",
    "airplay",
    "spotify",
    "wifi",
    "security",
    "soak-24h",
    "usb-recovery",
    "bootcount-rollback",
)
MODELS = {
    "st10": {"device": {"model": "SoundTouch 10", "variant": "rhino", "module_type": "sm2"},
             "checks": hw.ST10_CHECKS, "evidence": hw.REQUIRED_EVIDENCE},
    "st20": {"device": {"model": "SoundTouch 20", "variant": "spotty", "module_type": "scm"},
             "checks": hw.ST20_CHECKS, "evidence": hw.REQUIRED_EVIDENCE},
    "wla": {"device": {"model": "SoundTouch Wireless Link Adapter", "variant": "binky",
                       "module_type": "sm2"},
            "checks": WLA_CHECKS, "evidence": (*hw.REQUIRED_EVIDENCE, "rf_config")},
    "st300": {"device": {"model": "SoundTouch 300", "variant": "ginger", "module_type": "sm2"},
              "checks": ST300_CHECKS, "evidence": hw.REQUIRED_EVIDENCE},
}
SCHEMA_VERSION = 2


def validate_report(path: Path, asset: Path, asset_sha: str, model: str) -> None:
    """Raise ValueError unless path is a passing report for model, made against these asset bytes."""
    spec = MODELS[model]
    report = hw.load_report(path)
    hw.require_exact_keys(
        report,
        {"schema_version", "target", "completed_at", "artifact", "device", "checks", "evidence"},
        f"{model} report",
    )
    if report["schema_version"] != SCHEMA_VERSION or report["target"] != model:
        msg = f"{path.name} must be a schema 2 report for target {model}"
        raise ValueError(msg)
    stamp = report["completed_at"]
    if not isinstance(stamp, str) or not hw.TIMESTAMP.fullmatch(stamp):
        msg = f"{model}: completed_at must be an exact UTC timestamp"
        raise ValueError(msg)
    datetime.datetime.strptime(stamp, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.UTC)
    artifact = report["artifact"]
    hw.require_exact_keys(artifact, {"name", "sha256"}, f"{model} artifact")
    if artifact["name"] != asset.name or artifact["sha256"] != asset_sha:
        msg = f"{model} report was made against other bytes than {asset.name}"
        raise ValueError(msg)
    if report["device"] != spec["device"]:
        msg = f"{model} report device does not match the {model}"
        raise ValueError(msg)
    checks = report["checks"]
    hw.require_exact_keys(checks, set(spec["checks"]), f"{model} checks")
    failed = sorted(name for name, result in checks.items() if result != "pass")
    if failed:
        raise ValueError(f"{model} hardware checks did not pass: " + ", ".join(failed))
    hw.validate_evidence(path.parent.resolve(), report["evidence"], model, spec["evidence"])


def validate(asset: Path, reports: Path) -> None:
    """Raise ValueError unless every model has a passing report for asset in reports."""
    if not asset.is_file():
        msg = f"{asset} is missing"
        raise ValueError(msg)
    asset_sha = hw.sha256(asset)
    for model in MODELS:
        path = reports / f"hardware-{model}.json"
        if not path.is_file():
            msg = f"no {model} hardware report ({path.name}) for this release"
            raise ValueError(msg)
        validate_report(path, asset, asset_sha, model)
        release_gate.validate(model, "stu", "production", "promotion",
                              release_gate.Evidence(hardware=True, wla_rf_config=model == "wla"))


def main() -> None:
    """Check the reports given on the command line and report the approval."""
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--asset", required=True, type=Path)
    parser.add_argument("--reports", required=True, type=Path)
    args = parser.parse_args()
    try:
        validate(args.asset, args.reports)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        msg = f"RELEASE APPROVAL REJECT: {error}"
        raise SystemExit(msg) from error
    print(f"RELEASE APPROVAL OK: ST10, ST20, WLA and ST300 passed on {args.asset.name}")


if __name__ == "__main__":
    main()
