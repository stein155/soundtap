# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "support"))
sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tools/release"))
import hardware_evidence
import stu_check
from installer.test_stu_check import valid_stu


class HardwareEvidenceTests(unittest.TestCase):
    COMMIT = "a" * 40

    def fixture(self, root, target="st10"):
        artifact = root / "Update.stu"
        artifact.write_bytes(b"candidate")
        evidence = {}
        for name in hardware_evidence.REQUIRED_EVIDENCE:
            path = root / f"hardware-{target}-{name}.txt"
            path.write_text(name)
            evidence[name] = {
                "path": path.name,
                "sha256": hardware_evidence.sha256(path),
            }
        report = {
            "schema_version": 1,
            "target": target,
            "completed_at": "2026-08-17T12:00:00Z",
            "git_commit": self.COMMIT,
            "artifact": {
                "name": artifact.name,
                "sha256": hardware_evidence.sha256(artifact),
            },
            "device": hardware_evidence.TARGETS[target]["device"],
            "checks": dict.fromkeys(hardware_evidence.TARGETS[target]["checks"], "pass"),
            "evidence": evidence,
        }
        report_path = root / "report.json"
        report_path.write_text(json.dumps(report))
        return report_path, artifact, report

    def validate(self, report, artifact, commit=None):
        with mock.patch.object(hardware_evidence, "validate_source"), \
                mock.patch.object(hardware_evidence, "validate_artifact_provenance"):
            hardware_evidence.validate(
                report, artifact, artifact, commit or self.COMMIT, Path("/repo")
            )

    def test_complete_exact_report_passes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report, artifact, _ = self.fixture(root)
            self.validate(report, artifact)

    def test_complete_st20_report_passes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report_path, artifact, _report = self.fixture(root, "st20")
            self.validate(report_path, artifact)

    def test_artifact_or_commit_mismatch_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report, artifact, _ = self.fixture(root)
            artifact.write_bytes(b"different")
            with self.assertRaisesRegex(ValueError, "artifact bytes"):
                self.validate(report, artifact)
            with self.assertRaisesRegex(ValueError, "release commit"):
                self.validate(report, artifact, "b" * 40)

    def test_missing_failed_or_extra_check_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report_path, artifact, report = self.fixture(root)
            report["checks"][hardware_evidence.REQUIRED_CHECKS[0]] = "skip"
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "did not pass"):
                self.validate(report_path, artifact)
            report["checks"]["invented"] = "pass"
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "keys differ"):
                self.validate(report_path, artifact)

    def test_evidence_escape_and_hash_mismatch_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report_path, artifact, report = self.fixture(root)
            report["evidence"]["dmesg"]["path"] = "../outside"
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "asset name"):
                self.validate(report_path, artifact)
            report["evidence"]["dmesg"]["path"] = "hardware-st10-dmesg.txt"
            report["evidence"]["dmesg"]["sha256"] = "0" * 64
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "hash mismatch"):
                self.validate(report_path, artifact)

    def test_non_object_report_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report = root / "report.json"
            artifact = root / "Update.stu"
            report.write_text("[]")
            artifact.write_bytes(b"candidate")
            with self.assertRaisesRegex(ValueError, "report must be an object"):
                self.validate(report, artifact)

    def test_duplicate_keys_and_invalid_timestamp_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report_path, artifact, report = self.fixture(root)
            report_path.write_text('{"schema_version": 1, "schema_version": 1}')
            with self.assertRaisesRegex(ValueError, "duplicate JSON key"):
                self.validate(report_path, artifact)
            report["completed_at"] = "2026-99-99T99:99:99Z"
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "valid UTC"):
                self.validate(report_path, artifact)

    def test_empty_or_duplicate_evidence_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            report_path, artifact, report = self.fixture(root)
            empty = root / "empty.txt"
            empty.write_bytes(b"")
            report["evidence"]["dmesg"] = {
                "path": "hardware-st10-empty.txt",
                "sha256": hardware_evidence.sha256(empty),
            }
            empty.rename(root / "hardware-st10-empty.txt")
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "empty"):
                self.validate(report_path, artifact)
            report["evidence"]["dmesg"] = report["evidence"]["serial_log"]
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "unique files"):
                self.validate(report_path, artifact)

    def test_source_commit_and_cleanliness_are_enforced(self):
        with mock.patch.object(
            hardware_evidence.subprocess, "check_output",
            side_effect=["b" * 40 + "\n"],
        ), self.assertRaisesRegex(ValueError, "report commit"):
            hardware_evidence.validate_source(Path("/repo"), self.COMMIT)

    def test_artifact_provenance_is_model_specific(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            st10 = root / "st10.stu"
            st20 = root / "st20.stu"
            st10.write_bytes(valid_stu())
            st20.write_bytes(valid_stu(stu_check.ST20_MODULES))
            hashes = {
                "st10": hardware_evidence.sha256(st10),
                "st20": hardware_evidence.sha256(st20),
            }
            hardware_evidence.validate_artifact_provenance(
                st10, st10, "st10", hashes
            )
            hardware_evidence.validate_artifact_provenance(
                st20, st20, "st20", hashes
            )
            with self.assertRaisesRegex(ValueError, "module chain mismatch"):
                hardware_evidence.validate_artifact_provenance(
                    st10, st20, "st20", {**hashes, "st20": hashes["st20"]}
                )
        with mock.patch.object(
            hardware_evidence.subprocess, "check_output",
            side_effect=[self.COMMIT + "\n", " M changed\n"],
        ), self.assertRaisesRegex(ValueError, "clean checkout"):
            hardware_evidence.validate_source(Path("/repo"), self.COMMIT)


if __name__ == "__main__":
    unittest.main()
