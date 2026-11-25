#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Validate completed model-specific hardware evidence against exact STU bytes."""

import argparse
import datetime
import hashlib
import json
import re
import subprocess
import sys
from collections.abc import Iterable
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "os/support"))
import stu_check  # noqa: E402

ST10_CHECKS = (
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
    "buttons",
    "infrared",
    "leds",
    "bluetooth",
    "airplay",
    "apple-home",
    "spotify",
    "wifi",
    "security",
    "soak-24h",
    "official-usb-recovery",
    "serial-slot-rollback",
)
ST20_CHECKS = (
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
    "buttons",
    "infrared",
    "oled",
    "bco-network",
    "bco-stock-payloads",
    "airplay",
    "spotify",
    "wifi",
    "security",
    "soak-24h",
    "official-usb-recovery",
    "serial-slot-rollback",
)
TARGETS = {
    "st10": {
        "device": {
            "model": "SoundTouch 10", "variant": "rhino",
            "module_type": "sm2", "spare": True,
        },
        "checks": ST10_CHECKS,
    },
    "st20": {
        "device": {
            "model": "SoundTouch 20", "variant": "lisa",
            "module_type": "scm", "spare": True,
        },
        "checks": ST20_CHECKS,
    },
}
STOCK_STU_SHA256 = {
    "st10": "cc36bd1345ae1fce66b7ffd9186e762f07d49f271358c659a89b62550359d22e",
    "st20": "9cdff040f7e2f4d001d1ec1cab28ce27b3606814ff60e0a45f49d5f8c5cf5ab6",
}
# Compatibility alias for report tooling that predates ST20 candidate support.
REQUIRED_CHECKS = ST10_CHECKS
REQUIRED_EVIDENCE = ("serial_log", "dmesg", "soundtap_log", "network", "mounts")
SHA256 = re.compile(r"[0-9a-f]{64}")
COMMIT = re.compile(r"[0-9a-f]{40}")
TIMESTAMP = re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z")


class ReportShapeError(ValueError):
    """A report object is not an object or does not hold exactly the expected keys."""


def sha256(path: Path) -> str:
    """Return the SHA-256 of the file at path, in hex."""
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def require_exact_keys(value: dict, expected: set[str], label: str) -> None:
    """Raise ValueError unless value is an object with exactly the expected keys."""
    if not isinstance(value, dict):
        msg = f"{label} must be an object"
        raise ReportShapeError(msg)
    if set(value) != expected:
        missing = sorted(expected - set(value))
        extra = sorted(set(value) - expected)
        msg = f"{label} keys differ; missing={missing}, extra={extra}"
        raise ReportShapeError(msg)


def reject_duplicate_keys(pairs: list[tuple[str, object]]) -> dict:
    """Build a JSON object from pairs, raising ValueError on a repeated key."""
    result = {}
    for name, value in pairs:
        if name in result:
            msg = f"duplicate JSON key: {name}"
            raise ValueError(msg)
        result[name] = value
    return result


def load_report(path: Path) -> dict:
    """Load the JSON report at path, refusing duplicate keys."""
    return json.loads(path.read_text(), object_pairs_hook=reject_duplicate_keys)


def validate_source(repo: Path, commit: str) -> None:
    """Raise ValueError unless repo is a clean checkout of commit."""
    repo = repo.resolve()
    git = ["git", "-c", f"safe.directory={repo}", "-C", str(repo)]
    actual = subprocess.check_output([*git, "rev-parse", "HEAD"], text=True).strip()
    if actual != commit:
        msg = "checkout does not match the hardware report commit"
        raise ValueError(msg)
    status = subprocess.check_output(
        [*git, "status", "--porcelain", "--untracked-files=all"], text=True,
    )
    if status:
        msg = "hardware evidence must be validated from a clean checkout"
        raise ValueError(msg)


def validate_artifact_provenance(
    artifact_path: Path,
    stock_path: Path,
    target: str,
    stock_hashes: dict[str, str] = STOCK_STU_SHA256,
) -> None:
    """Raise ValueError unless the artifact is a target STU that changes only kernel and rootfs from the approved stock STU."""
    stock_path = stock_path.resolve()
    if not stock_path.is_file() or sha256(stock_path) != stock_hashes[target]:
        msg = f"{target} stock STU is missing or not the approved recovery input"
        raise ValueError(msg)
    artifact = bytearray(artifact_path.read_bytes())
    stock = bytearray(stock_path.read_bytes())
    found = stu_check.validate(artifact, target)
    detected, _profile = stu_check.profile_for_names([module[3] for module in found], target)
    if detected != target:
        msg = "artifact STU model does not match the hardware report target"
        raise ValueError(msg)
    changed = {"linuxPatched", "ubi.img"}
    stu_check.validate_recovery(stock, artifact, changed, target)


def validate(
    report_path: Path, artifact_path: Path, stock_path: Path, commit: str, repo: Path,
) -> None:
    """Raise ValueError unless the report proves every required check on the exact artifact bytes."""
    report_path = report_path.resolve()
    artifact_path = artifact_path.resolve()
    report = load_report(report_path)
    require_exact_keys(
        report,
        {
            "schema_version", "target", "completed_at", "git_commit",
            "artifact", "device", "checks", "evidence",
        },
        "report",
    )
    target = report["target"]
    if report["schema_version"] != 1 or target not in TARGETS:
        msg = "hardware report must use schema 1 for target st10 or st20"
        raise ValueError(msg)
    completed_at = report["completed_at"]
    if not isinstance(completed_at, str) or not TIMESTAMP.fullmatch(completed_at):
        msg = "completed_at must be an exact UTC timestamp"
        raise ValueError(msg)
    try:
        datetime.datetime.strptime(completed_at, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.UTC)
    except ValueError as error:
        msg = "completed_at is not a valid UTC timestamp"
        raise ValueError(msg) from error
    if (
        not isinstance(commit, str) or not COMMIT.fullmatch(commit)
        or report["git_commit"] != commit
    ):
        msg = "hardware report does not match the release commit"
        raise ValueError(msg)
    validate_source(repo, commit)
    if not artifact_path.is_file():
        msg = "tested artifact is missing"
        raise ValueError(msg)

    artifact = report["artifact"]
    require_exact_keys(artifact, {"name", "sha256"}, "artifact")
    if artifact["name"] != artifact_path.name:
        msg = "hardware report names a different artifact"
        raise ValueError(msg)
    if not isinstance(artifact["sha256"], str) or not SHA256.fullmatch(
        artifact["sha256"],
    ):
        msg = "artifact SHA-256 is not canonical"
        raise ValueError(msg)
    if sha256(artifact_path) != artifact["sha256"]:
        msg = "hardware report does not match the tested artifact bytes"
        raise ValueError(msg)
    validate_artifact_provenance(artifact_path, stock_path, target)

    device = report["device"]
    require_exact_keys(device, {"model", "variant", "module_type", "spare"}, "device")
    if device != TARGETS[target]["device"]:
        msg = f"hardware report device does not match target {target}"
        raise ValueError(msg)

    checks = report["checks"]
    require_exact_keys(checks, set(TARGETS[target]["checks"]), "checks")
    failed = sorted(name for name, result in checks.items() if result != "pass")
    if failed:
        raise ValueError("hardware checks did not pass: " + ", ".join(failed))

    validate_evidence(report_path.parent, report["evidence"], target, REQUIRED_EVIDENCE)


def validate_evidence(report_dir: Path, evidence: dict, target: str, required: Iterable[str]) -> None:
    """Check that every required evidence file sits next to the report, is non-empty and hashes as recorded."""
    require_exact_keys(evidence, set(required), "evidence")
    evidence_paths = set()
    for name, record in evidence.items():
        require_exact_keys(record, {"path", "sha256"}, f"evidence {name}")
        if not isinstance(record["sha256"], str) or not SHA256.fullmatch(
            record["sha256"],
        ):
            msg = f"evidence {name} SHA-256 is not canonical"
            raise ValueError(msg)
        if not isinstance(record["path"], str) or not record["path"]:
            msg = f"evidence {name} path must be a nonempty string"
            raise ValueError(msg)
        relative = Path(record["path"])
        if (
            relative.is_absolute()
            or relative != Path(relative.name)
            or not relative.name.startswith(f"hardware-{target}-")
        ):
            msg = f"evidence {name} path must be one hardware-{target}-* asset name"
            raise ValueError(
                msg,
            )
        path = (report_dir / relative).resolve()
        if report_dir not in path.parents or not path.is_file():
            msg = f"evidence {name} path is missing or escapes the report directory"
            raise ValueError(msg)
        if path in evidence_paths:
            msg = "evidence records must reference unique files"
            raise ValueError(msg)
        evidence_paths.add(path)
        if path.stat().st_size == 0:
            msg = f"evidence {name} file is empty"
            raise ValueError(msg)
        if sha256(path) != record["sha256"]:
            msg = f"evidence {name} hash mismatch"
            raise ValueError(msg)


def main() -> None:
    """Validate the report given on the command line and report the result."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--artifact", required=True, type=Path)
    parser.add_argument("--stock-stu", required=True, type=Path)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--repo", type=Path, default=ROOT)
    args = parser.parse_args()
    try:
        validate(args.report, args.artifact, args.stock_stu, args.commit, args.repo)
    except (OSError, ValueError, json.JSONDecodeError, subprocess.SubprocessError) as error:
        msg = f"HARDWARE EVIDENCE REJECT: {error}"
        raise SystemExit(msg) from error
    print("HARDWARE EVIDENCE OK: exact model artifact and all required checks")


if __name__ == "__main__":
    main()
