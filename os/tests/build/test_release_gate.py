# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("release_gate", ROOT / "tools/release/release_gate.py")
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)


class ReleaseGateTest(unittest.TestCase):
    def test_st10_stu_is_allowed_in_both_profiles(self):
        for profile in gate.PROFILES:
            with self.subTest(profile=profile):
                gate.validate("st10", "stu", profile)

    def test_st10_nand_is_allowed(self):
        gate.validate("st10", "nand", "alpha")

    def test_st20_stu_is_a_known_candidate_in_both_profiles(self):
        for profile in gate.PROFILES:
            with self.subTest(profile=profile):
                gate.validate("st20", "stu", profile)

    def test_st20_nand_is_always_rejected(self):
        for phase in gate.PHASES:
            with self.subTest(phase=phase), self.assertRaisesRegex(
                SystemExit, "ST20 NAND"
            ):
                gate.validate("st20", "nand", "alpha", phase, True)

    def test_wla_promotion_rejects_hdk_rf_fallback_without_exact_config_evidence(self):
        gate.validate("wla", "stu", "alpha")
        with self.assertRaisesRegex(SystemExit, "WLA NAND"):
            gate.validate("wla", "nand", "alpha")
        with self.assertRaisesRegex(SystemExit, "exact WLA RF configuration evidence"):
            gate.validate("wla", "stu", "alpha", "promotion", True)
        gate.validate("wla", "stu", "alpha", "promotion", True, True)

    def test_st300_nand_is_rejected(self):
        gate.validate("st300", "stu", "alpha")
        with self.assertRaisesRegex(SystemExit, "ST300 NAND"):
            gate.validate("st300", "nand", "alpha")

    def test_promotion_requires_explicit_validated_hardware_evidence(self):
        for target in ("st10", "st20", "st300"):
            with self.subTest(target=target):
                with self.assertRaisesRegex(SystemExit, "hardware evidence"):
                    gate.validate(target, "stu", "alpha", "promotion")
                gate.validate(target, "stu", "alpha", "promotion", True)

    def test_unknown_arguments_are_rejected(self):
        # validate() is imported directly by tests and build scripts, which do not get
        # argparse's choices checking. A typo must fail the gate, not pass it.
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
