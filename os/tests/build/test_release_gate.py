# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Tests for the release gate tools/release/release_gate.py."""

import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("release_gate", ROOT / "tools/release/release_gate.py")
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)


HARDWARE = gate.Evidence(hardware=True)


class ReleaseGateTest(unittest.TestCase):
    """Which model, artifact, profile and phase combinations the gate lets through."""

    def test_st10_stu_is_allowed_in_both_profiles(self) -> None:
        """An ST10 STU passes the candidate gate in both profiles."""
        for profile in gate.PROFILES:
            with self.subTest(profile=profile):
                gate.validate("st10", "stu", profile)

    def test_st10_nand_is_allowed(self) -> None:
        """An ST10 NAND image passes the candidate gate."""
        gate.validate("st10", "nand", "alpha")

    def test_st20_stu_is_a_known_candidate_in_both_profiles(self) -> None:
        """An ST20 STU passes the candidate gate in both profiles."""
        for profile in gate.PROFILES:
            with self.subTest(profile=profile):
                gate.validate("st20", "stu", profile)

    def test_st20_nand_is_always_rejected(self) -> None:
        """An ST20 NAND image is rejected in every phase, even with hardware evidence."""
        for phase in gate.PHASES:
            with self.subTest(phase=phase), self.assertRaisesRegex(
                SystemExit, "ST20 NAND",
            ):
                gate.validate("st20", "nand", "alpha", phase, HARDWARE)

    def test_wla_promotion_rejects_hdk_rf_fallback_without_exact_config_evidence(self) -> None:
        """A WLA promotion needs its exact RF configuration evidence besides hardware evidence."""
        gate.validate("wla", "stu", "alpha")
        with self.assertRaisesRegex(SystemExit, "WLA NAND"):
            gate.validate("wla", "nand", "alpha")
        with self.assertRaisesRegex(SystemExit, "exact WLA RF configuration evidence"):
            gate.validate("wla", "stu", "alpha", "promotion", HARDWARE)
        gate.validate("wla", "stu", "alpha", "promotion", gate.Evidence(hardware=True, wla_rf_config=True))

    def test_st300_nand_is_rejected(self) -> None:
        """An ST300 STU passes the candidate gate and its NAND image is rejected."""
        gate.validate("st300", "stu", "alpha")
        with self.assertRaisesRegex(SystemExit, "ST300 NAND"):
            gate.validate("st300", "nand", "alpha")

    def test_promotion_requires_explicit_validated_hardware_evidence(self) -> None:
        """A promotion fails without validated hardware evidence and passes with it."""
        for target in ("st10", "st20", "st300"):
            with self.subTest(target=target):
                with self.assertRaisesRegex(SystemExit, "hardware evidence"):
                    gate.validate(target, "stu", "alpha", "promotion")
                gate.validate(target, "stu", "alpha", "promotion", HARDWARE)

    def test_unknown_arguments_are_rejected(self) -> None:
        """A typo fails the gate: validate() is called directly, without argparse's choices checking."""
        for args in (
            ("st30", "stu", "alpha"),
            ("st10", "img", "alpha"),
            ("st10", "stu", "beta"),
            ("", "stu", "alpha"),
            ("ST10", "stu", "alpha"),
        ):
            with self.subTest(args=args), self.assertRaises(SystemExit):
                gate.validate(*args)
        with self.assertRaises(SystemExit):
            gate.validate("st10", "stu", "alpha", "publish")


if __name__ == "__main__":
    unittest.main()
